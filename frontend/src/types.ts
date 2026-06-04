export interface Member {
  id: string;
  name: string;
  online: boolean;
  muted: boolean;
  sharing: boolean;
}

export type ConnectionState = 'disconnected' | 'connecting' | 'connected';

export interface NetworkStats {
  latency: number | null;
  bitrate: number | null;
  connectionState: ConnectionState;
  connectionType: string;
}

export interface VideoCapability {
  backend: string;
  status: string;
  adapter: string;
  driver: string;
  reason: string;
  supportsHost: boolean;
  supportsView: boolean;
}

export interface AudioCapability {
  supportsProcessCapture: boolean;
  reason: string;
}

export interface RuntimeStatus {
  mode: string;
  networkBackend: string;
  networkState: string;
  networkAuthUrl: string;
  networkHealth: string[];
  mediaHealth: string[];
  videoBackend: string;
  videoStatus: string;
  audioBackend: string;
  audioStatus: string;
  version: string;
}

export interface UserPreferences {
  displayName: string;
}

export interface RoomState {
  roomId: string | null;
  inRoom: boolean;
  sharing: boolean;
  selfPeerId: string | null;
  selfSharing: boolean;
  hostVideoCapable: boolean;
  canShareToPeers: boolean;
}

export interface AudioState {
  micEnabled: boolean;
  volume: number;
  speakerEnabled: boolean;
}

export interface AudioShareSource {
  pid: number;
  name: string;
  displayName: string;
  iconDataUrl: string;
  active: boolean;
  selected: boolean;
}

export interface AvailableNode {
  id: string;
  hostname: string;
  online: boolean;
  ip: string;
  joinable: boolean;
  roomId?: string;
}

export interface StreamFrame {
  peerId: string;
  width: number;
  height: number;
  timestamp: number;
  format: 'jpeg';
  data: string;
  decodedFps?: number;
  estimatedSourceFps?: number;
  incomingBitrateKbps?: number;
}

export interface StreamSettings {
  peerId: string;
  width: number;
  height: number;
  fps: number;
}

export type MediaSignalKind = 'offer' | 'answer' | 'ice' | 'close' | 'renegotiate' | 'error';

export interface MediaSignal {
  sessionId: string;
  fromPeerId: string;
  toPeerId: string;
  kind: MediaSignalKind;
  media: 'screen' | string;
  payload?: unknown;
}

export interface WebRTCPlaybackStats {
  decodedFps: number | null;
  incomingBitrateKbps: number | null;
  packetsLost: number | null;
  jitterMs: number | null;
  framesDropped: number | null;
}

export type VoiceStatus = 'idle' | 'starting' | 'active' | 'muted' | 'error';

export interface VoiceChatState {
  active: boolean;
  muted: boolean;
  volume: number;
  state: number;
  peerId: string;
  experimental: boolean;
  message: string;
  status: VoiceStatus;
  error: string | null;
}
