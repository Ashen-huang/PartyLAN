package app

type Event string

const (
	EventPeerJoined           Event = "peer:joined"
	EventPeerLeft             Event = "peer:left"
	EventPeerVoiceState       Event = "peer:voice-state"
	EventStreamStarted        Event = "stream:started"
	EventStreamStopped        Event = "stream:stopped"
	EventStreamFrame          Event = "stream:frame"
	EventStreamStats          Event = "stream:stats"
	EventMediaSignal          Event = "media:signal"
	EventRoomCreated          Event = "room:created"
	EventRoomJoined           Event = "room:joined"
	EventRoomLeft             Event = "room:left"
	EventNetworkStatus        Event = "network:status"
	EventVideoCapability      Event = "video:capability"
	EventAudioCapability      Event = "audio:capability"
	EventRuntimeStatus        Event = "runtime:status"
	EventGameCastSignal       Event = "gamecast:signal"
	EventGameCastStatus       Event = "gamecast:status"
	EventGameCastShareRuntime Event = "gamecast:share-runtime"
	EventGameCastVoiceState   Event = "gamecast:voice-state"
	EventError                Event = "error"
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

type StreamStatsEvent struct {
	PeerID             string  `json:"peerId"`
	Width              int     `json:"width"`
	Height             int     `json:"height"`
	Mode               string  `json:"mode"`
	EncodedFPS         float64 `json:"encodedFps"`
	EstimatedSourceFPS float64 `json:"estimatedSourceFps"`
	BitrateKbps        float64 `json:"bitrateKbps"`
	Sessions           int     `json:"sessions"`
	RequestedWidth     int     `json:"requestedWidth,omitempty"`
	RequestedHeight    int     `json:"requestedHeight,omitempty"`
	RequestedFPS       int     `json:"requestedFps,omitempty"`
	ActualFPS          int     `json:"actualFps,omitempty"`
	FreshFPS           float64 `json:"freshFps,omitempty"`
	RepeatedRatio      float64 `json:"repeatedRatio,omitempty"`
	EncodeBudgetRatio  float64 `json:"encodeBudgetRatio,omitempty"`
	AdaptiveState      string  `json:"adaptiveState,omitempty"`
	AdaptiveReason     string  `json:"adaptiveReason,omitempty"`
	AdaptivePolicy     string  `json:"adaptivePolicy,omitempty"`
	AdaptiveLevel      int     `json:"adaptiveLevel,omitempty"`
	AdaptiveLevels     int     `json:"adaptiveLevels,omitempty"`
	WeakViewers        int     `json:"weakViewers,omitempty"`
	ActiveLayers       int     `json:"activeLayers,omitempty"`
	ConstrainedViewers int     `json:"constrainedViewers,omitempty"`
	LayerSummary       string  `json:"layerSummary,omitempty"`
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

type GameCastShareRuntimeEvent struct {
	Kind      string `json:"kind"`
	RoomID    string `json:"roomId"`
	PeerID    string `json:"peerId"`
	AudioMode string `json:"audioMode,omitempty"`
	Message   string `json:"message"`
}
