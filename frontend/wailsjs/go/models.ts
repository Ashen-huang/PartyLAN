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

