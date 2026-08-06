export interface Member {
  id: string;
  name: string;
  online: boolean;
  muted: boolean;
  sharing: boolean;
  statusLabel?: string;
  clientType?: 'web' | 'client';
  role?: 'owner' | 'member' | string;
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

export interface GameCastProfile {
  baseUrl: string;
  displayName?: string;
  trustedCertificateSha256?: string;
}

export interface GameCastProbeResult {
  ok: boolean;
  status: 'ok' | 'tls_untrusted' | 'error' | string;
  baseUrl: string;
  user?: string;
  loggedIn: boolean;
  roomName?: string;
  certificateFingerprint?: string;
  certificateTrusted: boolean;
  message?: string;
}

export interface GameCastMediaCapabilities {
  sfuViewer: boolean;
  sfuHost: boolean;
  h264: boolean;
  opus: boolean;
}

export interface GameCastRoomState {
  room: string;
  active: boolean;
  origin?: 'web' | 'client' | string;
  ownerParticipantId?: string;
  ownerName?: string;
  sharing: boolean;
  mediaMode?: 'sfu' | 'legacy-p2p' | string;
  mediaProtocolVersion?: number;
  sharePhase?: 'idle' | 'starting' | 'live' | 'stopping' | string;
  shareOwnerParticipantId?: string;
  shareOwnerName?: string;
  shareOrigin?: 'web' | 'client' | string;
  sfu: boolean;
  audioMode?: string;
  user?: string;
  ttl?: number;
  updatedAt?: string;
  participantCount?: number;
  updatedAtUnix?: number;
}

export interface GameCastPresenceParticipant {
  participantId: string;
  room: string;
  name?: string;
  clientType: 'web' | 'client' | string;
  role: 'owner' | 'member' | string;
  active: boolean;
  sharing: boolean;
  mediaProtocolVersion?: number;
  capabilities?: GameCastMediaCapabilities;
  ttl?: number;
  ageSeconds?: number;
}

export interface GameCastSessionState {
  user: string;
  loggedIn: boolean;
  authMode?: string;
  version?: string;
  roomName?: string;
}

export interface GameCastRoomSession {
  roomId: string;
  role: 'host' | 'viewer' | string;
  state: GameCastRoomState;
}

export interface GameCastStatus {
  state: 'connecting' | 'connected' | 'closed' | 'error' | string;
  detail?: string;
}

export interface GameCastShareRuntimeEvent {
  kind: 'video-stopped' | 'audio-disabled' | string;
  roomId: string;
  peerId: string;
  audioMode?: string;
  message: string;
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

export type ShareSourceKind = 'display' | 'window';
export type ShareAudioDefault = 'none' | 'system' | 'process';

export interface ShareSource {
  id: string;
  kind: ShareSourceKind;
  label: string;
  subtitle?: string;
  displayIndex?: number;
  windowHandle?: string;
  processId?: number;
  processName?: string;
  iconDataUrl?: string;
  previewDataUrl?: string;
  audioDefault: ShareAudioDefault;
  audioProcessName?: string;
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

export interface StreamStats {
  peerId: string;
  width: number;
  height: number;
  mode: string;
  encodedFps: number;
  estimatedSourceFps: number;
  bitrateKbps: number;
  sessions: number;
  requestedWidth?: number;
  requestedHeight?: number;
  requestedFps?: number;
  actualFps?: number;
  freshFps?: number;
  repeatedRatio?: number;
  encodeBudgetRatio?: number;
  adaptiveState?: string;
  adaptiveReason?: string;
  adaptivePolicy?: string;
  adaptiveLevel?: number;
  adaptiveLevels?: number;
  weakViewers?: number;
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
  valid?: boolean;
  invalidReason?: string;
  generation?: string;
  pairId?: string;
  packetsReceivedDelta?: number | null;
  packetsLostDelta?: number | null;
  framesDroppedDelta?: number | null;
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

export type RoomVoiceConnectionState =
  | 'idle'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'full'
  | 'unauthorized'
  | 'error';

export type RoomVoiceMicrophoneState = 'starting' | 'live' | 'muted' | 'unavailable';

export interface RoomVoiceState {
  connection: RoomVoiceConnectionState;
  microphone: RoomVoiceMicrophoneState;
  playbackMuted: boolean;
  participantCount: number;
  errorCode: string;
}
