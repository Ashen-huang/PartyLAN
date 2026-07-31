import type {
  RoomVoiceConnectionState,
  RoomVoiceMicrophoneState,
  RoomVoiceState,
} from '../types';

const CONNECTION_STATES = new Set<RoomVoiceConnectionState>([
  'idle',
  'connecting',
  'connected',
  'reconnecting',
  'full',
  'unauthorized',
  'error',
]);

const MICROPHONE_STATES = new Set<RoomVoiceMicrophoneState>([
  'starting',
  'live',
  'muted',
  'unavailable',
]);

export function normalizeRoomVoiceState(
  value: {
    connection?: string;
    microphone?: string;
    playbackMuted?: boolean;
    participantCount?: number;
    errorCode?: string;
  } | null | undefined,
): RoomVoiceState {
  const connection = CONNECTION_STATES.has(value?.connection as RoomVoiceConnectionState)
    ? (value?.connection as RoomVoiceConnectionState)
    : 'idle';
  const microphone = MICROPHONE_STATES.has(value?.microphone as RoomVoiceMicrophoneState)
    ? (value?.microphone as RoomVoiceMicrophoneState)
    : 'muted';
  const participantCount = Number.isFinite(value?.participantCount)
    ? Math.max(0, Math.min(20, Math.trunc(value?.participantCount || 0)))
    : 0;
  return {
    connection,
    microphone,
    playbackMuted: Boolean(value?.playbackMuted),
    participantCount,
    errorCode: String(value?.errorCode || ''),
  };
}

export function nextMicrophoneEnabled(state: RoomVoiceState): boolean {
  return state.microphone !== 'live';
}

export function roomVoiceStatusKey(state: RoomVoiceState): string {
  switch (state.connection) {
    case 'connected':
      return 'voice.statusConnected';
    case 'connecting':
      return 'voice.statusConnecting';
    case 'reconnecting':
      return 'voice.statusReconnecting';
    case 'full':
      return 'voice.statusFull';
    case 'unauthorized':
      return 'voice.statusUnauthorized';
    case 'error':
      return 'voice.statusError';
    default:
      return 'voice.statusIdle';
  }
}
