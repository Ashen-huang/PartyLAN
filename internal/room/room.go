package room

type RoomState int

const (
	RoomStateWaiting RoomState = iota
	RoomStateActive
	RoomStateClosed
)

type PeerRole int

const (
	PeerRoleHost PeerRole = iota
	PeerRoleViewer
)

type PeerInfo struct {
	ID                string
	Hostname          string
	Role              PeerRole
	VoiceState        int // VoiceState enum
	IsSharing         bool
	JoinedAtUnix      int64
	VideoBackend      string
	VideoStatus       string
	VideoReason       string
	VideoSupportsHost bool
	VideoSupportsView bool
}

type RoomConfig struct {
	ID       string
	Name     string
	Password string
}

type Room interface {
	Create(config RoomConfig) error
	Join(roomID string, password string) error
	Leave() error
	GetState() RoomState
	GetPeers() []PeerInfo
	StartSharing(width, height, fps int, codec, backend string) error
	StopSharing() error
	NotifyPeerSharing(peerID string, width, height, fps int, codec, backend string) error
	SendMediaSignal(peerID string, payload MediaSignalPayload) error
	OnPeerJoin(fn func(peer PeerInfo))
	OnPeerLeave(fn func(peerID string))
	OnStreamStart(fn func(peerID string, width, height, fps int, codec, backend string))
	OnStreamStop(fn func(peerID string))
	OnMediaSignal(fn func(payload MediaSignalPayload))
}
