import type { MediaSignal, WebRTCPlaybackStats } from '../types';

type ReceiverState = 'idle' | 'connecting' | 'connected' | 'closed' | 'error';

interface WailsAppBridge {
  SendMediaSignal?: (signal: MediaSignal) => Promise<void>;
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: WailsAppBridge;
      };
    };
  }
}

export interface WebRTCReceiverOptions {
  selfPeerId: string;
  iceServers?: RTCIceServer[];
  onRemoteStream?: (stream: MediaStream, signal: MediaSignal) => void;
  onStateChange?: (state: ReceiverState, detail?: string) => void;
  onStats?: (stats: WebRTCPlaybackStats) => void;
}

export class WebRTCReceiver {
  private pc: RTCPeerConnection | null = null;
  private statsTimer: number | null = null;
  private lastVideoStats: { bytesReceived: number; timestamp: number; framesDecoded: number | null } | null = null;
  private readonly options: WebRTCReceiverOptions;

  constructor(options: WebRTCReceiverOptions) {
    this.options = options;
  }

  async handleSignal(signal: MediaSignal): Promise<void> {
    if (signal.kind === 'offer') {
      await this.acceptOffer(signal);
      return;
    }

    if (signal.kind === 'ice') {
      await this.addIceCandidate(signal);
      return;
    }

    if (signal.kind === 'close') {
      this.close('remote closed');
    }
  }

  close(detail?: string): void {
    this.stopStats();
    this.lastVideoStats = null;
    if (this.pc) {
      this.pc.close();
      this.pc = null;
    }
    this.options.onStateChange?.('closed', detail);
  }

  private async acceptOffer(signal: MediaSignal): Promise<void> {
    const offer = asSessionDescription(signal.payload);
    if (!offer) {
      this.options.onStateChange?.('error', 'invalid offer payload');
      return;
    }

    this.close();
    this.options.onStateChange?.('connecting');

    const pc = new RTCPeerConnection({
      iceServers: this.options.iceServers || [],
    });
    this.pc = pc;

    pc.onicecandidate = (event) => {
      if (!event.candidate) {
        return;
      }
      void this.sendSignal({
        sessionId: signal.sessionId,
        fromPeerId: this.options.selfPeerId,
        toPeerId: signal.fromPeerId,
        kind: 'ice',
        media: signal.media || 'screen',
        payload: event.candidate.toJSON(),
      });
    };

    pc.ontrack = (event) => {
      const [stream] = event.streams;
      if (stream) {
        this.options.onRemoteStream?.(stream, signal);
      }
    };

    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'connected') {
        this.options.onStateChange?.('connected');
        this.startStats();
        return;
      }
      if (pc.connectionState === 'failed') {
        this.options.onStateChange?.('error', 'webrtc connection failed');
      }
    };

    await pc.setRemoteDescription(offer);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);

    await this.sendSignal({
      sessionId: signal.sessionId,
      fromPeerId: this.options.selfPeerId,
      toPeerId: signal.fromPeerId,
      kind: 'answer',
      media: signal.media || 'screen',
      payload: answer,
    });
  }

  private async addIceCandidate(signal: MediaSignal): Promise<void> {
    if (!this.pc) {
      return;
    }
    const candidate = asIceCandidate(signal.payload);
    if (!candidate) {
      return;
    }
    await this.pc.addIceCandidate(candidate);
  }

  private async sendSignal(signal: MediaSignal): Promise<void> {
    await window.go?.main?.App?.SendMediaSignal?.(signal);
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
    if (!this.pc) {
      return;
    }

    const report = await this.pc.getStats();
    let incomingBitrateKbps: number | null = null;
    let decodedFps: number | null = null;
    let packetsLost: number | null = null;
    let jitterMs: number | null = null;
    let framesDropped: number | null = null;

    report.forEach((entry) => {
      const stat = entry as unknown as Record<string, unknown>;
      if (stat.type !== 'inbound-rtp' || stat.kind !== 'video') {
        return;
      }

      const bytesReceived = typeof stat.bytesReceived === 'number' ? stat.bytesReceived : null;
      const timestamp = typeof stat.timestamp === 'number' ? stat.timestamp : null;
      const framesDecoded = typeof stat.framesDecoded === 'number' ? stat.framesDecoded : null;

      if (bytesReceived !== null && timestamp !== null && this.lastVideoStats) {
        const elapsedSeconds = Math.max(0, (timestamp - this.lastVideoStats.timestamp) / 1000);
        if (elapsedSeconds > 0) {
          incomingBitrateKbps = ((bytesReceived - this.lastVideoStats.bytesReceived) * 8) / elapsedSeconds / 1000;
          if (framesDecoded !== null && this.lastVideoStats.framesDecoded !== null) {
            decodedFps = (framesDecoded - this.lastVideoStats.framesDecoded) / elapsedSeconds;
          }
        }
      }
      if (bytesReceived !== null && timestamp !== null) {
        this.lastVideoStats = { bytesReceived, timestamp, framesDecoded };
      }

      packetsLost = typeof stat.packetsLost === 'number' ? stat.packetsLost : packetsLost;
      jitterMs = typeof stat.jitter === 'number' ? stat.jitter * 1000 : jitterMs;
      framesDropped = typeof stat.framesDropped === 'number' ? stat.framesDropped : framesDropped;
    });

    this.options.onStats?.({
      decodedFps,
      incomingBitrateKbps,
      packetsLost,
      jitterMs,
      framesDropped,
    });
  }
}

function asSessionDescription(value: unknown): RTCSessionDescriptionInit | null {
  if (!value || typeof value !== 'object') {
    return null;
  }
  const candidate = value as Partial<RTCSessionDescriptionInit>;
  if (candidate.type !== 'offer' && candidate.type !== 'answer') {
    return null;
  }
  return {
    type: candidate.type,
    sdp: candidate.sdp,
  };
}

function asIceCandidate(value: unknown): RTCIceCandidateInit | null {
  if (!value || typeof value !== 'object') {
    return null;
  }
  const candidate = value as RTCIceCandidateInit;
  if (!candidate.candidate) {
    return null;
  }
  return candidate;
}
