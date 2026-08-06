import type { GameCastPresenceParticipant, GameCastRoomState, Member, ShareSourceKind } from '../types';

export const GAMECAST_VIEWER_SELF_PEER_ID = 'gamecast-viewer';
export const GAMECAST_HOST_SELF_PEER_ID = 'gamecast-host';

export type GameCastRoomEntry = 'local-created' | 'client-joined-viewer' | 'web-joined-viewer' | 'unknown';
export type GameCastSharePath = 'native' | 'web-compat';
export type GameCastRoomRole = 'viewer' | 'host' | null;
export type GameCastRoomOrigin = 'web' | 'client' | string | null | undefined;
export type GameCastJoinAction = 'open-browser' | 'native-viewer';
export type OwnGameCastRoomAction = 'create-client' | 'enter-native' | 'open-browser';
export type GameCastFailureKind =
  | 'host-switch'
  | 'native-share-start'
  | 'compat-share-start'
  | 'viewer-reconnect';

export interface GameCastOperationError {
  kind: GameCastFailureKind;
  detail: string;
}

export interface GameCastShareDecisionInput {
  roomId: string | null;
  selfPeerId: string | null;
  roomEntry: GameCastRoomEntry | null;
  roomOrigin?: GameCastRoomOrigin;
  mediaMode?: string;
  mediaProtocolVersion?: number;
  role: GameCastRoomRole;
  hostVideoCapable: boolean;
  sourceKind: ShareSourceKind;
}

export interface GameCastViewerReconnectInput {
  roomId: string | null;
  selfPeerId: string | null;
  role: GameCastRoomRole;
  roomActive: boolean;
  roomSharing: boolean;
  roomMediaMode?: string;
  roomMediaProtocolVersion?: number;
  roomSharePhase?: string;
  roomSFU?: boolean;
  activeSharePath: GameCastSharePath | null;
  statusState: string | null | undefined;
  receiverState: string;
  attempt: number;
  maxAttempts?: number;
}

export type GameCastViewerReconnectEvent =
  | 'connection-started'
  | 'attempt-started'
  | 'websocket-connected'
  | 'media-connected'
  | 'sharing-stopped';

export class GameCastViewerLifecycle {
  private generation = 0;
  private reconnectGeneration: number | null = null;

  beginConnection(): number {
    this.generation += 1;
    this.reconnectGeneration = null;
    return this.generation;
  }

  beginReconnect(): number | null {
    if (this.reconnectGeneration !== null) {
      return null;
    }
    this.generation += 1;
    this.reconnectGeneration = this.generation;
    return this.generation;
  }

  finishReconnect(generation: number | null): boolean {
    if (generation === null || generation !== this.generation || generation !== this.reconnectGeneration) {
      return false;
    }
    this.reconnectGeneration = null;
    return true;
  }

  invalidate(): number {
    this.generation += 1;
    this.reconnectGeneration = null;
    return this.generation;
  }

  current(): number {
    return this.generation;
  }

  isCurrent(generation: number | null | undefined): boolean {
    return typeof generation === 'number' && generation === this.generation;
  }
}

export function reduceGameCastViewerReconnectAttempt(
  current: number,
  event: GameCastViewerReconnectEvent,
): number {
  if (event === 'attempt-started') {
    return current + 1;
  }
  if (event === 'connection-started' || event === 'media-connected' || event === 'sharing-stopped') {
    return 0;
  }
  return current;
}

export interface GameCastShareRuntimeEventInput {
  activeSharePath: GameCastSharePath | null;
  currentRoomId: string | null | undefined;
  eventRoomId: string | null | undefined;
}

export function isGameCastSelfPeerId(selfPeerId: string | null | undefined): boolean {
  return selfPeerId === GAMECAST_HOST_SELF_PEER_ID || selfPeerId === GAMECAST_VIEWER_SELF_PEER_ID;
}

export function hasUnifiedGameCastMediaContract(
  room: Pick<GameCastRoomState, 'mediaMode' | 'mediaProtocolVersion'> | null | undefined,
): boolean {
  return room?.mediaMode === 'sfu' && room.mediaProtocolVersion === 1;
}

export function isGameCastRoomLiveForViewer(
  room: Pick<
    GameCastRoomState,
    'active' | 'sharing' | 'mediaMode' | 'mediaProtocolVersion' | 'sharePhase' | 'sfu'
  > | null | undefined,
): boolean {
  return Boolean(
    room?.active &&
      room.sharing &&
      room.sharePhase === 'live' &&
      room.sfu &&
      hasUnifiedGameCastMediaContract(room),
  );
}

export function selectGameCastSharePath(input: GameCastShareDecisionInput): GameCastSharePath | null {
  if (
    !input.roomId ||
    !isGameCastSelfPeerId(input.selfPeerId) ||
    !input.hostVideoCapable ||
    !hasUnifiedGameCastMediaContract(input)
  ) {
    return null;
  }
  return 'native';
}

export function canStartGameCastSharing(
  selfPeerId: string | null | undefined,
  nativeHostVideoCapable: boolean,
  room: Pick<GameCastRoomState, 'mediaMode' | 'mediaProtocolVersion'> | null | undefined,
): boolean {
  return isGameCastSelfPeerId(selfPeerId) && nativeHostVideoCapable && hasUnifiedGameCastMediaContract(room);
}

export function selectGameCastJoinAction(origin: GameCastRoomOrigin): GameCastJoinAction {
  void origin;
  return 'native-viewer';
}

export function selectOwnGameCastRoomAction(
  ownRoomId: string,
  room: Pick<GameCastRoomState, 'room' | 'active' | 'origin'> | null | undefined,
): OwnGameCastRoomAction {
  if (!room?.active || room.room.trim().toLowerCase() !== ownRoomId.trim().toLowerCase()) {
    return 'create-client';
  }
  return 'enter-native';
}

export function shouldListGameCastRoom(
  room: Pick<GameCastRoomState, 'room' | 'active' | 'origin'>,
  ownRoomId: string,
): boolean {
  if (!room.active) {
    return false;
  }
  const ownRoom = Boolean(ownRoomId.trim()) && room.room.trim().toLowerCase() === ownRoomId.trim().toLowerCase();
  return !ownRoom;
}

export function gameCastRoomOriginLabelKey(origin: GameCastRoomOrigin): string {
  if (origin === 'web') return 'gamecast.originWeb';
  if (origin === 'client') return 'gamecast.originClient';
  return 'gamecast.originCompat';
}

export function mergeGameCastPresenceMembers(
  participants: GameCastPresenceParticipant[],
  selfParticipantId: string | null | undefined,
  selfPeerId: string | null | undefined,
): Member[] {
  const seen = new Set<string>();
  const members: Member[] = [];
  for (const participant of participants) {
    const participantId = participant.participantId?.trim();
    if (!participantId || !participant.active || seen.has(participantId)) {
      continue;
    }
    seen.add(participantId);
    members.push({
      id:
        participantId === selfParticipantId && selfPeerId
          ? selfPeerId
          : `gamecast-participant-${participantId}`,
      name: participant.name?.trim() || participantId,
      online: true,
      muted: false,
      sharing: Boolean(participant.sharing),
      clientType: participant.clientType === 'client' ? 'client' : 'web',
      role: participant.role || 'member',
    });
  }
  return members;
}

export function gameCastControlErrorMessageKey(code: string | null | undefined): string {
  switch (code) {
    case 'share_locked':
      return 'gamecast.errorShareLocked';
    case 'share_lease_expired':
      return 'gamecast.errorShareLeaseExpired';
    case 'room_origin_conflict':
      return 'gamecast.errorRoomOriginConflict';
    case 'participant_not_found':
      return 'gamecast.errorParticipantMissing';
    default:
      return 'gamecast.errorControlUnknown';
  }
}

export async function promoteGameCastViewerToHost(
  roomId: string,
  connectHost: (roomId: string) => Promise<void>,
): Promise<void> {
  const normalizedRoomId = roomId.trim();
  if (!normalizedRoomId) {
    throw new Error('room id is required');
  }
  await connectHost(normalizedRoomId);
}

export function shouldReconnectGameCastViewer(input: GameCastViewerReconnectInput): boolean {
  const maxAttempts = input.maxAttempts ?? 3;
  const receiverUnavailable =
    input.receiverState === 'idle' ||
    input.receiverState === 'closed' ||
    input.receiverState === 'error';
  const signalingDisconnected =
    input.statusState === 'closed' ||
    input.statusState === 'error';

  return (
    input.role === 'viewer' &&
    input.selfPeerId === GAMECAST_VIEWER_SELF_PEER_ID &&
    Boolean(input.roomId?.trim()) &&
    input.roomActive &&
    input.roomSharing &&
    input.roomMediaMode === 'sfu' &&
    input.roomMediaProtocolVersion === 1 &&
    input.roomSharePhase === 'live' &&
    input.roomSFU === true &&
    input.activeSharePath === null &&
    input.attempt < maxAttempts &&
    input.receiverState !== 'connecting' &&
    input.receiverState !== 'connected' &&
    (receiverUnavailable || signalingDisconnected)
  );
}

export function shouldApplyGameCastShareRuntimeEvent(input: GameCastShareRuntimeEventInput): boolean {
  const currentRoomId = input.currentRoomId?.trim();
  const eventRoomId = input.eventRoomId?.trim();
  return input.activeSharePath === 'native' && Boolean(currentRoomId) && currentRoomId === eventRoomId;
}

export function gameCastFailureMessageKey(kind: GameCastFailureKind): string {
  switch (kind) {
    case 'host-switch':
      return 'gamecast.errorHostSwitch';
    case 'native-share-start':
      return 'gamecast.errorNativeStart';
    case 'compat-share-start':
      return 'gamecast.errorCompatStart';
    case 'viewer-reconnect':
      return 'gamecast.errorViewerReconnect';
  }
}
