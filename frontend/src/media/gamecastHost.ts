import type { ShareSettings } from '../components/RoomControls';

type HostState = 'idle' | 'capturing' | 'connecting' | 'connected' | 'closed' | 'error';

export interface GameCastHostSignal {
  type?: string;
  sdp?: unknown;
  ice?: unknown;
  error?: string;
}

export interface GameCastHostShareInfo {
  width: number;
  height: number;
  fps: number;
}

export interface GameCastHostOptions {
  serverBaseUrl: string;
  onSignal: (signal: Record<string, unknown>) => Promise<void> | void;
  onLocalStream?: (stream: MediaStream) => void;
  onStateChange?: (state: HostState, detail?: string) => void;
  onEnded?: () => void;
}

export class GameCastHost {
  private pc: RTCPeerConnection | null = null;
  private localStream: MediaStream | null = null;
  private pendingIce: RTCIceCandidateInit[] = [];
  private intentionalStop = false;
  private lastShareInfo: GameCastHostShareInfo = { width: 0, height: 0, fps: 30 };
  private readonly options: GameCastHostOptions;

  constructor(options: GameCastHostOptions) {
    this.options = options;
  }

  async capture(settings: ShareSettings): Promise<GameCastHostShareInfo> {
    if (!navigator.mediaDevices?.getDisplayMedia) {
      throw new Error('screen capture is not available in this WebView');
    }

    this.closePeer();
    this.stopLocalStream();
    this.intentionalStop = false;
    this.options.onStateChange?.('capturing');

    const stream = await navigator.mediaDevices.getDisplayMedia(buildDisplayConstraints(settings));
    await applyCaptureConstraints(stream, settings);
    this.localStream = stream;
    this.lastShareInfo = describeShare(stream, settings);
    for (const track of stream.getTracks()) {
      if (track.kind === 'video') {
        (track as MediaStreamTrack & { contentHint?: string }).contentHint = settings.fps >= 45 ? 'motion' : 'detail';
      }
      track.addEventListener('ended', () => {
        if (!this.intentionalStop) {
          this.options.onEnded?.();
        }
      });
    }
    this.options.onLocalStream?.(stream);
    return this.lastShareInfo;
  }

  async publish(): Promise<GameCastHostShareInfo> {
    if (!this.localStream) {
      throw new Error('screen capture has not started');
    }

    this.options.onStateChange?.('connecting', 'creating host offer');
    const pc = this.createPeerConnection();
    for (const track of this.localStream.getTracks()) {
      const sender = pc.addTrack(track, this.localStream);
      if (track.kind === 'video') {
        await tuneSender(sender, this.lastShareInfo);
      }
    }

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await this.options.onSignal({
      type: 'offer',
      sdp: offer.sdp,
    });
    this.options.onStateChange?.('connecting', 'waiting for sfu answer');
    return this.lastShareInfo;
  }

  async handleSignal(signal: GameCastHostSignal): Promise<void> {
    if (signal.error) {
      this.options.onStateChange?.('error', signal.error);
      return;
    }

    switch (signal.type) {
      case 'answer':
        await this.acceptAnswer(signal);
        return;
      case 'ice-candidate':
        await this.addIceCandidate(signal);
        return;
      default:
        return;
    }
  }

  stop(detail?: string): void {
    this.intentionalStop = true;
    this.closePeer();
    this.stopLocalStream();
    this.pendingIce = [];
    this.options.onStateChange?.('closed', detail);
  }

  private createPeerConnection(): RTCPeerConnection {
    this.closePeer();
    this.pendingIce = [];

    const pc = new RTCPeerConnection({
      iceServers: buildIceServers(this.options.serverBaseUrl),
    });
    this.pc = pc;

    pc.onicecandidate = (event) => {
      if (!event.candidate) {
        return;
      }
      void this.options.onSignal({
        type: 'ice-candidate',
        ice: event.candidate.toJSON(),
      });
    };

    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'connected') {
        this.options.onStateChange?.('connected', 'sending media');
        return;
      }
      if (pc.connectionState === 'failed') {
        this.options.onStateChange?.('error', 'webrtc host connection failed');
      }
      if (pc.connectionState === 'closed') {
        this.options.onStateChange?.('closed');
      }
    };

    return pc;
  }

  private async acceptAnswer(signal: GameCastHostSignal): Promise<void> {
    const answer = parseSessionDescription(signal.sdp, 'answer');
    if (!answer || !this.pc) {
      this.options.onStateChange?.('error', 'invalid gamecast answer');
      return;
    }
    await this.pc.setRemoteDescription(answer);
    await this.flushPendingIce();
  }

  private async addIceCandidate(signal: GameCastHostSignal): Promise<void> {
    const candidate = parseIceCandidate(signal.ice);
    if (!candidate) {
      return;
    }
    if (!this.pc || !this.pc.remoteDescription) {
      this.pendingIce.push(candidate);
      return;
    }
    await this.pc.addIceCandidate(candidate);
  }

  private async flushPendingIce(): Promise<void> {
    if (!this.pc || !this.pc.remoteDescription || this.pendingIce.length === 0) {
      return;
    }
    const queue = this.pendingIce.slice();
    this.pendingIce = [];
    for (const candidate of queue) {
      await this.pc.addIceCandidate(candidate);
    }
  }

  private closePeer(): void {
    if (this.pc) {
      this.pc.close();
      this.pc = null;
    }
  }

  private stopLocalStream(): void {
    if (this.localStream) {
      for (const track of this.localStream.getTracks()) {
        track.stop();
      }
      this.localStream = null;
    }
  }
}

function buildDisplayConstraints(settings: ShareSettings): MediaStreamConstraints {
  const video: MediaTrackConstraints = {
    frameRate: {
      ideal: settings.fps,
      max: settings.fps,
    },
  };
  if (settings.width > 0 && settings.height > 0) {
    video.width = { ideal: settings.width };
    video.height = { ideal: settings.height };
  }
  return {
    video,
    audio: shouldRequestDisplayAudio(settings),
  };
}

function shouldRequestDisplayAudio(settings: ShareSettings): boolean {
  return settings.audioMode !== 'off';
}

async function applyCaptureConstraints(stream: MediaStream, settings: ShareSettings): Promise<void> {
  const [video] = stream.getVideoTracks();
  if (!video) {
    return;
  }
  const constraints: MediaTrackConstraints = {
    frameRate: {
      ideal: settings.fps,
      max: settings.fps,
    },
  };
  if (settings.width > 0 && settings.height > 0) {
    constraints.width = { ideal: settings.width };
    constraints.height = { ideal: settings.height };
  }
  try {
    await video.applyConstraints(constraints);
  } catch {
    // Some WebView2/runtime combinations only accept the initial picker constraints.
  }
}

function buildHighQualityEncoding(info: GameCastHostShareInfo): RTCRtpEncodingParameters {
  const fps = clamp(info.fps || 30, 24, 60);
  const pixels = Math.max(1280 * 720, (info.width || 1920) * (info.height || 1080));
  const scale = pixels >= 2560 * 1440 ? 1.65 : pixels >= 1920 * 1080 ? 1.35 : 1;
  const maxBitrate = Math.round((fps >= 45 ? 9000000 : 6500000) * scale);
  return {
    active: true,
    maxBitrate,
    maxFramerate: fps,
    scaleResolutionDownBy: 1,
  };
}

async function tuneSender(sender: RTCRtpSender, info: GameCastHostShareInfo): Promise<void> {
  const parameters = sender.getParameters();
  const encoding = buildHighQualityEncoding(info);
  if (parameters.encodings?.length) {
    parameters.encodings = parameters.encodings.map((current) => ({
      ...current,
      active: encoding.active,
      maxBitrate: encoding.maxBitrate,
      maxFramerate: encoding.maxFramerate,
      scaleResolutionDownBy: encoding.scaleResolutionDownBy,
    }));
  } else {
    parameters.encodings = [encoding];
  }
  (parameters as RTCRtpSendParameters & { degradationPreference?: string }).degradationPreference =
    info.fps >= 45 ? 'maintain-framerate' : 'balanced';
  try {
    await sender.setParameters(parameters);
  } catch {
    // Browser engines may reject sender tuning after addTrack; the offer still works.
  }
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function describeShare(stream: MediaStream, fallback: ShareSettings): GameCastHostShareInfo {
  const [video] = stream.getVideoTracks();
  const settings = video?.getSettings() || {};
  return {
    width: typeof settings.width === 'number' ? settings.width : fallback.width,
    height: typeof settings.height === 'number' ? settings.height : fallback.height,
    fps: typeof settings.frameRate === 'number' ? Math.round(settings.frameRate) : fallback.fps,
  };
}

function parseSessionDescription(value: unknown, fallbackType: RTCSdpType): RTCSessionDescriptionInit | null {
  if (!value) {
    return null;
  }
  if (typeof value === 'string') {
    if (value.startsWith('v=')) {
      return { type: fallbackType, sdp: value };
    }
    try {
      const parsed = JSON.parse(value) as Partial<RTCSessionDescriptionInit>;
      if (parsed?.sdp) {
        return { type: parsed.type || fallbackType, sdp: parsed.sdp };
      }
    } catch {
      return null;
    }
  }
  if (typeof value === 'object') {
    const parsed = value as Partial<RTCSessionDescriptionInit>;
    if (parsed.sdp) {
      return { type: parsed.type || fallbackType, sdp: parsed.sdp };
    }
  }
  return null;
}

function parseIceCandidate(value: unknown): RTCIceCandidateInit | null {
  if (!value || typeof value !== 'object') {
    return null;
  }
  const candidate = value as RTCIceCandidateInit;
  return candidate.candidate ? candidate : null;
}

function buildIceServers(serverBaseUrl: string): RTCIceServer[] {
  try {
    const parsed = new URL(serverBaseUrl);
    return [
      {
        urls: [`stun:${parsed.hostname}:3478`, 'stun:stun.l.google.com:19302'],
      },
    ];
  } catch {
    return [{ urls: ['stun:stun.l.google.com:19302'] }];
  }
}
