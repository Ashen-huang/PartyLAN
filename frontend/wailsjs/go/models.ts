export namespace app {

	export class AudioCapabilityEvent {
	    supportsProcessCapture: boolean;
	    reason: string;

	    static createFrom(source: any = {}) {
	        return new AudioCapabilityEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supportsProcessCapture = source["supportsProcessCapture"];
	        this.reason = source["reason"];
	    }
	}
	export class NetworkStatusEvent {
	    nodeId: string;
	    ip: string;
	    peerCount: number;
	    connectionType: string;
	    latency: number;

	    static createFrom(source: any = {}) {
	        return new NetworkStatusEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodeId = source["nodeId"];
	        this.ip = source["ip"];
	        this.peerCount = source["peerCount"];
	        this.connectionType = source["connectionType"];
	        this.latency = source["latency"];
	    }
	}
	export class RuntimeStatusEvent {
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

	    static createFrom(source: any = {}) {
	        return new RuntimeStatusEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.networkBackend = source["networkBackend"];
	        this.networkState = source["networkState"];
	        this.networkAuthUrl = source["networkAuthUrl"];
	        this.networkHealth = source["networkHealth"];
	        this.mediaHealth = source["mediaHealth"];
	        this.videoBackend = source["videoBackend"];
	        this.videoStatus = source["videoStatus"];
	        this.audioBackend = source["audioBackend"];
	        this.audioStatus = source["audioStatus"];
	        this.version = source["version"];
	    }
	}
	export class VideoCapabilityEvent {
	    backend: string;
	    status: string;
	    adapter: string;
	    driver: string;
	    reason: string;
	    supportsHost: boolean;
	    supportsView: boolean;

	    static createFrom(source: any = {}) {
	        return new VideoCapabilityEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.backend = source["backend"];
	        this.status = source["status"];
	        this.adapter = source["adapter"];
	        this.driver = source["driver"];
	        this.reason = source["reason"];
	        this.supportsHost = source["supportsHost"];
	        this.supportsView = source["supportsView"];
	    }
	}

}

export namespace gamecast {

	export class HistoryEntry {
	    id: string;
	    time: number;

	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.time = source["time"];
	    }
	}
	export class MediaCapabilities {
	    sfuViewer: boolean;
	    sfuHost: boolean;
	    h264: boolean;
	    opus: boolean;

	    static createFrom(source: any = {}) {
	        return new MediaCapabilities(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sfuViewer = source["sfuViewer"];
	        this.sfuHost = source["sfuHost"];
	        this.h264 = source["h264"];
	        this.opus = source["opus"];
	    }
	}
	export class PresenceParticipant {
	    participantId: string;
	    room: string;
	    name?: string;
	    clientType: string;
	    role: string;
	    active: boolean;
	    sharing: boolean;
	    mediaProtocolVersion?: number;
	    capabilities?: MediaCapabilities;
	    ttl?: number;
	    ageSeconds?: number;

	    static createFrom(source: any = {}) {
	        return new PresenceParticipant(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.participantId = source["participantId"];
	        this.room = source["room"];
	        this.name = source["name"];
	        this.clientType = source["clientType"];
	        this.role = source["role"];
	        this.active = source["active"];
	        this.sharing = source["sharing"];
	        this.mediaProtocolVersion = source["mediaProtocolVersion"];
	        this.capabilities = this.convertValues(source["capabilities"], MediaCapabilities);
	        this.ttl = source["ttl"];
	        this.ageSeconds = source["ageSeconds"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProbeResult {
	    ok: boolean;
	    status: string;
	    baseUrl: string;
	    user?: string;
	    loggedIn: boolean;
	    roomName?: string;
	    certificateFingerprint?: string;
	    certificateTrusted: boolean;
	    message?: string;

	    static createFrom(source: any = {}) {
	        return new ProbeResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.status = source["status"];
	        this.baseUrl = source["baseUrl"];
	        this.user = source["user"];
	        this.loggedIn = source["loggedIn"];
	        this.roomName = source["roomName"];
	        this.certificateFingerprint = source["certificateFingerprint"];
	        this.certificateTrusted = source["certificateTrusted"];
	        this.message = source["message"];
	    }
	}
	export class RoomState {
	    room: string;
	    active: boolean;
	    origin?: string;
	    ownerParticipantId?: string;
	    ownerName?: string;
	    sharing: boolean;
	    mediaMode?: string;
	    mediaProtocolVersion?: number;
	    sharePhase?: string;
	    shareOwnerParticipantId?: string;
	    shareOwnerName?: string;
	    shareOrigin?: string;
	    sfu: boolean;
	    audioMode?: string;
	    user?: string;
	    ttl?: number;
	    participantCount?: number;
	    // Go type: time
	    updatedAt?: any;
	    updatedAtUnix?: number;

	    static createFrom(source: any = {}) {
	        return new RoomState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.room = source["room"];
	        this.active = source["active"];
	        this.origin = source["origin"];
	        this.ownerParticipantId = source["ownerParticipantId"];
	        this.ownerName = source["ownerName"];
	        this.sharing = source["sharing"];
	        this.mediaMode = source["mediaMode"];
	        this.mediaProtocolVersion = source["mediaProtocolVersion"];
	        this.sharePhase = source["sharePhase"];
	        this.shareOwnerParticipantId = source["shareOwnerParticipantId"];
	        this.shareOwnerName = source["shareOwnerName"];
	        this.shareOrigin = source["shareOrigin"];
	        this.sfu = source["sfu"];
	        this.audioMode = source["audioMode"];
	        this.user = source["user"];
	        this.ttl = source["ttl"];
	        this.participantCount = source["participantCount"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.updatedAtUnix = source["updatedAtUnix"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {

	export class AudioShareCandidate {
	    pid: number;
	    name: string;
	    displayName: string;
	    iconDataUrl: string;
	    active: boolean;
	    selected: boolean;

	    static createFrom(source: any = {}) {
	        return new AudioShareCandidate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.name = source["name"];
	        this.displayName = source["displayName"];
	        this.iconDataUrl = source["iconDataUrl"];
	        this.active = source["active"];
	        this.selected = source["selected"];
	    }
	}
	export class GameCastCredentials {
	    user: string;
	    pass: string;

	    static createFrom(source: any = {}) {
	        return new GameCastCredentials(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user = source["user"];
	        this.pass = source["pass"];
	    }
	}
	export class GameCastHostShareRequest {
	    roomId: string;
	    sharing: boolean;
	    peerId?: string;
	    sourceId?: string;
	    sourceKind?: string;
	    sourceLabel?: string;
	    width?: number;
	    height?: number;
	    fps?: number;
	    audioMode?: string;
	    audioProcessName?: string;
	    audioProcessId?: number;

	    static createFrom(source: any = {}) {
	        return new GameCastHostShareRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.roomId = source["roomId"];
	        this.sharing = source["sharing"];
	        this.peerId = source["peerId"];
	        this.sourceId = source["sourceId"];
	        this.sourceKind = source["sourceKind"];
	        this.sourceLabel = source["sourceLabel"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.fps = source["fps"];
	        this.audioMode = source["audioMode"];
	        this.audioProcessName = source["audioProcessName"];
	        this.audioProcessId = source["audioProcessId"];
	    }
	}
	export class GameCastProfile {
	    baseUrl: string;
	    displayName?: string;
	    trustedCertificateSha256?: string;

	    static createFrom(source: any = {}) {
	        return new GameCastProfile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseUrl = source["baseUrl"];
	        this.displayName = source["displayName"];
	        this.trustedCertificateSha256 = source["trustedCertificateSha256"];
	    }
	}
	export class GameCastRoomSession {
	    roomId: string;
	    role: string;
	    state: gamecast.RoomState;

	    static createFrom(source: any = {}) {
	        return new GameCastRoomSession(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.roomId = source["roomId"];
	        this.role = source["role"];
	        this.state = this.convertValues(source["state"], gamecast.RoomState);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GameCastSessionState {
	    user: string;
	    loggedIn: boolean;
	    authMode?: string;
	    version?: string;
	    roomName?: string;

	    static createFrom(source: any = {}) {
	        return new GameCastSessionState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user = source["user"];
	        this.loggedIn = source["loggedIn"];
	        this.authMode = source["authMode"];
	        this.version = source["version"];
	        this.roomName = source["roomName"];
	    }
	}
	export class UserPreferences {
	    displayName: string;

	    static createFrom(source: any = {}) {
	        return new UserPreferences(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.displayName = source["displayName"];
	    }
	}
	export class VoiceStateSnapshot {
	    active: boolean;
	    muted: boolean;
	    volume: number;
	    state: number;
	    peerId: string;
	    experimental: boolean;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new VoiceStateSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.muted = source["muted"];
	        this.volume = source["volume"];
	        this.state = source["state"];
	        this.peerId = source["peerId"];
	        this.experimental = source["experimental"];
	        this.message = source["message"];
	    }
	}

}

export namespace network {

	export class NodeInfo {
	    ID: string;
	    Hostname: string;
	    IP: number[];
	    Online: boolean;
	    Latency: number;
	    Joinable: boolean;
	    RoomID: string;

	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Hostname = source["Hostname"];
	        this.IP = source["IP"];
	        this.Online = source["Online"];
	        this.Latency = source["Latency"];
	        this.Joinable = source["Joinable"];
	        this.RoomID = source["RoomID"];
	    }
	}

}

export namespace room {

	export class MediaSignalPayload {
	    sessionId: string;
	    fromPeerId: string;
	    toPeerId: string;
	    kind: string;
	    media: string;
	    payload?: number[];

	    static createFrom(source: any = {}) {
	        return new MediaSignalPayload(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.fromPeerId = source["fromPeerId"];
	        this.toPeerId = source["toPeerId"];
	        this.kind = source["kind"];
	        this.media = source["media"];
	        this.payload = source["payload"];
	    }
	}
	export class PeerInfo {
	    ID: string;
	    Hostname: string;
	    Role: number;
	    VoiceState: number;
	    IsSharing: boolean;
	    JoinedAtUnix: number;
	    VideoBackend: string;
	    VideoStatus: string;
	    VideoReason: string;
	    VideoSupportsHost: boolean;
	    VideoSupportsView: boolean;

	    static createFrom(source: any = {}) {
	        return new PeerInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Hostname = source["Hostname"];
	        this.Role = source["Role"];
	        this.VoiceState = source["VoiceState"];
	        this.IsSharing = source["IsSharing"];
	        this.JoinedAtUnix = source["JoinedAtUnix"];
	        this.VideoBackend = source["VideoBackend"];
	        this.VideoStatus = source["VideoStatus"];
	        this.VideoReason = source["VideoReason"];
	        this.VideoSupportsHost = source["VideoSupportsHost"];
	        this.VideoSupportsView = source["VideoSupportsView"];
	    }
	}

}

export namespace sharecatalog {

	export class Source {
	    id: string;
	    kind: string;
	    label: string;
	    subtitle?: string;
	    displayIndex?: number;
	    windowHandle?: string;
	    processId?: number;
	    processName?: string;
	    iconDataUrl?: string;
	    previewDataUrl?: string;
	    audioDefault: string;
	    audioProcessName?: string;

	    static createFrom(source: any = {}) {
	        return new Source(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.subtitle = source["subtitle"];
	        this.displayIndex = source["displayIndex"];
	        this.windowHandle = source["windowHandle"];
	        this.processId = source["processId"];
	        this.processName = source["processName"];
	        this.iconDataUrl = source["iconDataUrl"];
	        this.previewDataUrl = source["previewDataUrl"];
	        this.audioDefault = source["audioDefault"];
	        this.audioProcessName = source["audioProcessName"];
	    }
	}

}

export namespace voice {

	export class State {
	    connection: string;
	    microphone: string;
	    playbackMuted: boolean;
	    participantCount: number;
	    errorCode?: string;

	    static createFrom(source: any = {}) {
	        return new State(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connection = source["connection"];
	        this.microphone = source["microphone"];
	        this.playbackMuted = source["playbackMuted"];
	        this.participantCount = source["participantCount"];
	        this.errorCode = source["errorCode"];
	    }
	}

}

