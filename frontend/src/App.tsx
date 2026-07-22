import { useEffect, useMemo, useReducer, useRef, useState } from 'react';
import './App.css';
import MemberList from './components/MemberList';
import AvailableNodes from './components/AvailableNodes';
import ScreenPreview from './components/ScreenPreview';
import StatusBar from './components/StatusBar';
import RoomControls from './components/RoomControls';
import RoomToolsDialog, { type RoomToolsTab } from './components/RoomToolsDialog';
import LobbyView from './components/LobbyView';
import SettingsPanel from './components/SettingsPanel';
import DiagnosticsPanel from './components/DiagnosticsPanel';
import type { ShareSettings } from './components/RoomControls';
import {
  createTranslator,
  DEFAULT_LANGUAGE,
  isLanguage,
  LANGUAGE_STORAGE_KEY,
  type Language,
} from './i18n';
import { WebRTCReceiver } from './media/webrtcReceiver';
import { WindowMinimise, Quit, EventsOn, BrowserOpenURL } from '../wailsjs/runtime/runtime';
import {
	ConnectGameCastHost,
  ConnectGameCastViewer,
  CreateGameCastRoom,
  CreateRoom,
  DisconnectGameCast,
  GetAudioCapability,
  GetGameCastHistory,
  GetGameCastParticipantID,
  GetGameCastPresence,
  GetGameCastProfile,
  GetGameCastRooms,
  GetGameCastRoomState,
  GetGameCastSession,
  GetShareSources,
  GetTailscaleAuthKey,
  GetUserPreferences,
  GetAvailableNodes,
  GetNetworkStatus,
  JoinRoom,
  LeaveGameCastRoom,
  LeaveRoom,
  LoginGameCast,
  LogoutGameCast,
  GetRuntimeStatus,
  GetVideoCapability,
  GetVoiceState,
  PostGameCastPlaybackStats,
  SetVoiceMuted,
  SetVoiceVolume,
  SetTailscaleAuthKey,
  SetUserPreferences,
  SetGameCastHostSharing,
  ProbeGameCastServer,
  RegisterGameCast,
  RemoveGameCastHistory,
  SaveGameCastProfile,
  SendGameCastSignal,
  StartSharing,
  StartNativeGameCastSharing,
  StartVoiceChat,
  StopSharing,
  StopVoiceChat,
  TrustGameCastServerCertificate,
} from '../wailsjs/go/main/App';
import type {
  AudioCapability,
  GameCastProbeResult,
  GameCastProfile,
  GameCastPresenceParticipant,
  GameCastRoomSession,
  GameCastRoomState,
  GameCastShareRuntimeEvent,
  GameCastSessionState,
  GameCastStatus,
  Member,
  NetworkStats,
  RoomState,
  AudioState,
  AudioShareSource,
  ShareSource,
  StreamFrame,
  StreamSettings,
  StreamStats,
  AvailableNode,
  VideoCapability,
  RuntimeStatus,
  MediaSignal,
  WebRTCPlaybackStats,
  VoiceChatState,
  VoiceStatus,
  UserPreferences,
} from './types';
import { GameCastReceiver } from './media/gamecastReceiver';
import { GameCastHost } from './media/gamecastHost';
import {
  GAMECAST_HOST_SELF_PEER_ID as GAMECAST_HOST_PEER_ID,
  GAMECAST_VIEWER_SELF_PEER_ID as GAMECAST_SELF_PEER_ID,
  canStartGameCastSharing,
  gameCastControlErrorMessageKey,
  gameCastFailureMessageKey,
  mergeGameCastPresenceMembers,
  nativeGameCastAudioRequest,
  promoteGameCastViewerToHost,
  selectOwnGameCastRoomAction,
  selectGameCastSharePath,
  selectGameCastJoinAction,
  shouldListGameCastRoom,
  shouldApplyGameCastShareRuntimeEvent,
  shouldReconnectGameCastViewer,
  type GameCastFailureKind,
  type GameCastOperationError,
  type GameCastRoomEntry,
  type GameCastSharePath,
} from './media/gamecastOrchestrator';

type Scene = 'lobby' | 'room-idle' | 'room-sharing-self' | 'room-sharing-watch';

const DIAGNOSTICS_OVERLAY_STORAGE_KEY = 'partylan.roomDiagnosticsOverlayVisible';
const GAMECAST_VIEWER_PEER_PREFIX = 'gamecast-viewer-';
const ROOM_INVALIDATED_ERROR_CODE = 3001;

interface GameCastHistoryEntry {
  id: string;
  time: number;
}

interface GameCastClientState {
  profile: GameCastProfile;
  serverDraft: string;
  roomDraft: string;
  authUserDraft: string;
  authPassDraft: string;
  session: GameCastSessionState | null;
  probe: GameCastProbeResult | null;
  roomState: GameCastRoomState | null;
  status: GameCastStatus | null;
  rooms: GameCastRoomState[];
  history: GameCastHistoryEntry[];
  receiverState: string;
  role: 'viewer' | 'host' | null;
  roomEntry: GameCastRoomEntry | null;
  activeSharePath: GameCastSharePath | null;
  operationError: GameCastOperationError | null;
  viewerReconnectAttempt: number;
  participantId: string;
}

interface GameCastPresenceViewer {
  id: string;
  name: string;
}

interface AppState {
  members: Member[];
  network: NetworkStats;
  room: RoomState;
  audio: AudioState;
  audioSources: AudioShareSource[];
  frame: StreamFrame | null;
  streamStats: StreamStats | null;
  nodes: AvailableNode[];
  capability: VideoCapability | null;
  audioCapability: AudioCapability | null;
  runtime: RuntimeStatus | null;
  streamSettings: StreamSettings | null;
}

type AppAction =
  | { type: 'TOGGLE_MIC' }
  | { type: 'TOGGLE_SPEAKER' }
  | { type: 'SET_VOLUME'; volume: number }
  | { type: 'AUDIO_SOURCES_UPDATED'; sources: AudioShareSource[] }
  | { type: 'AUDIO_CAPABILITY'; capability: AudioCapability }
  | {
      type: 'NETWORK_STATUS';
      nodeId: string;
      peerCount: number;
      connectionType: string;
      latency: number | null;
    }
  | { type: 'VIDEO_CAPABILITY'; capability: VideoCapability }
  | { type: 'RUNTIME_STATUS'; runtime: RuntimeStatus }
  | { type: 'ROOM_CREATED'; roomId: string; selfPeerId: string | null }
  | { type: 'ROOM_JOINED'; roomId: string; selfPeerId: string | null }
  | { type: 'ROOM_LEFT' }
  | { type: 'PEER_JOINED'; member: Member }
  | { type: 'PEER_LEFT'; id: string }
  | { type: 'GAMECAST_PRESENCE'; viewers: GameCastPresenceViewer[]; selfPeerId: string | null; selfName: string }
  | { type: 'GAMECAST_CONTROL_PRESENCE'; members: Member[] }
  | { type: 'PEER_VOICE_STATE'; id: string; state: number }
  | { type: 'STREAM_STARTED'; peerId: string; width: number; height: number; fps: number }
  | { type: 'STREAM_STOPPED'; peerId: string }
  | { type: 'WEBRTC_STATS'; stats: WebRTCPlaybackStats }
  | { type: 'FRAME_RECEIVED'; frame: StreamFrame }
  | { type: 'STREAM_STATS'; stats: StreamStats }
  | { type: 'NODES_UPDATED'; nodes: AvailableNode[] }
  | { type: 'SET_ERROR_STATE' };

const initialState: AppState = {
  members: [],
  network: {
    latency: null,
    bitrate: null,
    connectionState: 'disconnected',
    connectionType: '',
  },
  room: {
    roomId: null,
    inRoom: false,
    sharing: false,
    selfPeerId: null,
    selfSharing: false,
    hostVideoCapable: false,
    canShareToPeers: false,
  },
  audio: {
    micEnabled: true,
    volume: 80,
    speakerEnabled: true,
  },
  audioSources: [],
  frame: null,
  streamStats: null,
  nodes: [],
  capability: null,
  audioCapability: null,
  runtime: null,
  streamSettings: null,
};

function hasRemoteMembers(members: Member[], selfPeerId: string | null): boolean {
  return members.some((member) => member.id !== selfPeerId);
}

function upsertMember(members: Member[], member: Member): Member[] {
 const existing = members.findIndex((entry) => entry.id === member.id);
 if (existing < 0) {
    return [...members, member];
  }

  const next = [...members];
  next[existing] = member;
  return next;
}

function rebuildGameCastPresenceMembers(
  state: AppState,
  viewers: GameCastPresenceViewer[],
  selfName: string,
): Member[] {
  const next: Member[] = [];
  const selfPeerId = state.room.selfPeerId;
  const push = (member: Member | null | undefined) => {
    if (!member) {
      return;
    }
    const updated = upsertMember(next, member);
    next.length = 0;
    next.push(...updated);
  };

  if (selfPeerId) {
    const existingSelf = state.members.find((member) => member.id === selfPeerId);
    push(existingSelf || fallbackSelfMember(selfPeerId, state.room));
  }

  if (selfPeerId === GAMECAST_SELF_PEER_ID) {
    const existingHost = state.members.find((member) => member.id === GAMECAST_HOST_PEER_ID);
    push(
      existingHost || {
        id: GAMECAST_HOST_PEER_ID,
        name: 'Host',
        online: true,
        muted: false,
        sharing: state.room.sharing,
      },
    );
  }

  for (const viewer of viewers) {
    const isSelfViewer =
      selfPeerId === GAMECAST_SELF_PEER_ID &&
      selfName &&
      sameGameCastRoom(viewer.name, selfName);
    if (isSelfViewer) {
      continue;
    }
    push({
      id: `${GAMECAST_VIEWER_PEER_PREFIX}${viewer.id}`,
      name: viewer.name || viewer.id,
      online: true,
      muted: false,
      sharing: false,
    });
  }

  return next;
}

function reducer(state: AppState, action: AppAction): AppState {
  switch (action.type) {
    case 'TOGGLE_MIC':
      return { ...state, audio: { ...state.audio, micEnabled: !state.audio.micEnabled } };
    case 'TOGGLE_SPEAKER':
      return {
        ...state,
        audio: {
          ...state.audio,
          speakerEnabled: !state.audio.speakerEnabled,
        },
      };
    case 'SET_VOLUME':
      return { ...state, audio: { ...state.audio, volume: action.volume } };
    case 'AUDIO_SOURCES_UPDATED':
      return {
        ...state,
        audioSources: action.sources,
      };
    case 'AUDIO_CAPABILITY':
      return {
        ...state,
        audioCapability: action.capability,
      };
    case 'NETWORK_STATUS':
      return {
        ...state,
        network: {
          ...state.network,
          latency: action.latency,
          connectionState: action.nodeId ? 'connected' : 'disconnected',
          connectionType: action.connectionType || state.network.connectionType,
        },
      };
    case 'VIDEO_CAPABILITY':
      return {
        ...state,
        capability: action.capability,
        room: {
          ...state.room,
          hostVideoCapable: action.capability.supportsHost,
          canShareToPeers: hasRemoteMembers(state.members, state.room.selfPeerId),
        },
      };
    case 'RUNTIME_STATUS':
      return {
        ...state,
        runtime: action.runtime,
      };
    case 'ROOM_CREATED':
    case 'ROOM_JOINED':
      const isGameCastRoom =
        action.selfPeerId === GAMECAST_HOST_PEER_ID || action.selfPeerId === GAMECAST_SELF_PEER_ID;
      return {
        ...state,
        members: isGameCastRoom ? [] : state.members,
        room: {
          ...state.room,
          roomId: action.roomId,
          inRoom: true,
          selfPeerId: action.selfPeerId,
          canShareToPeers: isGameCastRoom ? false : hasRemoteMembers(state.members, action.selfPeerId),
        },
        network: {
          ...state.network,
          connectionState: state.network.connectionState === 'disconnected' ? 'connected' : state.network.connectionState,
          connectionType: state.network.connectionType || 'networked',
        },
      };
    case 'ROOM_LEFT':
      return {
        ...state,
        room: {
          roomId: null,
          inRoom: false,
          sharing: false,
          selfPeerId: null,
          selfSharing: false,
          hostVideoCapable: state.room.hostVideoCapable,
          canShareToPeers: false,
        },
        streamSettings: null,
        streamStats: null,
        members: [],
        frame: null,
        network: {
          ...state.network,
          latency: null,
          bitrate: null,
        },
      };
    case 'PEER_JOINED': {
      const members = upsertMember(state.members, action.member);
      return {
        ...state,
        members,
        room: {
          ...state.room,
          canShareToPeers: hasRemoteMembers(members, state.room.selfPeerId),
        },
      };
    }
    case 'PEER_LEFT': {
      const members = state.members.filter((m) => m.id !== action.id);
      return {
        ...state,
        members,
        room: {
          ...state.room,
          canShareToPeers: hasRemoteMembers(members, state.room.selfPeerId),
        },
      };
    }
    case 'GAMECAST_PRESENCE': {
      const members = rebuildGameCastPresenceMembers(state, action.viewers, action.selfName);
      return {
        ...state,
        members,
        room: {
          ...state.room,
          canShareToPeers: hasRemoteMembers(members, state.room.selfPeerId),
        },
      };
    }
    case 'GAMECAST_CONTROL_PRESENCE':
      return {
        ...state,
        members: action.members,
        room: {
          ...state.room,
          canShareToPeers: hasRemoteMembers(action.members, state.room.selfPeerId),
        },
      };
    case 'PEER_VOICE_STATE':
      return {
        ...state,
        members: state.members.map((member) =>
          member.id === action.id ? { ...member, muted: action.state === 1 } : member,
        ),
      };
    case 'STREAM_STARTED':
      return {
        ...state,
        streamSettings: {
          peerId: action.peerId,
          width: action.width,
          height: action.height,
          fps: action.fps,
        },
        room: {
          ...state.room,
          sharing: true,
          selfSharing: state.room.selfPeerId === action.peerId ? true : state.room.selfSharing,
        },
        members: state.members.map((member) =>
          member.id === action.peerId ? { ...member, sharing: true } : member,
        ),
      };
    case 'STREAM_STOPPED':
      return {
        ...state,
        streamSettings: state.streamSettings?.peerId === action.peerId ? null : state.streamSettings,
        room: {
          ...state.room,
          sharing: false,
          selfSharing: state.room.selfPeerId === action.peerId ? false : state.room.selfSharing,
        },
        frame: null,
        streamStats: state.streamStats?.peerId === action.peerId ? null : state.streamStats,
        members: state.members.map((member) =>
          member.id === action.peerId ? { ...member, sharing: false } : member,
        ),
      };
    case 'WEBRTC_STATS':
      return {
        ...state,
        network: {
          ...state.network,
          bitrate:
            typeof action.stats.incomingBitrateKbps === 'number'
              ? Math.max(0, action.stats.incomingBitrateKbps / 1000)
              : state.network.bitrate,
        },
      };
    case 'FRAME_RECEIVED':
      return {
        ...state,
        frame: action.frame,
        network: {
          ...state.network,
          bitrate:
            typeof action.frame.incomingBitrateKbps === 'number'
              ? Math.max(0, action.frame.incomingBitrateKbps / 1000)
              : state.network.bitrate,
          },
      };
    case 'STREAM_STATS':
      return {
        ...state,
        streamStats: action.stats,
      };
    case 'NODES_UPDATED':
      return {
        ...state,
        nodes: action.nodes,
      };
    case 'SET_ERROR_STATE':
      return {
        ...state,
        streamSettings: null,
        room: { ...state.room, sharing: false, selfSharing: false },
      };
    default:
      return state;
  }
}

function toMember(payload: { peerId?: string; hostname?: string }): Member | null {
  if (!payload.peerId) {
    return null;
  }
  return {
    id: payload.peerId,
    name: payload.hostname || payload.peerId,
    online: true,
    muted: false,
    sharing: false,
  };
}

function toNode(source: {
  ID?: string;
  Hostname?: string;
  Online?: boolean;
  IP?: number[];
  Joinable?: boolean;
  RoomID?: string;
}): AvailableNode | null {
  if (!source.ID) {
    return null;
  }

  const ip = Array.isArray(source.IP) ? source.IP.join('.') : '';
  return {
    id: source.ID,
    hostname: source.Hostname || source.ID,
    online: Boolean(source.Online),
    ip,
    joinable: Boolean(source.Joinable),
    roomId: source.RoomID,
  };
}

function deriveScene(room: RoomState): Scene {
  if (!room.inRoom) {
    return 'lobby';
  }
  if (room.selfSharing) {
    return 'room-sharing-self';
  }
  if (room.sharing) {
    return 'room-sharing-watch';
  }
  return 'room-idle';
}

function fallbackSelfMember(selfPeerId: string | null, room: RoomState): Member {
  return {
    id: selfPeerId || 'self',
    name: 'You',
    online: true,
    muted: false,
    sharing: room.selfSharing,
  };
}

function loadDiagnosticsOverlayVisible(): boolean {
  try {
    return window.localStorage.getItem(DIAGNOSTICS_OVERLAY_STORAGE_KEY) !== 'false';
  } catch {
    return true;
  }
}

function loadLanguage(): Language {
  try {
    const saved = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
    return isLanguage(saved) ? saved : DEFAULT_LANGUAGE;
  } catch {
    return DEFAULT_LANGUAGE;
  }
}

function voiceStatusFromSnapshot(snapshot: Partial<VoiceChatState>): VoiceStatus {
  if (!snapshot.active) {
    return 'idle';
  }
  if (snapshot.muted || snapshot.state === 1) {
    return 'muted';
  }
  return 'active';
}

function toVoiceChatState(
  snapshot: Partial<VoiceChatState>,
  status?: VoiceStatus,
  error: string | null = null,
): VoiceChatState {
  return {
    active: Boolean(snapshot.active),
    muted: Boolean(snapshot.muted),
    volume: typeof snapshot.volume === 'number' ? snapshot.volume : 80,
    state: typeof snapshot.state === 'number' ? snapshot.state : 0,
    peerId: snapshot.peerId || '',
    experimental: snapshot.experimental !== false,
    message: snapshot.message || 'Experimental voice controls are connected; real device input and output are not complete yet.',
    status: status || voiceStatusFromSnapshot(snapshot),
    error,
  };
}

function toNetworkStatus(payload: {
  nodeId?: string;
  peerCount?: number;
  connectionType?: string;
  latency?: number;
}): { nodeId: string; peerCount: number; connectionType: string; latency: number | null } {
  return {
    nodeId: payload.nodeId ?? '',
    peerCount: payload.peerCount ?? 0,
    connectionType: payload.connectionType ?? '',
    latency: typeof payload.latency === 'number' ? payload.latency : null,
  };
}

function toVideoCapability(payload: {
  backend?: string;
  status?: string;
  adapter?: string;
  driver?: string;
  reason?: string;
  supportsHost?: boolean;
  supportsView?: boolean;
}): VideoCapability {
  return {
    backend: payload.backend ?? '',
    status: payload.status ?? '',
    adapter: payload.adapter ?? '',
    driver: payload.driver ?? '',
    reason: payload.reason ?? '',
    supportsHost: Boolean(payload.supportsHost),
    supportsView: Boolean(payload.supportsView),
  };
}

function toRuntimeStatus(payload: {
  mode?: string;
  networkBackend?: string;
  networkState?: string;
  networkAuthUrl?: string;
  networkHealth?: string[];
  mediaHealth?: string[];
  videoBackend?: string;
  videoStatus?: string;
  version?: string;
  audioBackend?: string;
  audioStatus?: string;
}): RuntimeStatus {
  return {
    mode: payload.mode ?? '',
    networkBackend: payload.networkBackend ?? '',
    networkState: payload.networkState ?? '',
    networkAuthUrl: payload.networkAuthUrl ?? '',
    networkHealth: Array.isArray(payload.networkHealth) ? payload.networkHealth : [],
    mediaHealth: Array.isArray(payload.mediaHealth) ? payload.mediaHealth : [],
    videoBackend: payload.videoBackend ?? '',
    videoStatus: payload.videoStatus ?? '',
    version: payload.version ?? '',
    audioBackend: payload.audioBackend ?? '',
    audioStatus: payload.audioStatus ?? '',
  };
}

function toAudioCapability(payload: { supportsProcessCapture?: boolean; reason?: string }): AudioCapability {
  return {
    supportsProcessCapture: Boolean(payload.supportsProcessCapture),
    reason: payload.reason ?? '',
  };
}

function toShareSources(raw: unknown): ShareSource[] {
  return Array.isArray(raw)
    ? raw
        .map((entry) => {
          const value = entry as Partial<ShareSource>;
          return {
            id: String(value.id || ''),
            kind: (value.kind || 'display') as ShareSource['kind'],
            label: String(value.label || ''),
            subtitle: value.subtitle ? String(value.subtitle) : '',
            displayIndex: typeof value.displayIndex === 'number' ? value.displayIndex : undefined,
            windowHandle: value.windowHandle ? String(value.windowHandle) : '',
            processId: typeof value.processId === 'number' ? value.processId : undefined,
            processName: value.processName ? String(value.processName) : '',
            iconDataUrl: value.iconDataUrl ? String(value.iconDataUrl) : '',
            previewDataUrl: value.previewDataUrl ? String(value.previewDataUrl) : '',
            audioDefault: (value.audioDefault || 'none') as ShareSource['audioDefault'],
            audioProcessName: value.audioProcessName ? String(value.audioProcessName) : '',
          } as ShareSource;
        })
        .filter((entry) => entry.id && entry.label)
    : [];
}

function toGameCastProfile(payload: Partial<GameCastProfile> | null | undefined): GameCastProfile {
  return {
    baseUrl: String(payload?.baseUrl || initialGameCastProfile.baseUrl),
    displayName: String(payload?.displayName || ''),
    trustedCertificateSha256: String(payload?.trustedCertificateSha256 || ''),
  };
}

function toGameCastRoomState(payload: Partial<GameCastRoomState> | null | undefined, roomId: string): GameCastRoomState {
  return {
    room: String(payload?.room || roomId),
    active: Boolean(payload?.active),
    origin: payload?.origin ? String(payload.origin) : '',
    ownerParticipantId: payload?.ownerParticipantId ? String(payload.ownerParticipantId) : '',
    ownerName: payload?.ownerName ? String(payload.ownerName) : '',
    sharing: Boolean(payload?.sharing),
    shareOwnerParticipantId: payload?.shareOwnerParticipantId ? String(payload.shareOwnerParticipantId) : '',
    shareOwnerName: payload?.shareOwnerName ? String(payload.shareOwnerName) : '',
    shareOrigin: payload?.shareOrigin ? String(payload.shareOrigin) : '',
    sfu: Boolean(payload?.sfu),
    audioMode: payload?.audioMode ? String(payload.audioMode) : '',
    user: payload?.user ? String(payload.user) : '',
    ttl: typeof payload?.ttl === 'number' ? payload.ttl : undefined,
    updatedAt: payload?.updatedAt ? String(payload.updatedAt) : '',
    participantCount: typeof payload?.participantCount === 'number' ? payload.participantCount : undefined,
    updatedAtUnix: typeof payload?.updatedAtUnix === 'number' ? payload.updatedAtUnix : undefined,
  };
}

function optimisticGameCastRoomState(roomId: string): GameCastRoomState {
  return {
    room: roomId,
    active: true,
    sharing: true,
    sfu: true,
    audioMode: '',
    user: '',
  };
}

function toGameCastSession(payload: Partial<GameCastSessionState> | null | undefined): GameCastSessionState {
  return {
    user: String(payload?.user || 'guest'),
    loggedIn: Boolean(payload?.loggedIn),
    authMode: payload?.authMode ? String(payload.authMode) : '',
    version: payload?.version ? String(payload.version) : '',
    roomName: payload?.roomName ? String(payload.roomName) : '',
  };
}

function toGameCastRoomSession(payload: Partial<GameCastRoomSession> | null | undefined): GameCastRoomSession {
  const roomId = String(payload?.roomId || '');
  return {
    roomId,
    role: payload?.role || 'host',
    state: toGameCastRoomState(payload?.state, roomId),
  };
}

function toGameCastRooms(raw: unknown): GameCastRoomState[] {
  return Array.isArray(raw)
    ? raw
        .map((entry) => {
          const roomId = String((entry as Partial<GameCastRoomState>)?.room || '');
          return toGameCastRoomState(entry as Partial<GameCastRoomState>, roomId);
        })
        .filter((entry) => entry.room && entry.active)
    : [];
}

function toGameCastPresenceViewers(raw: unknown): GameCastPresenceViewer[] | null {
  const payload = raw as { type?: unknown; viewers?: unknown };
  if (!payload || payload.type !== 'presence') {
    return null;
  }
  return Array.isArray(payload.viewers)
    ? payload.viewers
        .map((entry) => ({
          id: String((entry as Partial<GameCastPresenceViewer>)?.id || ''),
          name: String((entry as Partial<GameCastPresenceViewer>)?.name || ''),
        }))
        .filter((entry) => entry.id)
    : [];
}

function toGameCastPresenceParticipants(raw: unknown): GameCastPresenceParticipant[] {
  return Array.isArray(raw)
    ? raw
        .map((entry) => {
          const value = entry as Partial<GameCastPresenceParticipant>;
          return {
            participantId: String(value.participantId || ''),
            room: String(value.room || ''),
            name: value.name ? String(value.name) : '',
            clientType: value.clientType ? String(value.clientType) : '',
            role: value.role ? String(value.role) : 'member',
            active: Boolean(value.active),
            sharing: Boolean(value.sharing),
            ttl: typeof value.ttl === 'number' ? value.ttl : undefined,
            ageSeconds: typeof value.ageSeconds === 'number' ? value.ageSeconds : undefined,
          };
        })
        .filter((entry) => entry.participantId)
    : [];
}

function toGameCastHistory(raw: unknown): GameCastHistoryEntry[] {
  return Array.isArray(raw)
    ? raw
        .map((entry) => ({
          id: String(
            (entry as Partial<GameCastHistoryEntry> & { ID?: string }).id ||
              (entry as Partial<GameCastHistoryEntry> & { ID?: string }).ID ||
              '',
          ),
          time: Number(
            (entry as Partial<GameCastHistoryEntry> & { Time?: number }).time ||
              (entry as Partial<GameCastHistoryEntry> & { Time?: number }).Time ||
              0,
          ),
        }))
        .filter((entry) => entry.id)
    : [];
}

function preferredViewerLayer(): string {
  return 'auto';
}

function sameGameCastRoom(left: string, right: string): boolean {
  return left.trim().toLowerCase() === right.trim().toLowerCase();
}

function isLegacyRuntimeMode(mode: string | null | undefined): boolean {
  return mode === 'loopback' || mode === 'lan' || mode === 'networked' || mode === 'tailscale-system';
}

const initialVoiceState: VoiceChatState = toVoiceChatState({});

const initialGameCastProfile: GameCastProfile = {
  baseUrl: 'https://113.142.217.42:5050',
  displayName: '',
  trustedCertificateSha256: '',
};

const initialGameCastState: GameCastClientState = {
  profile: initialGameCastProfile,
  serverDraft: initialGameCastProfile.baseUrl,
  roomDraft: '',
  authUserDraft: '',
  authPassDraft: '',
  session: null,
  probe: null,
  roomState: null,
  status: null,
  rooms: [],
  history: [],
  receiverState: 'idle',
  role: null,
  roomEntry: null,
  activeSharePath: null,
  operationError: null,
  viewerReconnectAttempt: 0,
  participantId: '',
};

export default function App() {
  const [state, dispatch] = useReducer(reducer, initialState);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [roomToolsOpen, setRoomToolsOpen] = useState(false);
  const [roomToolsInitialTab, setRoomToolsInitialTab] = useState<RoomToolsTab>('share');
  const [diagnosticsOverlayVisible, setDiagnosticsOverlayVisibleState] = useState(loadDiagnosticsOverlayVisible);
  const [language, setLanguageState] = useState<Language>(loadLanguage);
  const [voiceState, setVoiceState] = useState<VoiceChatState>(initialVoiceState);
  const [gameCast, setGameCast] = useState<GameCastClientState>(initialGameCastState);
  const [authKeyDraft, setAuthKeyDraft] = useState('');
  const [displayNameDraft, setDisplayNameDraft] = useState('');
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [remoteMediaStream, setRemoteMediaStream] = useState<MediaStream | null>(null);
  const [webrtcStats, setWebrtcStats] = useState<WebRTCPlaybackStats | null>(null);
  const [shareSources, setShareSources] = useState<ShareSource[]>([]);
  const selfPeerIdRef = useRef('');
  const mediaReceiverRef = useRef<WebRTCReceiver | null>(null);
  const gameCastReceiverRef = useRef<GameCastReceiver | null>(null);
  const gameCastHostRef = useRef<GameCastHost | null>(null);
  const gameCastProfileRef = useRef<GameCastProfile>(initialGameCastProfile);
  const gameCastSessionRef = useRef<GameCastSessionState | null>(null);
  const gameCastRoomRef = useRef('');
  const gameCastRoleRef = useRef<GameCastClientState['role']>(null);
  const gameCastActiveSharePathRef = useRef<GameCastSharePath | null>(null);
  const gameCastRoomOriginRef = useRef('');
  const gameCastParticipantIdRef = useRef('');
  const lastGameCastStatsPostRef = useRef(0);
  const gameCastViewerReconnectTimerRef = useRef<number | null>(null);
  const gameCastAutoViewerRoomRef = useRef('');
  const t = useMemo(() => createTranslator(language), [language]);

  function formatGameCastFailure(kind: GameCastFailureKind, detail: string): string {
    return t(gameCastFailureMessageKey(kind), {
      detail: detail.trim() || t('gamecast.errorUnknownDetail'),
    });
  }

  function formatGameCastControlError(detail: string): string | null {
    const code = ['share_locked', 'share_lease_expired', 'room_origin_conflict', 'participant_not_found'].find(
      (candidate) => detail.includes(candidate),
    );
    return code ? t(gameCastControlErrorMessageKey(code)) : null;
  }

  function recordWebRTCStats(stats: WebRTCPlaybackStats) {
    setWebrtcStats(stats);
    dispatch({ type: 'WEBRTC_STATS', stats });

    const roomId = gameCastRoomRef.current.trim();
    const role = gameCastRoleRef.current;
    if (!roomId || !role) {
      return;
    }
    const now = Date.now();
    if (now - lastGameCastStatsPostRef.current < 2000) {
      return;
    }
    lastGameCastStatsPostRef.current = now;
    void PostGameCastPlaybackStats({
      room: roomId,
      participantId: gameCastParticipantIdRef.current,
      role,
      valid: Boolean(stats.valid),
      invalidReason: stats.invalidReason || '',
      generation: stats.generation || '',
      pairId: stats.pairId || '',
      decodedFps: stats.decodedFps,
      incomingBitrateKbps: stats.incomingBitrateKbps,
      packetsLost: stats.packetsLost,
      jitterMs: stats.jitterMs,
      framesDropped: stats.framesDropped,
      packetsReceivedDelta: stats.packetsReceivedDelta,
      packetsLostDelta: stats.packetsLostDelta,
      framesDroppedDelta: stats.framesDroppedDelta,
      receiverState: gameCastReceiverRef.current ? 'active' : 'closed',
    }).catch(() => undefined);
  }

  useEffect(() => {
    selfPeerIdRef.current = state.room.selfPeerId || '';
  }, [state.room.selfPeerId]);

  useEffect(() => {
    gameCastProfileRef.current = gameCast.profile;
  }, [gameCast.profile]);

  useEffect(() => {
    gameCastSessionRef.current = gameCast.session;
  }, [gameCast.session]);

  useEffect(() => {
    gameCastRoomRef.current = gameCast.roomDraft || state.room.roomId || '';
    lastGameCastStatsPostRef.current = 0;
  }, [gameCast.roomDraft, state.room.roomId]);

  useEffect(() => {
    gameCastRoleRef.current = gameCast.role;
  }, [gameCast.role]);

  useEffect(() => {
    gameCastActiveSharePathRef.current = gameCast.activeSharePath;
  }, [gameCast.activeSharePath]);

  useEffect(() => {
    gameCastRoomOriginRef.current = gameCast.roomState?.origin || '';
  }, [gameCast.roomState?.origin]);

  useEffect(() => {
    gameCastParticipantIdRef.current = gameCast.participantId;
  }, [gameCast.participantId]);

  useEffect(() => {
    if (gameCastViewerReconnectTimerRef.current !== null) {
      window.clearTimeout(gameCastViewerReconnectTimerRef.current);
      gameCastViewerReconnectTimerRef.current = null;
    }

    const shouldReconnect = shouldReconnectGameCastViewer({
      roomId: gameCast.roomDraft,
      selfPeerId: state.room.selfPeerId,
      role: gameCast.role,
      roomActive: Boolean(gameCast.roomState?.active),
      roomSharing: Boolean(gameCast.roomState?.sharing),
      activeSharePath: gameCast.activeSharePath,
      statusState: gameCast.status?.state,
      receiverState: gameCast.receiverState,
      attempt: gameCast.viewerReconnectAttempt,
    });

    if (!shouldReconnect) {
      return;
    }

    const roomId = gameCast.roomDraft.trim();
    const attempt = gameCast.viewerReconnectAttempt + 1;
    gameCastViewerReconnectTimerRef.current = window.setTimeout(() => {
      setGameCast((current) => ({
        ...current,
        receiverState: 'connecting',
        status: { state: 'connecting', detail: `reconnecting viewer ${roomId} (${attempt}/3)` },
        operationError: null,
        viewerReconnectAttempt: attempt,
      }));
      void ConnectGameCastViewer(roomId, preferredViewerLayer())
        .then(() => {
          setGameCast((current) => ({
            ...current,
            receiverState: 'connecting',
            status: { state: 'connecting', detail: `reconnecting viewer ${roomId}` },
            operationError: null,
            viewerReconnectAttempt: 0,
          }));
          setError(null);
        })
        .catch((err) => {
          const detail = err instanceof Error ? err.message : String(err);
          const message = t(gameCastFailureMessageKey('viewer-reconnect'), {
            detail: detail.trim() || t('gamecast.errorUnknownDetail'),
          });
          setGameCast((current) => ({
            ...current,
            receiverState: 'error',
            status: { state: 'error', detail: message },
            operationError: { kind: 'viewer-reconnect', detail },
            viewerReconnectAttempt: attempt,
          }));
          setError(message);
        });
    }, Math.min(800 * attempt, 2400));

    return () => {
      if (gameCastViewerReconnectTimerRef.current !== null) {
        window.clearTimeout(gameCastViewerReconnectTimerRef.current);
        gameCastViewerReconnectTimerRef.current = null;
      }
    };
  }, [
    gameCast.role,
    gameCast.roomDraft,
    gameCast.roomState?.active,
    gameCast.roomState?.sharing,
    gameCast.status?.state,
    gameCast.receiverState,
    gameCast.activeSharePath,
    gameCast.viewerReconnectAttempt,
    state.room.selfPeerId,
    t,
  ]);

  useEffect(() => {
    const unsubscribers = [
      EventsOn('room:created', (payload) => {
        dispatch({ type: 'ROOM_CREATED', roomId: payload.roomId, selfPeerId: payload.selfPeerId ?? null });
      }),
      EventsOn('room:joined', (payload) => {
        dispatch({ type: 'ROOM_JOINED', roomId: payload.roomId, selfPeerId: payload.selfPeerId ?? null });
      }),
      EventsOn('room:left', () => {
        dispatch({ type: 'ROOM_LEFT' });
        setRoomToolsOpen(false);
        setVoiceState(initialVoiceState);
        setRemoteMediaStream(null);
        setWebrtcStats(null);
        setGameCast((current) => ({
          ...current,
          roomState: null,
          role: null,
          roomEntry: null,
          activeSharePath: null,
          receiverState: 'closed',
          operationError: null,
          viewerReconnectAttempt: 0,
        }));
        mediaReceiverRef.current?.close('room left');
        mediaReceiverRef.current = null;
        gameCastReceiverRef.current?.close('room left');
        gameCastReceiverRef.current = null;
        gameCastHostRef.current?.stop('room left');
        gameCastHostRef.current = null;
      }),
      EventsOn('network:status', (payload) => {
        const status = toNetworkStatus(payload);
        dispatch({
          type: 'NETWORK_STATUS',
          nodeId: status.nodeId,
          peerCount: status.peerCount,
          connectionType: status.connectionType,
          latency: status.latency,
        });
      }),
      EventsOn('video:capability', (payload) => {
        dispatch({
          type: 'VIDEO_CAPABILITY',
          capability: toVideoCapability(payload),
        });
      }),
      EventsOn('runtime:status', (payload) => {
        dispatch({
          type: 'RUNTIME_STATUS',
          runtime: toRuntimeStatus(payload),
        });
      }),
      EventsOn('audio:capability', (payload) => {
        dispatch({
          type: 'AUDIO_CAPABILITY',
          capability: toAudioCapability(payload),
        });
      }),
      EventsOn('peer:joined', (payload) => {
        const member = toMember(payload);
        if (member) {
          dispatch({ type: 'PEER_JOINED', member });
        }
      }),
      EventsOn('peer:left', (payload) => {
        if (payload.peerId) {
          dispatch({ type: 'PEER_LEFT', id: payload.peerId });
        }
      }),
      EventsOn('peer:voice-state', (payload) => {
        const peerId = payload.peerId ?? '';
        const nextState = typeof payload.state === 'number' ? payload.state : 0;
        if (peerId) {
          dispatch({ type: 'PEER_VOICE_STATE', id: peerId, state: nextState });
        }
        setVoiceState((current) => {
          if (!current.active) {
            return current;
          }
          const muted = nextState === 1;
          return {
            ...current,
            muted,
            state: nextState,
            status: muted ? 'muted' : 'active',
          };
        });
      }),
      EventsOn('stream:started', (payload) => {
        if (payload.peerId) {
          dispatch({
            type: 'STREAM_STARTED',
            peerId: payload.peerId,
            width: Number(payload.width) || 0,
            height: Number(payload.height) || 0,
            fps: Number(payload.fps) || 0,
          });
        }
      }),
      EventsOn('stream:stopped', (payload) => {
        if (payload.peerId) {
          dispatch({ type: 'STREAM_STOPPED', peerId: payload.peerId });
        }
        setRemoteMediaStream(null);
        setWebrtcStats(null);
        mediaReceiverRef.current?.close('stream stopped');
        mediaReceiverRef.current = null;
      }),
      EventsOn('stream:frame', (payload) => {
        dispatch({ type: 'FRAME_RECEIVED', frame: payload as StreamFrame });
      }),
      EventsOn('stream:stats', (payload) => {
        dispatch({ type: 'STREAM_STATS', stats: payload as StreamStats });
      }),
      EventsOn('media:signal', (payload) => {
        const selfPeerId = selfPeerIdRef.current;
        if (!selfPeerId) {
          return;
        }
        if (!mediaReceiverRef.current) {
          mediaReceiverRef.current = new WebRTCReceiver({
            selfPeerId,
            onRemoteStream: (stream) => {
              setRemoteMediaStream(stream);
            },
            onStats: (stats) => {
              recordWebRTCStats(stats);
            },
            onStateChange: (mediaState, detail) => {
              if (mediaState === 'error') {
                setError(detail || 'WebRTC media connection failed');
              }
              if (mediaState === 'closed') {
                setRemoteMediaStream(null);
                setWebrtcStats(null);
              }
            },
          });
        }
        void mediaReceiverRef.current.handleSignal(payload as MediaSignal);
      }),
      EventsOn('gamecast:signal', (payload) => {
        const presenceViewers = toGameCastPresenceViewers(payload);
        if (presenceViewers) {
          if (gameCastRoomOriginRef.current === 'client') {
            return;
          }
          dispatch({
            type: 'GAMECAST_PRESENCE',
            viewers: presenceViewers,
            selfPeerId: selfPeerIdRef.current || null,
            selfName: gameCastSessionRef.current?.user || '',
          });
          return;
        }
        if (gameCastHostRef.current) {
          void gameCastHostRef.current.handleSignal(payload);
          return;
        }
        if (!gameCastReceiverRef.current) {
          gameCastReceiverRef.current = new GameCastReceiver({
            serverBaseUrl: gameCastProfileRef.current.baseUrl,
            onSignal: (signal) => SendGameCastSignal(signal),
            onRemoteStream: (stream) => {
              setRemoteMediaStream(stream);
              dispatch({
                type: 'STREAM_STARTED',
                peerId: GAMECAST_HOST_PEER_ID,
                width: 0,
                height: 0,
                fps: 0,
              });
            },
            onStats: (stats) => {
              recordWebRTCStats(stats);
            },
            onStateChange: (receiverState, detail) => {
              const failureDetail = detail || 'GameCast media connection failed';
              const failureMessage =
                receiverState === 'error' ? formatGameCastFailure('viewer-reconnect', failureDetail) : '';
              setGameCast((current) => ({
                ...current,
                receiverState,
                status:
                  receiverState === 'error'
                    ? { state: 'error', detail: failureMessage }
                    : detail
                      ? { state: receiverState, detail }
                      : current.status,
                operationError:
                  receiverState === 'connected'
                    ? null
                    : receiverState === 'error'
                      ? { kind: 'viewer-reconnect', detail: failureDetail }
                      : current.operationError,
                viewerReconnectAttempt: receiverState === 'connected' ? 0 : current.viewerReconnectAttempt,
              }));
              if (receiverState === 'error') {
                setError(failureMessage);
              }
              if (receiverState === 'connected') {
                setError(null);
              }
              if (receiverState === 'closed') {
                setRemoteMediaStream(null);
                setWebrtcStats(null);
              }
            },
          });
        }
        void gameCastReceiverRef.current.handleSignal(payload);
      }),
      EventsOn('gamecast:status', (payload) => {
        setGameCast((current) => ({
          ...current,
          status: payload as GameCastStatus,
        }));
      }),
      EventsOn('gamecast:share-runtime', (payload) => {
        const event = payload as GameCastShareRuntimeEvent;
        if (
          !shouldApplyGameCastShareRuntimeEvent({
            activeSharePath: gameCastActiveSharePathRef.current,
            currentRoomId: gameCastRoomRef.current,
            eventRoomId: event.roomId,
          })
        ) {
          return;
        }

        const message = event.message || 'Native GameCast sharing changed unexpectedly';
        setError(message);
        if (event.kind === 'audio-disabled') {
          setGameCast((current) => ({
            ...current,
            roomState: current.roomState
              ? {
                  ...current.roomState,
                  audioMode: 'off',
                }
              : current.roomState,
          }));
          return;
        }
        if (event.kind !== 'video-stopped') {
          return;
        }

        gameCastActiveSharePathRef.current = null;
        setRemoteMediaStream(null);
        setWebrtcStats(null);
        setGameCast((current) => ({
          ...current,
          roomState: current.roomState
            ? {
                ...current.roomState,
                sharing: false,
                sfu: false,
                audioMode: 'off',
              }
            : current.roomState,
          receiverState: 'host-ready',
          activeSharePath: null,
          operationError: null,
          status: { state: 'host-ready', detail: message },
        }));
        dispatch({ type: 'STREAM_STOPPED', peerId: event.peerId || GAMECAST_HOST_PEER_ID });
      }),
      EventsOn('error', (payload) => {
        setError(payload.message || 'Unexpected error');
        if (Number(payload.code) === ROOM_INVALIDATED_ERROR_CODE) {
          setGameCast((current) => ({
            ...current,
            activeSharePath: null,
          }));
          dispatch({ type: 'SET_ERROR_STATE' });
        }
      }),
    ];

    return () => {
      unsubscribers.forEach((unsubscribe) => unsubscribe());
    };
  }, [t]);

  useEffect(() => {
    let cancelled = false;

    async function loadRuntimeSnapshots() {
      try {
        const [networkStatus, videoCapability, runtimeStatus, audioCapability] = await Promise.all([
          GetNetworkStatus(),
          GetVideoCapability(),
          GetRuntimeStatus(),
          GetAudioCapability(),
        ]);
        if (cancelled) {
          return;
        }

        const network = toNetworkStatus(networkStatus);
        dispatch({
          type: 'NETWORK_STATUS',
          nodeId: network.nodeId,
          peerCount: network.peerCount,
          connectionType: network.connectionType,
          latency: network.latency,
        });
        dispatch({ type: 'VIDEO_CAPABILITY', capability: toVideoCapability(videoCapability) });
        dispatch({ type: 'RUNTIME_STATUS', runtime: toRuntimeStatus(runtimeStatus) });
        dispatch({ type: 'AUDIO_CAPABILITY', capability: toAudioCapability(audioCapability) });
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    }

    void loadRuntimeSnapshots();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!isLegacyRuntimeMode(state.runtime?.mode)) {
      dispatch({ type: 'NODES_UPDATED', nodes: [] });
      return;
    }

    let cancelled = false;

    async function loadNodes() {
      try {
        const rawSources = await GetAvailableNodes();
        if (cancelled) {
          return;
        }
        const sources = Array.isArray(rawSources) ? rawSources : [];
        const nodes = sources
          .map((source) => toNode(source))
          .filter((node): node is AvailableNode => node !== null);
        dispatch({ type: 'NODES_UPDATED', nodes });
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    }

    void loadNodes();
    const timer = window.setInterval(() => {
      void loadNodes();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [state.runtime?.mode]);

  useEffect(() => {
    let cancelled = false;

    async function loadAudioSources() {
      try {
        const { GetAudioShareCandidates } = await import('../wailsjs/go/main/App');
        const rawSources = await GetAudioShareCandidates();
        if (cancelled) {
          return;
        }
        const sources = Array.isArray(rawSources) ? rawSources : [];
        dispatch({
          type: 'AUDIO_SOURCES_UPDATED',
          sources: sources.map((source: {
            pid?: number;
            name?: string;
            displayName?: string;
            iconDataUrl?: string;
            active?: boolean;
            selected?: boolean;
          }) => ({
            pid: source.pid ?? 0,
            name: source.name ?? '',
            displayName: source.displayName ?? '',
            iconDataUrl: source.iconDataUrl ?? '',
            active: source.active !== false,
            selected: Boolean(source.selected),
          })),
        });
      } catch {
        if (!cancelled) {
          dispatch({ type: 'AUDIO_SOURCES_UPDATED', sources: [] });
        }
      }
    }

    void loadAudioSources();
    const timer = window.setInterval(() => {
      void loadAudioSources();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    if (!roomToolsOpen) {
      return;
    }

    let cancelled = false;

    async function loadShareSources() {
      try {
        const sources = await GetShareSources();
        if (!cancelled) {
          setShareSources(toShareSources(sources));
        }
      } catch {
        if (!cancelled) {
          setShareSources([]);
        }
      }
    }

    void loadShareSources();
    const timer = window.setInterval(() => {
      void loadShareSources();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [roomToolsOpen]);

  useEffect(() => {
    let cancelled = false;

    async function loadSettings() {
      try {
        const [preferences, gameCastProfile, gameCastSession, participantId] = await Promise.all([
          GetUserPreferences(),
          GetGameCastProfile(),
          GetGameCastSession().catch(() => null),
          GetGameCastParticipantID().catch(() => ''),
        ]);
        if (!cancelled) {
          setDisplayNameDraft((preferences as Partial<UserPreferences>)?.displayName || '');
          const profile = toGameCastProfile(gameCastProfile);
          setGameCast((current) => ({
            ...current,
            profile,
            serverDraft: profile.baseUrl,
            session: gameCastSession ? toGameCastSession(gameCastSession) : current.session,
            participantId: String(participantId || ''),
          }));
        }
      } catch {
        if (!cancelled) {
          setDisplayNameDraft('');
        }
      }
    }

    void loadSettings();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const legacyNetworkSettingsVisible =
      state.runtime?.mode === 'networked' || state.runtime?.mode === 'tailscale-system';

    if (!legacyNetworkSettingsVisible) {
      setAuthKeyDraft('');
      return;
    }

    let cancelled = false;

    async function loadAuthKey() {
      try {
        const authKey = await GetTailscaleAuthKey();
        if (!cancelled) {
          setAuthKeyDraft(authKey || '');
        }
      } catch {
        if (!cancelled) {
          setAuthKeyDraft('');
        }
      }
    }

    void loadAuthKey();

    return () => {
      cancelled = true;
    };
  }, [state.runtime?.mode]);

  useEffect(() => {
    let cancelled = false;

    async function loadGameCastHistory() {
      try {
        const history = await GetGameCastHistory();
        if (cancelled) {
          return;
        }
        setGameCast((current) => ({
          ...current,
          history: toGameCastHistory(history),
        }));
      } catch {
        if (!cancelled) {
          setGameCast((current) => ({ ...current, history: [] }));
        }
      }
    }

    void loadGameCastHistory();

    return () => {
      cancelled = true;
    };
  }, [gameCast.profile.baseUrl, gameCast.profile.trustedCertificateSha256]);

  useEffect(() => {
    let cancelled = false;

    async function loadVoiceState() {
      try {
        const snapshot = await GetVoiceState();
        if (!cancelled) {
          setVoiceState(toVoiceChatState(snapshot));
        }
      } catch {
        if (!cancelled) {
          setVoiceState(initialVoiceState);
        }
      }
    }

    void loadVoiceState();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const roomId = gameCast.roomDraft.trim();
    if (!roomId) {
      return;
    }
    let cancelled = false;

    async function pollRoomState() {
      try {
        const roomState = toGameCastRoomState(await GetGameCastRoomState(roomId), roomId);
        if (!cancelled) {
          setGameCast((current) => ({
            ...current,
            roomState,
            viewerReconnectAttempt: roomState.sharing ? current.viewerReconnectAttempt : 0,
            operationError:
              !roomState.sharing && current.operationError?.kind === 'viewer-reconnect'
                ? null
                : current.operationError,
          }));
        }
      } catch {
        // Keep the last known room-state visible while connectivity recovers.
      }
    }

    void pollRoomState();
    const timer = window.setInterval(() => {
      void pollRoomState();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [gameCast.roomDraft, gameCast.profile.baseUrl, gameCast.profile.trustedCertificateSha256]);

  useEffect(() => {
    const roomId = state.room.roomId?.trim() || '';
    if (!roomId || gameCast.roomState?.origin !== 'client') {
      return;
    }
    let cancelled = false;

    async function pollPresence() {
      try {
        const participants = toGameCastPresenceParticipants(await GetGameCastPresence(roomId));
        if (cancelled) {
          return;
        }
        dispatch({
          type: 'GAMECAST_CONTROL_PRESENCE',
          members: mergeGameCastPresenceMembers(participants, gameCast.participantId, selfPeerIdRef.current || null),
        });
      } catch {
        // Keep the last participant snapshot while the control endpoint recovers.
      }
    }

    void pollPresence();
    const timer = window.setInterval(() => {
      void pollPresence();
    }, 3000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [
    gameCast.participantId,
    gameCast.profile.baseUrl,
    gameCast.profile.trustedCertificateSha256,
    gameCast.roomState?.origin,
    state.room.roomId,
    state.room.selfPeerId,
  ]);

  useEffect(() => {
    if (
      gameCast.roomState?.origin !== 'client' ||
      gameCast.roomState.sharing ||
      !state.room.inRoom ||
      !state.room.sharing ||
      state.room.selfSharing
    ) {
      return;
    }
    setRemoteMediaStream(null);
    setWebrtcStats(null);
    dispatch({ type: 'STREAM_STOPPED', peerId: GAMECAST_HOST_PEER_ID });
  }, [
    gameCast.roomState?.origin,
    gameCast.roomState?.sharing,
    state.room.inRoom,
    state.room.selfSharing,
    state.room.sharing,
  ]);

  useEffect(() => {
    let cancelled = false;

    async function loadGameCastRooms() {
      try {
        const rooms = await GetGameCastRooms();
        if (!cancelled) {
          setGameCast((current) => ({
            ...current,
            rooms: toGameCastRooms(rooms),
          }));
        }
      } catch {
        if (!cancelled) {
          setGameCast((current) => ({
            ...current,
            rooms: [],
          }));
        }
      }
    }

    void loadGameCastRooms();
    const timer = window.setInterval(() => {
      void loadGameCastRooms();
    }, 5000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [gameCast.profile.baseUrl, gameCast.profile.trustedCertificateSha256]);

  async function withBusy(task: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await task();
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      setError(formatGameCastControlError(detail) || detail);
    } finally {
      setBusy(false);
    }
  }

  const handleCreateRoom = () =>
    withBusy(async () => {
      await CreateRoom('PartyLAN Room', '');
    });

  const handleJoinRoom = (targetNodeId: string) =>
    withBusy(async () => {
      await JoinRoom(targetNodeId, '');
    });

  const handleLeaveRoom = () =>
    withBusy(async () => {
      if (state.room.selfPeerId === GAMECAST_SELF_PEER_ID || state.room.selfPeerId === GAMECAST_HOST_PEER_ID) {
        const wasHost = state.room.selfPeerId === GAMECAST_HOST_PEER_ID;
        gameCastHostRef.current?.stop('room left');
        gameCastHostRef.current = null;
        if (state.room.selfPeerId === GAMECAST_HOST_PEER_ID) {
          await LeaveGameCastRoom();
        } else {
          await LeaveGameCastRoom();
        }
        gameCastReceiverRef.current?.close('viewer disconnected');
        gameCastReceiverRef.current = null;
        setRemoteMediaStream(null);
        setWebrtcStats(null);
        setGameCast((current) => ({
          ...current,
          status: { state: 'closed', detail: wasHost ? 'host room closed' : 'viewer disconnected' },
          receiverState: 'closed',
          roomState: null,
          role: null,
          roomEntry: null,
          activeSharePath: null,
          operationError: null,
          viewerReconnectAttempt: 0,
        }));
        dispatch({ type: 'ROOM_LEFT' });
        setRoomToolsOpen(false);
        setVoiceState(initialVoiceState);
        return;
      }
      await LeaveRoom();
      setRoomToolsOpen(false);
      setVoiceState(initialVoiceState);
    });

  const stopGameCastHostShare = async (roomId: string, detail = 'host stopped') => {
    gameCastHostRef.current?.stop(detail);
    gameCastHostRef.current = null;
    await DisconnectGameCast().catch(() => undefined);
    const roomState = toGameCastRoomState(
      await SetGameCastHostSharing({
        roomId,
        sharing: false,
        peerId: GAMECAST_HOST_PEER_ID,
        audioMode: 'off',
      }),
      roomId,
    );
    setRemoteMediaStream(null);
    setWebrtcStats(null);
    setGameCast((current) => ({
      ...current,
      roomState,
      receiverState: 'host-ready',
      status: { state: 'host-ready', detail: roomId },
      role: 'host',
      activeSharePath: null,
      operationError: null,
      viewerReconnectAttempt: 0,
    }));
    dispatch({ type: 'STREAM_STOPPED', peerId: GAMECAST_HOST_PEER_ID });
  };

  const stopGameCastCompatShare = async (roomId: string, detail = 'compat host stopped') => {
    gameCastHostRef.current?.stop(detail);
    gameCastHostRef.current = null;
    gameCastActiveSharePathRef.current = null;
    await DisconnectGameCast().catch(() => undefined);
    const roomState = toGameCastRoomState(
      await SetGameCastHostSharing({
        roomId,
        sharing: false,
        peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID,
        audioMode: 'off',
      }),
      roomId,
    );
    setRemoteMediaStream(null);
    setWebrtcStats(null);
    setGameCast((current) => ({
      ...current,
      roomState,
      receiverState: 'idle',
      status: { state: 'connected', detail: roomId },
      activeSharePath: null,
      operationError: null,
      viewerReconnectAttempt: 0,
    }));
    dispatch({ type: 'STREAM_STOPPED', peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID });
  };

  const failGameCastCompatShare = async (roomId: string, detail: string): Promise<string> => {
    gameCastHostRef.current?.stop('compat host failed');
    gameCastHostRef.current = null;
    gameCastActiveSharePathRef.current = null;
    await DisconnectGameCast().catch(() => undefined);
    let roomState: GameCastRoomState | null = null;
    try {
      roomState = toGameCastRoomState(
        await SetGameCastHostSharing({
          roomId,
          sharing: false,
          peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID,
          audioMode: 'off',
        }),
        roomId,
      );
    } catch {
      // The viewer remains in the room even if room-state cleanup is temporarily unavailable.
    }

    const message = formatGameCastFailure('compat-share-start', detail);
    setRemoteMediaStream(null);
    setWebrtcStats(null);
    setGameCast((current) => ({
      ...current,
      roomState: roomState || current.roomState,
      receiverState: 'idle',
      activeSharePath: null,
      operationError: { kind: 'compat-share-start', detail },
      viewerReconnectAttempt: 0,
    }));
    dispatch({ type: 'STREAM_STOPPED', peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID });
    setError(message);
    return message;
  };

  const handleStartSharing = (settings: ShareSettings) =>
    withBusy(async () => {
      setGameCast((current) => ({
        ...current,
        operationError: null,
      }));
      const selectedAudioSource = state.audioSources.find((source) => source.selected);
      const requestedAudio = nativeGameCastAudioRequest(
        settings,
        selectedAudioSource?.name || '',
        selectedAudioSource?.pid,
      );
      const requestedAudioMode = requestedAudio.audioMode;
      const requestedAudioProcessName = requestedAudio.audioProcessName;

      if (
        (state.room.selfPeerId === GAMECAST_HOST_PEER_ID || state.room.selfPeerId === GAMECAST_SELF_PEER_ID) &&
        state.room.roomId
      ) {
        const roomId = state.room.roomId;
        const sharePath = selectGameCastSharePath({
          roomId,
          selfPeerId: state.room.selfPeerId,
          roomEntry: gameCast.roomEntry,
          roomOrigin: gameCast.roomState?.origin,
          role: gameCast.role,
          hostVideoCapable: state.room.hostVideoCapable,
          sourceKind: settings.sourceKind,
        });

        if (!sharePath) {
          const detail = t('gamecast.sharePathUnavailable');
          const message = formatGameCastFailure('host-switch', detail);
          setGameCast((current) => ({
            ...current,
            operationError: { kind: 'host-switch', detail },
            activeSharePath: null,
          }));
          throw new Error(message);
        }

        if (sharePath === 'web-compat') {
          gameCastActiveSharePathRef.current = 'web-compat';
          gameCastReceiverRef.current?.close('switching viewer to compat share');
          gameCastReceiverRef.current = null;
          gameCastHostRef.current?.stop('restarting');
          gameCastHostRef.current = null;
          let compatFailureHandled = false;

          const host = new GameCastHost({
            serverBaseUrl: gameCastProfileRef.current.baseUrl,
            onSignal: (signal) => SendGameCastSignal(signal),
            onLocalStream: (stream) => {
              setRemoteMediaStream(stream);
              setWebrtcStats(null);
            },
            onStateChange: (receiverState, detail) => {
              setGameCast((current) => ({
                ...current,
                receiverState,
                activeSharePath: current.activeSharePath || 'web-compat',
                status: detail ? { state: receiverState, detail } : current.status,
                operationError: receiverState === 'connected' ? null : current.operationError,
              }));
              if (receiverState === 'error' && !compatFailureHandled) {
                compatFailureHandled = true;
                void failGameCastCompatShare(roomId, detail || 'WebRTC compat host connection failed');
              }
              if (receiverState === 'closed') {
                setRemoteMediaStream(null);
                setWebrtcStats(null);
              }
            },
            onEnded: () => {
              setError(t('gamecast.compatShareEnded'));
              void stopGameCastCompatShare(roomId, 'compat capture ended').catch((err) => {
                const message = err instanceof Error ? err.message : String(err);
                setError(message);
              });
            },
          });
          gameCastHostRef.current = host;
          try {
            const captured = await host.capture(settings);
            await promoteGameCastViewerToHost(roomId, ConnectGameCastHost);
            const published = await host.publish();
            const shareInfo = {
              width: published.width || captured.width || settings.width,
              height: published.height || captured.height || settings.height,
              fps: published.fps || captured.fps || settings.fps,
            };
            const roomState = toGameCastRoomState(
              await SetGameCastHostSharing({
                roomId,
                sharing: true,
                peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID,
                width: shareInfo.width,
                height: shareInfo.height,
                fps: shareInfo.fps,
                audioMode: requestedAudioMode === 'off' ? 'off' : 'system',
              }),
              roomId,
            );
            setGameCast((current) => ({
              ...current,
              roomState,
              role: 'host',
              receiverState: 'sharing',
              activeSharePath: 'web-compat',
              operationError: null,
              viewerReconnectAttempt: 0,
              status: { state: 'sharing', detail: roomId },
            }));
            dispatch({
              type: 'STREAM_STARTED',
              peerId: state.room.selfPeerId || GAMECAST_SELF_PEER_ID,
              width: shareInfo.width,
              height: shareInfo.height,
              fps: shareInfo.fps,
            });
            setRoomToolsOpen(false);
            return;
          } catch (compatErr) {
            gameCastActiveSharePathRef.current = null;
            const message = compatErr instanceof Error ? compatErr.message : String(compatErr);
            if (!compatFailureHandled) {
              compatFailureHandled = true;
              throw new Error(await failGameCastCompatShare(roomId, message));
            }
            throw new Error(formatGameCastFailure('compat-share-start', message));
          }
        }

        gameCastHostRef.current?.stop('restarting');
        gameCastHostRef.current = null;
        gameCastActiveSharePathRef.current = 'native';
        try {
          const roomState = toGameCastRoomState(
            await StartNativeGameCastSharing({
              roomId,
              sharing: true,
              sourceId: settings.sourceId,
              sourceKind: settings.sourceKind,
              sourceLabel: settings.sourceLabel,
              width: settings.width,
              height: settings.height,
              fps: settings.fps,
              audioMode: requestedAudioMode,
              audioProcessName: requestedAudioProcessName,
              audioProcessId: requestedAudio.audioProcessId,
            }),
            roomId,
          );
          setRemoteMediaStream(null);
          setWebrtcStats(null);
          setGameCast((current) => ({
            ...current,
            roomState,
            role: 'host',
            receiverState: 'sharing',
            activeSharePath: 'native',
            operationError: null,
            viewerReconnectAttempt: 0,
            status: { state: 'sharing', detail: roomId },
          }));
          dispatch({ type: 'ROOM_JOINED', roomId, selfPeerId: GAMECAST_HOST_PEER_ID });
          dispatch({
            type: 'PEER_JOINED',
            member: {
              id: GAMECAST_HOST_PEER_ID,
              name: t('gamecast.hostSelf'),
              online: true,
              muted: false,
              sharing: true,
              clientType: 'client',
            },
          });
          dispatch({
            type: 'STREAM_STARTED',
            peerId: GAMECAST_HOST_PEER_ID,
            width: settings.width,
            height: settings.height,
            fps: settings.fps,
          });
          setRoomToolsOpen(false);
          return;
        } catch (nativeErr) {
          gameCastActiveSharePathRef.current = null;
          const detail = nativeErr instanceof Error ? nativeErr.message : String(nativeErr);
          await DisconnectGameCast().catch(() => undefined);
          let roomState: GameCastRoomState | null = null;
          try {
            roomState = toGameCastRoomState(
              await SetGameCastHostSharing({
                roomId,
                sharing: false,
                peerId: GAMECAST_HOST_PEER_ID,
                audioMode: 'off',
              }),
              roomId,
            );
          } catch {
            // Keep the local host room usable even when the room-state endpoint is temporarily unavailable.
          }
          const message = formatGameCastFailure('native-share-start', detail);
          setGameCast((current) => ({
            ...current,
            roomState: roomState || current.roomState,
            receiverState: 'host-ready',
            activeSharePath: null,
            operationError: { kind: 'native-share-start', detail },
            viewerReconnectAttempt: 0,
            status: { state: 'host-ready', detail: roomId },
          }));
          setRemoteMediaStream(null);
          setWebrtcStats(null);
          dispatch({ type: 'STREAM_STOPPED', peerId: GAMECAST_HOST_PEER_ID });
          throw new Error(message);
        }
      }

      await StartSharing(selectedDisplayIndexFromSource(settings.sourceId, settings.sourceKind), settings.width, settings.height, settings.fps);
      setRoomToolsOpen(false);
    });

  const handleStopSharing = () =>
    withBusy(async () => {
      if (gameCast.activeSharePath === 'web-compat' && state.room.roomId) {
        await stopGameCastCompatShare(state.room.roomId);
        return;
      }
      if (state.room.selfPeerId === GAMECAST_HOST_PEER_ID && state.room.roomId) {
        await stopGameCastHostShare(state.room.roomId);
        return;
      }
      await StopSharing();
    });

  const handleToggleAudioSource = (pid: number) =>
    withBusy(async () => {
      const { SetAudioShareSelection } = await import('../wailsjs/go/main/App');
      const target = state.audioSources.find((source) => source.pid === pid);
      const shouldSelect = Boolean(target && !target.selected);
      const nextSources = state.audioSources.map((source) => ({
        ...source,
        selected: shouldSelect && source.pid === pid,
      }));
      const selected = shouldSelect && target?.name ? [target.name] : [];
      await SetAudioShareSelection(selected);
      dispatch({ type: 'AUDIO_SOURCES_UPDATED', sources: nextSources });
    });

  function selectedDisplayIndexFromSource(sourceId: string, sourceKind: string): number {
    if (sourceKind !== 'display') {
      return 0;
    }
    const match = /^display:(\d+)$/.exec(sourceId.trim());
    if (!match) {
      return 0;
    }
    const parsed = Number.parseInt(match[1], 10);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
  }

  const openRoomTools = (tab: RoomToolsTab) => {
    setRoomToolsInitialTab(tab);
    setRoomToolsOpen(true);
  };

  const setDiagnosticsOverlayVisible = (visible: boolean) => {
    setDiagnosticsOverlayVisibleState(visible);
    try {
      window.localStorage.setItem(DIAGNOSTICS_OVERLAY_STORAGE_KEY, visible ? 'true' : 'false');
    } catch {
      // Ignore local preference persistence errors.
    }
  };

  const setLanguage = (nextLanguage: Language) => {
    setLanguageState(nextLanguage);
    try {
      window.localStorage.setItem(LANGUAGE_STORAGE_KEY, nextLanguage);
    } catch {
      // Ignore local preference persistence errors.
    }
  };

  const handleStartVoice = () =>
    withBusy(async () => {
      setVoiceState((current) => ({ ...current, status: 'starting', error: null }));
      try {
        const snapshot = await StartVoiceChat();
        setVoiceState(toVoiceChatState(snapshot));
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setVoiceState((current) => ({
          ...current,
          active: false,
          status: 'error',
          error: message,
        }));
        throw err;
      }
    });

  const handleStopVoice = () =>
    withBusy(async () => {
      const snapshot = await StopVoiceChat();
      setVoiceState(toVoiceChatState(snapshot));
    });

  const handleToggleVoiceMuted = () =>
    withBusy(async () => {
      const snapshot = await SetVoiceMuted(!voiceState.muted);
      setVoiceState(toVoiceChatState(snapshot));
    });

  const handleVoiceVolumeChange = (volume: number) => {
    setVoiceState((current) => ({ ...current, volume }));
    SetVoiceVolume(volume)
      .then((snapshot) => setVoiceState(toVoiceChatState(snapshot)))
      .catch((err) => {
        setVoiceState((current) => ({
          ...current,
          status: 'error',
          error: err instanceof Error ? err.message : String(err),
        }));
      });
  };

  const handleOpenAuthUrl = () => {
    if (state.runtime?.networkAuthUrl) {
      BrowserOpenURL(state.runtime.networkAuthUrl);
    }
  };

  const handleOpenSettings = () => {
    setGameCast((current) => ({
      ...current,
      serverDraft: current.profile.baseUrl,
      probe: null,
    }));
    setSettingsOpen(true);
  };

  const handleCloseSettings = () => {
    setGameCast((current) => ({
      ...current,
      serverDraft: current.profile.baseUrl,
      probe: null,
    }));
    setSettingsOpen(false);
  };

  const handleSaveSettings = async () => {
    setSettingsSaving(true);
    setError(null);
    try {
      await SetUserPreferences({ displayName: displayNameDraft.trim() });
      if (showLegacyNetworkSettings) {
        await SetTailscaleAuthKey(authKeyDraft.trim());
      }
      if ((state.runtime?.mode || 'gamecast-client') === 'gamecast-client') {
        const saved = await SaveGameCastProfile({
          ...gameCast.profile,
          baseUrl: gameCast.serverDraft.trim(),
        });
        const profile = toGameCastProfile(saved);
        setGameCast((current) => ({
          ...current,
          profile,
          serverDraft: profile.baseUrl,
          probe: null,
        }));
      }
      setSettingsOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSettingsSaving(false);
    }
  };

  const handleProbeGameCast = () =>
    withBusy(async () => {
      const probe = await ProbeGameCastServer(gameCast.serverDraft.trim());
      setGameCast((current) => ({
        ...current,
        probe: probe as GameCastProbeResult,
      }));
    });

  const handleTrustGameCastCertificate = () =>
    withBusy(async () => {
      const fingerprint = gameCast.probe?.certificateFingerprint || '';
      const profile = await TrustGameCastServerCertificate(gameCast.serverDraft.trim(), fingerprint);
      const normalizedProfile = toGameCastProfile(profile);
      const probe = await ProbeGameCastServer(normalizedProfile.baseUrl);
      setGameCast((current) => ({
        ...current,
        profile: normalizedProfile,
        serverDraft: normalizedProfile.baseUrl,
        probe: probe as GameCastProbeResult,
      }));
    });

  const handleLoginGameCast = () =>
    withBusy(async () => {
      const session = await LoginGameCast({
        user: gameCast.authUserDraft.trim(),
        pass: gameCast.authPassDraft,
      });
      const history = await GetGameCastHistory().catch(() => gameCast.history);
      setGameCast((current) => ({
        ...current,
        session: toGameCastSession(session),
        authPassDraft: '',
        history: toGameCastHistory(history),
      }));
    });

  const handleRegisterGameCast = () =>
    withBusy(async () => {
      const session = await RegisterGameCast({
        user: gameCast.authUserDraft.trim(),
        pass: gameCast.authPassDraft,
      });
      const history = await GetGameCastHistory().catch(() => gameCast.history);
      setGameCast((current) => ({
        ...current,
        session: toGameCastSession(session),
        authPassDraft: '',
        history: toGameCastHistory(history),
      }));
    });

  const handleLogoutGameCast = () =>
    withBusy(async () => {
      await LogoutGameCast();
      setGameCast((current) => ({
        ...current,
        session: { user: 'guest', loggedIn: false },
        authPassDraft: '',
      }));
    });

  const handleCreateGameCastRoom = () =>
    withBusy(async () => {
      const saved = await SaveGameCastProfile({
        ...gameCast.profile,
        baseUrl: gameCast.serverDraft.trim(),
      });
      const profile = toGameCastProfile(saved);
      const ownRoomId = gameCast.session?.loggedIn ? gameCast.session.user.trim() : '';
      let existingRoom: GameCastRoomState | null = null;
      if (ownRoomId) {
        try {
          existingRoom = toGameCastRoomState(await GetGameCastRoomState(ownRoomId), ownRoomId);
        } catch {
          existingRoom = null;
        }
      }
      if (selectOwnGameCastRoomAction(ownRoomId, existingRoom) === 'open-browser') {
        setGameCast((current) => ({
          ...current,
          profile,
          serverDraft: profile.baseUrl,
          roomDraft: ownRoomId,
          roomState: existingRoom,
          operationError: null,
        }));
        BrowserOpenURL(`${profile.baseUrl.replace(/\/+$/, '')}/?room=${encodeURIComponent(ownRoomId)}`);
        return;
      }
      const room = toGameCastRoomSession(await CreateGameCastRoom());
      const [history, rooms] = await Promise.all([
        GetGameCastHistory().catch(() => gameCast.history),
        GetGameCastRooms().catch(() => gameCast.rooms),
      ]);
      setGameCast((current) => ({
        ...current,
        profile,
        serverDraft: profile.baseUrl,
        roomDraft: room.roomId,
        roomState: room.state,
        role: 'host',
        receiverState: 'host-ready',
        roomEntry: 'local-created',
        activeSharePath: null,
        operationError: null,
        viewerReconnectAttempt: 0,
        status: { state: 'host-ready', detail: room.roomId },
        history: toGameCastHistory(history),
        rooms: toGameCastRooms(rooms),
      }));
      dispatch({ type: 'ROOM_JOINED', roomId: room.roomId, selfPeerId: GAMECAST_HOST_PEER_ID });
      dispatch({
        type: 'PEER_JOINED',
        member: {
          id: GAMECAST_HOST_PEER_ID,
          name: t('gamecast.hostSelf'),
          online: true,
          muted: false,
          sharing: false,
        },
      });
    });

  const handleRemoveGameCastHistory = (roomId: string) =>
    withBusy(async () => {
      const history = await RemoveGameCastHistory(roomId);
      setGameCast((current) => ({
        ...current,
        history: toGameCastHistory(history),
      }));
    });

  const handleOpenGameCastWebRoom = (targetRoomId?: string) => {
    const roomId = (targetRoomId || gameCast.roomDraft || state.room.roomId || '').trim();
    if (!roomId) {
      return;
    }
    const base = gameCast.profile.baseUrl || gameCast.serverDraft;
    BrowserOpenURL(`${base.replace(/\/+$/, '')}/?room=${encodeURIComponent(roomId)}`);
  };

  const handleConnectGameCastViewer = (targetRoomId?: string, knownRoomState?: GameCastRoomState) =>
    withBusy(async () => {
      const roomId = (targetRoomId || gameCast.roomDraft).trim();
      if (!roomId) {
        throw new Error(t('gamecast.roomRequired'));
      }
      const saved = await SaveGameCastProfile({
        ...gameCast.profile,
        baseUrl: gameCast.serverDraft.trim(),
      });
      const profile = toGameCastProfile(saved);
      let roomState = knownRoomState || optimisticGameCastRoomState(roomId);
      try {
        const fetchedState = toGameCastRoomState(await GetGameCastRoomState(roomId), roomId);
        roomState = fetchedState.active ? fetchedState : roomState;
      } catch {
        // A room selected from the live room list already has a usable state snapshot.
      }

      gameCastReceiverRef.current?.close('reconnecting');
      gameCastReceiverRef.current = null;
      setRemoteMediaStream(null);
      setWebrtcStats(null);
      await ConnectGameCastViewer(roomId, preferredViewerLayer());

      setGameCast((current) => ({
        ...current,
        profile,
        serverDraft: profile.baseUrl,
        roomDraft: roomId,
        roomState,
        receiverState: 'connecting',
        role: 'viewer',
        roomEntry: 'client-joined-viewer',
        activeSharePath: null,
        operationError: null,
        viewerReconnectAttempt: 0,
      }));
      dispatch({ type: 'ROOM_JOINED', roomId, selfPeerId: GAMECAST_SELF_PEER_ID });
      dispatch({
        type: 'PEER_JOINED',
        member: {
          id: GAMECAST_SELF_PEER_ID,
          name: t('gamecast.viewerSelf'),
          online: true,
          muted: false,
          sharing: false,
        },
      });
      dispatch({
        type: 'PEER_JOINED',
        member: {
          id: GAMECAST_HOST_PEER_ID,
          name: roomState.user || t('gamecast.remoteHost'),
          online: roomState.active,
          muted: false,
          sharing: roomState.sharing,
        },
      });
      if (roomState.sharing) {
        dispatch({
          type: 'STREAM_STARTED',
          peerId: GAMECAST_HOST_PEER_ID,
          width: 0,
          height: 0,
          fps: 0,
        });
      }
    });

  const handleJoinGameCastRoom = async (roomId: string, knownRoomState?: GameCastRoomState) => {
    const normalizedRoomId = roomId.trim();
    if (!normalizedRoomId) {
      return;
    }
    let roomState = knownRoomState;
    if (!roomState) {
      try {
        roomState = toGameCastRoomState(await GetGameCastRoomState(normalizedRoomId), normalizedRoomId);
      } catch {
        roomState = undefined;
      }
    }
    if (selectGameCastJoinAction(roomState?.origin) === 'open-browser') {
      handleOpenGameCastWebRoom(normalizedRoomId);
      return;
    }
    await handleConnectGameCastViewer(normalizedRoomId, roomState);
  };

  const handleDisconnectGameCast = () =>
    withBusy(async () => {
      await LeaveGameCastRoom();
      gameCastHostRef.current?.stop('viewer disconnected');
      gameCastHostRef.current = null;
      gameCastReceiverRef.current?.close('viewer disconnected');
      gameCastReceiverRef.current = null;
      setRemoteMediaStream(null);
      setWebrtcStats(null);
      setGameCast((current) => ({
        ...current,
        status: { state: 'closed', detail: 'viewer disconnected' },
        receiverState: 'closed',
        roomState: null,
        role: null,
        roomEntry: null,
        activeSharePath: null,
        operationError: null,
        viewerReconnectAttempt: 0,
      }));
      dispatch({ type: 'ROOM_LEFT' });
    });

  useEffect(() => {
    const roomId = state.room.roomId?.trim() || '';
    const shouldWatchWebShare =
      Boolean(roomId) &&
      gameCast.roomState?.origin === 'client' &&
      gameCast.roomState.sharing &&
      gameCast.roomState.shareOrigin === 'web' &&
      gameCast.role === 'host' &&
      state.room.selfPeerId === GAMECAST_HOST_PEER_ID &&
      gameCast.activeSharePath === null;

    if (!shouldWatchWebShare) {
      if (!gameCast.roomState?.sharing || gameCast.roomState?.shareOrigin !== 'web') {
        gameCastAutoViewerRoomRef.current = '';
      }
      return;
    }
    if (gameCastAutoViewerRoomRef.current === roomId) {
      return;
    }
    gameCastAutoViewerRoomRef.current = roomId;
    void handleConnectGameCastViewer(roomId, gameCast.roomState || undefined);
  }, [
    gameCast.activeSharePath,
    gameCast.role,
    gameCast.roomState,
    state.room.roomId,
    state.room.selfPeerId,
  ]);

  const scene = deriveScene(state.room);
  const inRoom = state.room.inRoom;
  const selfMember =
    state.members.find((member) => member.id === state.room.selfPeerId) || {
      ...fallbackSelfMember(state.room.selfPeerId, state.room),
      name: t('members.self'),
    };
  const otherMembers = state.members.filter((member) => member.id !== state.room.selfPeerId);
  const remoteViewerCount = Math.max(0, otherMembers.length);
  const hostCanStartSharing = canStartGameCastSharing(
    state.room.selfPeerId,
    state.room.hostVideoCapable,
    typeof navigator !== 'undefined' && typeof navigator.mediaDevices?.getDisplayMedia === 'function',
    gameCast.roomState?.origin,
  ) && !gameCast.roomState?.sharing;
  const activeStreamOwnerName = state.streamSettings
    ? state.streamSettings.peerId === state.room.selfPeerId
      ? t('members.self')
      : state.members.find((member) => member.id === state.streamSettings?.peerId)?.name || state.streamSettings.peerId
    : null;
  const voiceButtonLabel =
    voiceState.status === 'starting'
      ? t('voice.connecting')
      : voiceState.status === 'error'
        ? t('voice.error')
        : voiceState.muted
          ? t('voice.muted')
          : voiceState.active
            ? t('voice.active')
            : t('members.voiceChat');
  const roomDiagnosticsOverlay = diagnosticsOverlayVisible ? (
    <DiagnosticsPanel
      variant="overlay"
      showHeader={false}
      mode={state.runtime?.mode || 'unknown'}
      networkBackend={state.runtime?.networkBackend || 'unknown'}
      videoCapability={state.capability}
      audioCapability={state.audioCapability}
      runtime={state.runtime}
      inRoom={state.room.inRoom}
      roomId={state.room.roomId}
      sharing={state.room.sharing}
      selfSharing={state.room.selfSharing}
      hostVideoCapable={hostCanStartSharing}
      canShareToPeers={state.room.canShareToPeers}
      remoteViewerCount={remoteViewerCount}
      streamStats={state.streamStats}
      t={t}
      onOpenAuthUrl={handleOpenAuthUrl}
    />
  ) : null;
  const gameCastNeedsTrust = gameCast.probe?.status === 'tls_untrusted' && Boolean(gameCast.probe.certificateFingerprint);
  const ownGameCastRoomID = gameCast.session?.loggedIn ? gameCast.session.user || '' : '';
  const joinableGameCastRooms = gameCast.rooms.filter(
    (room) => shouldListGameCastRoom(room, ownGameCastRoomID),
  );
  const gameCastStatusText = gameCast.status?.detail
    ? `${gameCast.status.state}: ${gameCast.status.detail}`
    : gameCast.status?.state || t('gamecast.idle');
  const gameCastSharePathText =
    gameCast.activeSharePath === 'native'
      ? t('gamecast.pathNative')
      : gameCast.activeSharePath === 'web-compat'
        ? t('gamecast.pathCompat')
        : '';
  const gameCastMediaStateText = gameCastSharePathText
    ? t('gamecast.mediaStateWithPath', { value: gameCast.receiverState, path: gameCastSharePathText })
    : gameCast.receiverState;
  const gameCastRoomStateText = gameCast.roomState
    ? gameCast.roomState.sharing && gameCast.roomState.sfu
      ? t('gamecast.roomLive')
      : gameCast.roomState.active
        ? t('gamecast.roomActive')
        : t('gamecast.roomIdle')
    : t('gamecast.roomUnknown');
  const canSubmitGameCastAuth = Boolean(gameCast.authUserDraft.trim() && gameCast.authPassDraft);
  const runtimeMode = state.runtime?.mode || 'gamecast-client';
  const showLegacyLobbyTools = isLegacyRuntimeMode(runtimeMode);
  const showLegacyNetworkSettings = runtimeMode === 'networked' || runtimeMode === 'tailscale-system';
  const gameCastServerConfigured = Boolean(gameCast.profile.baseUrl.trim());
  const gameCastStatusItems = [
    { label: t('gamecast.server'), value: gameCast.probe?.status || t('runtime.unknown') },
    { label: t('gamecast.room'), value: gameCastRoomStateText },
    { label: t('gamecast.media'), value: gameCastMediaStateText },
    { label: t('gamecast.signal'), value: gameCastStatusText },
  ];

  return (
    <div className={`app ${scene}`}>
      <div className="title-bar">
        <span className="title-bar-text">PartyLAN</span>
        <div className="title-bar-buttons">
          <button className="title-btn" title={t('app.settings')} aria-label={t('app.settings')} onClick={handleOpenSettings}>
            ⚙
          </button>
          <button className="title-btn" title={t('app.minimize')} aria-label={t('app.minimize')} onClick={WindowMinimise}>
            −
          </button>
          <button className="title-btn close" title={t('app.close')} aria-label={t('app.close')} onClick={Quit}>
            ×
          </button>
        </div>
      </div>

      <div className="status-shell">
        <StatusBar stats={state.network} capability={state.capability} runtime={state.runtime} t={t} />
      </div>

      <div className="scene-content">
        {!inRoom ? (
          <LobbyView
            t={t}
            busy={busy}
            error={error}
            session={gameCast.session}
            authUser={gameCast.authUserDraft}
            authPass={gameCast.authPassDraft}
            rooms={joinableGameCastRooms}
            history={gameCast.history}
            statusItems={gameCastStatusItems}
            serverConfigured={gameCastServerConfigured}
            serverNeedsTrust={gameCastNeedsTrust}
            canSubmitAuth={canSubmitGameCastAuth}
            canOpenWeb={Boolean(gameCast.roomDraft.trim() || state.room.roomId)}
            onAuthUserChange={(value) => setGameCast((current) => ({ ...current, authUserDraft: value }))}
            onAuthPassChange={(value) => setGameCast((current) => ({ ...current, authPassDraft: value }))}
            onLogin={handleLoginGameCast}
            onRegister={handleRegisterGameCast}
            onLogout={handleLogoutGameCast}
            onCreateRoom={handleCreateGameCastRoom}
            onJoinRoom={(roomId, room) => {
              void handleJoinGameCastRoom(roomId, room);
            }}
            onRemoveHistory={(roomId) => {
              void handleRemoveGameCastHistory(roomId);
            }}
            onOpenWeb={() => handleOpenGameCastWebRoom()}
            onOpenSettings={handleOpenSettings}
            legacyContent={showLegacyLobbyTools ? (
              <>
                <RoomControls
                  inRoom={false}
                  roomId={null}
                  sharing={false}
                  selfSharing={false}
                  canControlSharing={true}
                  hostVideoCapable={hostCanStartSharing}
                  canShareToPeers={false}
                  remoteViewerCount={0}
                  busy={busy}
                  t={t}
                  onCreateRoom={handleCreateRoom}
                  onJoinRoom={handleJoinRoom}
                  onLeaveRoom={handleLeaveRoom}
                  onOpenShareSettings={() => openRoomTools('share')}
                  onStopSharing={handleStopSharing}
                />
                <AvailableNodes
                  nodes={state.nodes}
                  busy={busy}
                  t={t}
                  onJoinNode={(nodeId: string) => handleJoinRoom(nodeId)}
                />
              </>
            ) : undefined}
          />
        ) : (
          <div className="room-layout">
            <aside className="room-sidebar">
              <div className="member-list-wrapper">
                <MemberList
                  selfMember={selfMember}
                  otherMembers={otherMembers}
                  t={t}
                  voiceLabel={voiceButtonLabel}
                  voiceActive={voiceState.active}
                  voiceMuted={voiceState.muted}
                  onOpenVoice={() => openRoomTools('voice')}
                />
              </div>

              <RoomControls
                inRoom={state.room.inRoom}
                roomId={state.room.roomId}
                sharing={state.room.sharing}
                selfSharing={state.room.selfSharing}
                canControlSharing={!state.room.sharing || state.room.selfSharing}
                hostVideoCapable={hostCanStartSharing}
                canShareToPeers={state.room.canShareToPeers}
                remoteViewerCount={remoteViewerCount}
                busy={busy}
                t={t}
                onCreateRoom={handleCreateRoom}
                onJoinRoom={handleJoinRoom}
                onLeaveRoom={handleLeaveRoom}
                onOpenShareSettings={() => openRoomTools('share')}
                onStopSharing={handleStopSharing}
              />
            </aside>

            <main className="room-main">
              <div className="viewer-stage">
                <ScreenPreview
                  frame={state.frame}
                  remoteStream={remoteMediaStream}
                  webrtcStats={webrtcStats}
                  localStreamStats={state.streamStats}
                  inRoom={state.room.inRoom}
                  sharing={state.room.sharing}
                  selfSharing={state.room.selfSharing}
                  remoteViewerCount={remoteViewerCount}
                  streamSettings={state.streamSettings}
                  streamOwnerName={activeStreamOwnerName}
                  diagnosticsOverlay={roomDiagnosticsOverlay}
                  t={t}
                />
              </div>

              {error && <div className="error-banner">{error}</div>}
            </main>
          </div>
        )}
      </div>

      <SettingsPanel
        open={settingsOpen}
        authKey={authKeyDraft}
        displayName={displayNameDraft}
        gameCastServer={gameCast.serverDraft}
        gameCastProbe={gameCast.probe}
        gameCastNeedsTrust={gameCastNeedsTrust}
        runtime={state.runtime}
        videoCapability={state.capability}
        audioCapability={state.audioCapability}
        inRoom={state.room.inRoom}
        roomId={state.room.roomId}
        sharing={state.room.sharing}
        selfSharing={state.room.selfSharing}
        hostVideoCapable={hostCanStartSharing}
        canShareToPeers={state.room.canShareToPeers}
        remoteViewerCount={remoteViewerCount}
        diagnosticsOverlayVisible={diagnosticsOverlayVisible}
        language={language}
        busy={busy}
        saving={settingsSaving}
        error={error}
        t={t}
        onClose={handleCloseSettings}
        onAuthKeyChange={setAuthKeyDraft}
        onDisplayNameChange={setDisplayNameDraft}
        onGameCastServerChange={(value) =>
          setGameCast((current) => ({ ...current, serverDraft: value, probe: null }))
        }
        onProbeGameCast={() => void handleProbeGameCast()}
        onTrustGameCastCertificate={() => void handleTrustGameCastCertificate()}
        onDiagnosticsOverlayVisibleChange={setDiagnosticsOverlayVisible}
        onLanguageChange={setLanguage}
        onSave={() => void handleSaveSettings()}
        onOpenAuthUrl={handleOpenAuthUrl}
      />

      <RoomToolsDialog
        open={roomToolsOpen}
        initialTab={roomToolsInitialTab}
        busy={busy}
        sharing={state.room.sharing}
        hostVideoCapable={hostCanStartSharing}
        shareSources={shareSources}
        audio={state.audio}
        audioCapability={state.audioCapability}
        audioSources={state.audioSources}
        voice={voiceState}
        error={error}
        t={t}
        onClose={() => setRoomToolsOpen(false)}
        onStartSharing={handleStartSharing}
        onToggleMic={() => dispatch({ type: 'TOGGLE_MIC' })}
        onToggleSpeaker={() => dispatch({ type: 'TOGGLE_SPEAKER' })}
        onVolumeChange={(volume: number) => dispatch({ type: 'SET_VOLUME', volume })}
        onToggleAudioSource={handleToggleAudioSource}
        onStartVoice={handleStartVoice}
        onStopVoice={handleStopVoice}
        onToggleVoiceMuted={handleToggleVoiceMuted}
        onVoiceVolumeChange={handleVoiceVolumeChange}
      />
    </div>
  );
}
