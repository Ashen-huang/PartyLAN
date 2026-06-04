import { useEffect, useMemo, useReducer, useRef, useState } from 'react';
import './App.css';
import MemberList from './components/MemberList';
import AvailableNodes from './components/AvailableNodes';
import ScreenPreview from './components/ScreenPreview';
import StatusBar from './components/StatusBar';
import RoomControls from './components/RoomControls';
import RoomToolsDialog, { type RoomToolsTab } from './components/RoomToolsDialog';
import SettingsPanel from './components/SettingsPanel';
import DiagnosticsPanel, { pickNetworkNote } from './components/DiagnosticsPanel';
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
  CreateRoom,
  GetAudioCapability,
  GetTailscaleAuthKey,
  GetUserPreferences,
  GetAvailableNodes,
  GetNetworkStatus,
  JoinRoom,
  LeaveRoom,
  GetRuntimeStatus,
  GetVideoCapability,
  GetVoiceState,
  SetVoiceMuted,
  SetVoiceVolume,
  SetTailscaleAuthKey,
  SetUserPreferences,
  StartSharing,
  StartVoiceChat,
  StopSharing,
  StopVoiceChat,
} from '../wailsjs/go/main/App';
import type {
  AudioCapability,
  Member,
  NetworkStats,
  RoomState,
  AudioState,
  AudioShareSource,
  StreamFrame,
  StreamSettings,
  AvailableNode,
  VideoCapability,
  RuntimeStatus,
  MediaSignal,
  WebRTCPlaybackStats,
  VoiceChatState,
  VoiceStatus,
  UserPreferences,
} from './types';

type Scene = 'lobby' | 'room-idle' | 'room-sharing-self' | 'room-sharing-watch';

const DIAGNOSTICS_OVERLAY_STORAGE_KEY = 'partylan.roomDiagnosticsOverlayVisible';

interface AppState {
  members: Member[];
  network: NetworkStats;
  room: RoomState;
  audio: AudioState;
  audioSources: AudioShareSource[];
  frame: StreamFrame | null;
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
  | { type: 'PEER_VOICE_STATE'; id: string; state: number }
  | { type: 'STREAM_STARTED'; peerId: string; width: number; height: number; fps: number }
  | { type: 'STREAM_STOPPED'; peerId: string }
  | { type: 'FRAME_RECEIVED'; frame: StreamFrame }
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
      return {
        ...state,
        room: {
          ...state.room,
          roomId: action.roomId,
          inRoom: true,
          selfPeerId: action.selfPeerId,
          canShareToPeers: hasRemoteMembers(state.members, action.selfPeerId),
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
        members: state.members.map((member) =>
          member.id === action.peerId ? { ...member, sharing: false } : member,
        ),
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

const initialVoiceState: VoiceChatState = toVoiceChatState({});

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
  const [authKeyDraft, setAuthKeyDraft] = useState('');
  const [displayNameDraft, setDisplayNameDraft] = useState('');
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [remoteMediaStream, setRemoteMediaStream] = useState<MediaStream | null>(null);
  const [webrtcStats, setWebrtcStats] = useState<WebRTCPlaybackStats | null>(null);
  const selfPeerIdRef = useRef('');
  const mediaReceiverRef = useRef<WebRTCReceiver | null>(null);
  const t = useMemo(() => createTranslator(language), [language]);

  useEffect(() => {
    selfPeerIdRef.current = state.room.selfPeerId || '';
  }, [state.room.selfPeerId]);

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
        mediaReceiverRef.current?.close('room left');
        mediaReceiverRef.current = null;
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
              setWebrtcStats(stats);
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
      EventsOn('error', (payload) => {
        setError(payload.message || 'Unexpected error');
        dispatch({ type: 'SET_ERROR_STATE' });
      }),
    ];

    return () => {
      unsubscribers.forEach((unsubscribe) => unsubscribe());
    };
  }, []);

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
  }, []);

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
    let cancelled = false;

    async function loadSettings() {
      try {
        const [authKey, preferences] = await Promise.all([
          GetTailscaleAuthKey(),
          GetUserPreferences(),
        ]);
        if (!cancelled) {
          setAuthKeyDraft(authKey || '');
          setDisplayNameDraft((preferences as Partial<UserPreferences>)?.displayName || '');
        }
      } catch {
        if (!cancelled) {
          setAuthKeyDraft('');
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

  async function withBusy(task: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await task();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
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
      await LeaveRoom();
      setRoomToolsOpen(false);
      setVoiceState(initialVoiceState);
    });

  const handleStartSharing = (settings: ShareSettings) =>
    withBusy(async () => {
      await StartSharing(0, settings.width, settings.height, settings.fps);
      setRoomToolsOpen(false);
    });

  const handleStopSharing = () =>
    withBusy(async () => {
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

  const handleSaveSettings = async () => {
    setSettingsSaving(true);
    setError(null);
    try {
      await SetUserPreferences({ displayName: displayNameDraft.trim() });
      await SetTailscaleAuthKey(authKeyDraft.trim());
      setSettingsOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSettingsSaving(false);
    }
  };

  const scene = deriveScene(state.room);
  const inRoom = state.room.inRoom;
  const selfMember =
    state.members.find((member) => member.id === state.room.selfPeerId) || {
      ...fallbackSelfMember(state.room.selfPeerId, state.room),
      name: t('members.self'),
    };
  const otherMembers = state.members.filter((member) => member.id !== state.room.selfPeerId);
  const remoteViewerCount = Math.max(0, otherMembers.length);
  const networkNote = pickNetworkNote(state.runtime?.networkHealth);
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
      hostVideoCapable={state.room.hostVideoCapable}
      canShareToPeers={state.room.canShareToPeers}
      remoteViewerCount={remoteViewerCount}
      t={t}
      onOpenAuthUrl={handleOpenAuthUrl}
    />
  ) : null;

  return (
    <div className={`app ${scene}`}>
      <div className="title-bar">
        <span className="title-bar-text">PartyLAN</span>
        <div className="title-bar-buttons">
          <button className="title-btn" title={t('app.settings')} onClick={() => setSettingsOpen(true)}>
            ⚙
          </button>
          <button className="title-btn" title={t('app.minimize')} onClick={WindowMinimise}>
            −
          </button>
          <button className="title-btn close" title={t('app.close')} onClick={Quit}>
            ×
          </button>
        </div>
      </div>

      <div className="status-shell">
        <StatusBar stats={state.network} capability={state.capability} runtime={state.runtime} t={t} />
      </div>

      <div className="scene-content">
        {!inRoom ? (
          <div className="lobby-layout">
            <div className="lobby-main">
              <section className="hero-panel">
                <div className="hero-eyebrow">{t('lobby.eyebrow')}</div>
                <h1 className="hero-title">{t('lobby.title')}</h1>
                <p className="hero-copy">
                  {t('lobby.copy')}
                </p>
              </section>

              <RoomControls
                inRoom={false}
                roomId={null}
                sharing={false}
                selfSharing={false}
                canControlSharing={true}
                hostVideoCapable={state.room.hostVideoCapable}
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

              <AvailableNodes nodes={state.nodes} busy={busy} t={t} onJoinNode={(nodeId: string) => handleJoinRoom(nodeId)} />
            </div>

            <div className="lobby-side">
              <section className="welcome-panel">
                <div className="welcome-panel-header">
                  <span className="welcome-kicker">{t('lobby.welcome')}</span>
                  <span className="welcome-version">{state.runtime?.version || t('app.versionUnknown')}</span>
                </div>
                <div className="welcome-note">
                  {networkNote || t('lobby.networkNoteFallback')}
                </div>
                <ul className="quick-list">
                  <li>{t('lobby.quickCreate')}</li>
                  <li>{t('lobby.quickJoin')}</li>
                  <li>{t('lobby.quickSettings')}</li>
                </ul>
              </section>

              <DiagnosticsPanel
                title={t('diag.networkConsole')}
                mode={state.runtime?.mode || 'unknown'}
                networkBackend={state.runtime?.networkBackend || 'unknown'}
                videoCapability={state.capability}
                audioCapability={state.audioCapability}
                runtime={state.runtime}
                inRoom={false}
                roomId={null}
                sharing={false}
                selfSharing={false}
                hostVideoCapable={state.room.hostVideoCapable}
                canShareToPeers={false}
                remoteViewerCount={0}
                t={t}
                onOpenAuthUrl={handleOpenAuthUrl}
              />

              {error && <div className="error-banner">{error}</div>}
            </div>
          </div>
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
                hostVideoCapable={state.room.hostVideoCapable}
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
        runtime={state.runtime}
        videoCapability={state.capability}
        audioCapability={state.audioCapability}
        inRoom={state.room.inRoom}
        roomId={state.room.roomId}
        sharing={state.room.sharing}
        selfSharing={state.room.selfSharing}
        hostVideoCapable={state.room.hostVideoCapable}
        canShareToPeers={state.room.canShareToPeers}
        remoteViewerCount={remoteViewerCount}
        diagnosticsOverlayVisible={diagnosticsOverlayVisible}
        language={language}
        saving={settingsSaving}
        error={error}
        t={t}
        onClose={() => setSettingsOpen(false)}
        onAuthKeyChange={setAuthKeyDraft}
        onDisplayNameChange={setDisplayNameDraft}
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
        hostVideoCapable={state.room.hostVideoCapable}
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
