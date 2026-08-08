import type { WebRTCPlaybackStats } from '../types';

type ReceiverState = 'idle' | 'connecting' | 'connected' | 'closed' | 'error';

export interface GameCastSignal {
  type?: string;
  sessionId?: string;
  sdp?: unknown;
  ice?: unknown;
  iceServers?: RTCIceServer[];
  error?: string;
}

export interface GameCastReceiverOptions {
  serverBaseUrl: string;
  onSignal: (signal: Record<string, unknown>) => Promise<void> | void;
  onRemoteStream?: (stream: MediaStream) => void;
  onStateChange?: (state: ReceiverState, detail?: string) => void;
  onStats?: (stats: WebRTCPlaybackStats) => void;
}

export interface GameCastVideoCounters {
  bytesReceived: number;
  packetsReceived: number;
  packetsLost: number;
  framesDecoded: number | null;
  framesDropped: number;
  timestamp: number;
}

export function computeGameCastVideoStats(
  report: RTCStatsReport | Map<string, Record<string, unknown>>,
  previous: GameCastVideoCounters | null,
  generation: string,
): { stats: WebRTCPlaybackStats; counters: GameCastVideoCounters | null } {
  let pairId = '';
  const videoEntries: Array<Record<string, unknown>> = [];
  const candidatePairs: Array<Record<string, unknown>> = [];
  report.forEach((entry) => {
    const stat = entry as unknown as Record<string, unknown>;
    if (stat.type === 'transport' && typeof stat.selectedCandidatePairId === 'string') {
      pairId = stat.selectedCandidatePairId;
    }
    if (stat.type === 'inbound-rtp' && stat.kind === 'video') {
      videoEntries.push(stat);
    }
    if (
      stat.type === 'candidate-pair' && stat.state === 'succeeded' &&
      (stat.selected === true || stat.nominated === true)
    ) {
      candidatePairs.push(stat);
    }
  });
  if (!pairId && typeof candidatePairs[0]?.id === 'string') {
    pairId = candidatePairs[0].id;
  }
  const video = videoEntries[0];

  const empty: WebRTCPlaybackStats = {
    decodedFps: null,
    incomingBitrateKbps: null,
    packetsLost: null,
    jitterMs: null,
    framesDropped: null,
    valid: false,
    invalidReason: 'baseline',
    generation,
    pairId,
    packetsReceivedDelta: null,
    packetsLostDelta: null,
    framesDroppedDelta: null,
  };
  if (!video) {
    return { stats: { ...empty, invalidReason: 'no-video-stats' }, counters: previous };
  }

  const number = (value: unknown, fallback = 0): number =>
    typeof value === 'number' && Number.isFinite(value) ? value : fallback;
  const counters: GameCastVideoCounters = {
    bytesReceived: number(video.bytesReceived),
    packetsReceived: number(video.packetsReceived),
    packetsLost: number(video.packetsLost),
    framesDecoded: typeof video.framesDecoded === 'number' ? video.framesDecoded : null,
    framesDropped: number(video.framesDropped),
    timestamp: number(video.timestamp),
  };
  const stats: WebRTCPlaybackStats = {
    ...empty,
    packetsLost: counters.packetsLost,
    jitterMs: number(video.jitter) * 1000,
    framesDropped: counters.framesDropped,
  };
  if (!previous) {
    return { stats, counters };
  }

  const elapsedSeconds = (counters.timestamp - previous.timestamp) / 1000;
  const bytesDelta = counters.bytesReceived - previous.bytesReceived;
  const receivedDelta = counters.packetsReceived - previous.packetsReceived;
  const lostDelta = counters.packetsLost - previous.packetsLost;
  const droppedDelta = counters.framesDropped - previous.framesDropped;
  const decodedDelta =
    counters.framesDecoded !== null && previous.framesDecoded !== null
      ? counters.framesDecoded - previous.framesDecoded
      : null;
  if (
    !(elapsedSeconds > 0) || elapsedSeconds > 10 || bytesDelta < 0 || receivedDelta < 0 ||
    lostDelta < 0 || droppedDelta < 0 || (decodedDelta !== null && decodedDelta < 0)
  ) {
    return { stats: { ...stats, invalidReason: elapsedSeconds > 10 ? 'sample-gap' : 'counter-reset' }, counters };
  }
  if (receivedDelta + lostDelta < 20) {
    return { stats: { ...stats, invalidReason: 'insufficient-packets' }, counters };
  }

  return {
    counters,
    stats: {
      ...stats,
      valid: true,
      invalidReason: '',
      incomingBitrateKbps: (bytesDelta * 8) / elapsedSeconds / 1000,
      decodedFps: decodedDelta === null ? null : decodedDelta / elapsedSeconds,
      packetsReceivedDelta: receivedDelta,
      packetsLostDelta: lostDelta,
      framesDroppedDelta: droppedDelta,
    },
  };
}

export class GameCastReceiver {
  private pc: RTCPeerConnection | null = null;
  private remoteStream: MediaStream | null = null;
  private pendingIce: Array<{ candidate: RTCIceCandidateInit; sessionId: string | null }> = [];
  private sessionId: string | null = null;
  private statsTimer: number | null = null;
  private lastVideoStats: GameCastVideoCounters | null = null;
  private generation = '';
  private readonly options: GameCastReceiverOptions;

  constructor(options: GameCastReceiverOptions) {
    this.options = options;
  }

  async handleSignal(signal: GameCastSignal): Promise<void> {
    if (signal.error) {
      this.options.onStateChange?.('error', signal.error);
      return;
    }

    switch (signal.type) {
      case 'offer':
        await this.acceptOffer(signal);
        return;
      case 'ice-candidate':
        await this.addIceCandidate(signal);
        return;
      case 'endshare':
        this.endShare(signal);
        return;
      default:
        return;
    }
  }

  close(detail?: string): void {
    this.stopStats();
    this.pendingIce = [];
    this.lastVideoStats = null;
    this.generation = '';
    this.sessionId = null;
    this.remoteStream = null;
    this.closePeer();
    this.options.onStateChange?.('closed', detail);
  }

  private createPeerConnection(iceServers?: RTCIceServer[]): RTCPeerConnection {
    this.closePeer();
    this.remoteStream = new MediaStream();
    const pc = new RTCPeerConnection({
      iceServers: Array.isArray(iceServers) ? iceServers : buildIceServers(this.options.serverBaseUrl),
    });
    this.pc = pc;
    this.generation = `${Date.now()}-${Math.random().toString(36).slice(2)}`;

    pc.onicecandidate = (event) => {
      if (this.pc !== pc || !event.candidate) {
        return;
      }
      const signal: Record<string, unknown> = {
        type: 'ice-candidate',
        ice: event.candidate.toJSON(),
      };
      if (this.sessionId) {
        signal.sessionId = this.sessionId;
      }
      void this.options.onSignal(signal);
    };

    pc.ontrack = (event) => {
      if (this.pc !== pc) {
        return;
      }
      const stream = this.remoteStream || new MediaStream();
      this.remoteStream = stream;
      if (!stream.getTracks().some((track) => track.id === event.track.id)) {
        stream.addTrack(event.track);
      }
      this.options.onRemoteStream?.(stream);
      this.options.onStateChange?.('connected', 'receiving media');
    };

    pc.onconnectionstatechange = () => {
      if (this.pc !== pc) {
        return;
      }
      if (pc.connectionState === 'connected') {
        this.options.onStateChange?.('connected');
        this.startStats();
        return;
      }
      if (pc.connectionState === 'failed') {
        this.options.onStateChange?.('error', 'webrtc connection failed');
      }
      if (pc.connectionState === 'closed') {
        this.options.onStateChange?.('closed');
      }
    };

    return pc;
  }

  private async acceptOffer(signal: GameCastSignal): Promise<void> {
    const offer = parseSessionDescription(signal.sdp, 'offer');
    if (!offer) {
      this.options.onStateChange?.('error', 'invalid gamecast offer');
      return;
    }

    const nextSessionId = normalizeSessionId(signal.sessionId);
    this.pendingIce = this.pendingIce.filter((pending) => pending.sessionId === nextSessionId);
    this.stopStats();
    this.lastVideoStats = null;
    this.remoteStream = null;
    this.closePeer();
    this.sessionId = nextSessionId;
    this.options.onStateChange?.('connecting');
    const pc = this.createPeerConnection(signal.iceServers);
    await pc.setRemoteDescription(offer);
    if (this.pc !== pc) {
      return;
    }
    await this.flushPendingIce(pc);
    if (this.pc !== pc) {
      return;
    }
    const answer = await pc.createAnswer();
    if (this.pc !== pc) {
      return;
    }
    await pc.setLocalDescription(answer);
    if (this.pc !== pc) {
      return;
    }
    const answerSignal: Record<string, unknown> = {
      type: 'answer',
      sdp: answer.sdp,
    };
    if (this.sessionId) {
      answerSignal.sessionId = this.sessionId;
    }
    await this.options.onSignal(answerSignal);
  }

  private closePeer(): void {
    const pc = this.pc;
    this.pc = null;
    if (pc) {
      pc.close();
    }
  }

  private async addIceCandidate(signal: GameCastSignal): Promise<void> {
    const candidate = parseIceCandidate(signal.ice);
    if (!candidate) {
      return;
    }
    const candidateSessionId = normalizeSessionId(signal.sessionId);
    if (this.sessionId !== null && candidateSessionId !== this.sessionId) {
      return;
    }
    if (!this.pc || !this.pc.remoteDescription) {
      this.pendingIce.push({ candidate, sessionId: candidateSessionId });
      return;
    }
    await this.pc.addIceCandidate(candidate);
  }

  private async flushPendingIce(pc: RTCPeerConnection): Promise<void> {
    if (this.pc !== pc || !pc.remoteDescription || this.pendingIce.length === 0) {
      return;
    }
    const queue = this.pendingIce.filter((pending) => pending.sessionId === this.sessionId);
    this.pendingIce = [];
    for (const pending of queue) {
      if (this.pc !== pc) {
        return;
      }
      await pc.addIceCandidate(pending.candidate);
    }
  }

  private endShare(signal: GameCastSignal): void {
    const endedSessionId = normalizeSessionId(signal.sessionId);
    if (endedSessionId !== this.sessionId) {
      return;
    }
    this.close('share ended');
  }

  private startStats(): void {
    this.stopStats();
    this.statsTimer = window.setInterval(() => {
      void this.emitStats();
    }, 1000);
  }

  private stopStats(): void {
    if (this.statsTimer !== null) {
      window.clearInterval(this.statsTimer);
      this.statsTimer = null;
    }
  }

  private async emitStats(): Promise<void> {
    const pc = this.pc;
    if (!pc) {
      return;
    }

    const generation = this.generation;
    const report = await pc.getStats();
    if (this.pc !== pc || this.generation !== generation) {
      return;
    }
    const result = computeGameCastVideoStats(report, this.lastVideoStats, this.generation);
    this.lastVideoStats = result.counters;
    this.options.onStats?.(result.stats);
  }
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

function normalizeSessionId(value: unknown): string | null {
  if (typeof value !== 'string') {
    return null;
  }
  const normalized = value.trim();
  return normalized || null;
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
