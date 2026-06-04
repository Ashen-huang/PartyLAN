package app

type Event string

const (
	EventPeerJoined      Event = "peer:joined"
	EventPeerLeft        Event = "peer:left"
	EventPeerVoiceState  Event = "peer:voice-state"
	EventStreamStarted   Event = "stream:started"
	EventStreamStopped   Event = "stream:stopped"
	EventStreamFrame     Event = "stream:frame"
	EventMediaSignal     Event = "media:signal"
	EventRoomCreated     Event = "room:created"
	EventRoomJoined      Event = "room:joined"
	EventRoomLeft        Event = "room:left"
	EventNetworkStatus   Event = "network:status"
	EventVideoCapability Event = "video:capability"
	EventAudioCapability Event = "audio:capability"
	EventRuntimeStatus   Event = "runtime:status"
	EventError           Event = "error"
)

type PeerEvent struct {
	PeerID   string `json:"peerId"`
	Hostname string `json:"hostname"`
}

type VoiceStateEvent struct {
	PeerID string `json:"peerId"`
	State  int    `json:"state"` // 0=idle, 1=muted, 2=talking
}

type StreamEvent struct {
	PeerID string `json:"peerId"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
}

type NetworkStatusEvent struct {
	NodeID         string `json:"nodeId"`
	IP             string `json:"ip"`
	PeerCount      int    `json:"peerCount"`
	ConnectionType string `json:"connectionType"` // "p2p" or "relay"
	Latency        int64  `json:"latency"`
}

type VideoCapabilityEvent struct {
	Backend      string `json:"backend"`
	Status       string `json:"status"`
	Adapter      string `json:"adapter"`
	Driver       string `json:"driver"`
	Reason       string `json:"reason"`
	SupportsHost bool   `json:"supportsHost"`
	SupportsView bool   `json:"supportsView"`
}

type AudioCapabilityEvent struct {
	SupportsProcessCapture bool   `json:"supportsProcessCapture"`
	Reason                 string `json:"reason"`
}

type RuntimeStatusEvent struct {
	Mode           string   `json:"mode"`
	NetworkBackend string   `json:"networkBackend"`
	NetworkState   string   `json:"networkState"`
	NetworkAuthURL string   `json:"networkAuthUrl"`
	NetworkHealth  []string `json:"networkHealth"`
	MediaHealth    []string `json:"mediaHealth"`
	VideoBackend   string   `json:"videoBackend"`
	VideoStatus    string   `json:"videoStatus"`
	AudioBackend   string   `json:"audioBackend"`
	AudioStatus    string   `json:"audioStatus"`
	Version        string   `json:"version"`
}

type ErrorEvent struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
