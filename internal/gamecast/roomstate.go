package gamecast

import "time"

const (
	MediaProtocolVersion = 1
	MediaModeSFU         = "sfu"
	MediaModeLegacyP2P   = "legacy-p2p"
	SharePhaseIdle       = "idle"
	SharePhaseStarting   = "starting"
	SharePhaseLive       = "live"
	SharePhaseStopping   = "stopping"
)

type MediaCapabilities struct {
	SFUViewer bool `json:"sfuViewer"`
	SFUHost   bool `json:"sfuHost"`
	H264      bool `json:"h264"`
	Opus      bool `json:"opus"`
}

type RoomState struct {
	Room                    string    `json:"room"`
	Active                  bool      `json:"active"`
	Origin                  string    `json:"origin,omitempty"`
	OwnerParticipantID      string    `json:"ownerParticipantId,omitempty"`
	OwnerName               string    `json:"ownerName,omitempty"`
	Sharing                 bool      `json:"sharing"`
	MediaMode               string    `json:"mediaMode,omitempty"`
	MediaProtocolVersion    int       `json:"mediaProtocolVersion,omitempty"`
	SharePhase              string    `json:"sharePhase,omitempty"`
	ShareOwnerParticipantID string    `json:"shareOwnerParticipantId,omitempty"`
	ShareOwnerName          string    `json:"shareOwnerName,omitempty"`
	ShareOrigin             string    `json:"shareOrigin,omitempty"`
	SFU                     bool      `json:"sfu"`
	AudioMode               string    `json:"audioMode,omitempty"`
	User                    string    `json:"user,omitempty"`
	TTL                     int       `json:"ttl,omitempty"`
	ParticipantCount        int       `json:"participantCount,omitempty"`
	UpdatedAt               time.Time `json:"updatedAt,omitempty"`
	UpdatedAtUnix           float64   `json:"updatedAtUnix,omitempty"`
}

type PresenceRequest struct {
	Room                 string            `json:"room"`
	ParticipantID        string            `json:"participantId"`
	ClientType           string            `json:"clientType,omitempty"`
	Origin               string            `json:"origin,omitempty"`
	Role                 string            `json:"role,omitempty"`
	Name                 string            `json:"name,omitempty"`
	MediaProtocolVersion int               `json:"mediaProtocolVersion,omitempty"`
	Capabilities         MediaCapabilities `json:"capabilities,omitempty"`
	TTL                  int               `json:"ttl,omitempty"`
}

type PresenceParticipant struct {
	ParticipantID        string            `json:"participantId"`
	Room                 string            `json:"room"`
	Name                 string            `json:"name,omitempty"`
	ClientType           string            `json:"clientType"`
	Role                 string            `json:"role"`
	Active               bool              `json:"active"`
	Sharing              bool              `json:"sharing"`
	MediaProtocolVersion int               `json:"mediaProtocolVersion,omitempty"`
	Capabilities         MediaCapabilities `json:"capabilities,omitempty"`
	TTL                  int               `json:"ttl,omitempty"`
	AgeSeconds           float64           `json:"ageSeconds,omitempty"`
}

type PresenceResponse struct {
	OK           bool                  `json:"ok"`
	Count        int                   `json:"count,omitempty"`
	Participant  *PresenceParticipant  `json:"participant,omitempty"`
	Participants []PresenceParticipant `json:"participants,omitempty"`
	Room         *RoomState            `json:"room,omitempty"`
	Code         string                `json:"code,omitempty"`
	Error        string                `json:"error,omitempty"`
}

type VoiceTokenRequest struct {
	Room          string `json:"room"`
	ParticipantID string `json:"participantId"`
}

type VoiceTokenResponse struct {
	OK            bool   `json:"ok"`
	Token         string `json:"token,omitempty"`
	ExpiresAtUnix int64  `json:"expiresAtUnix,omitempty"`
	Code          string `json:"code,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ShareLeaseRequest struct {
	Room          string `json:"room"`
	ParticipantID string `json:"participantId"`
	LeaseID       string `json:"leaseId,omitempty"`
	MediaMode     string `json:"mediaMode,omitempty"`
	Phase         string `json:"phase,omitempty"`
	SFU           bool   `json:"sfu"`
	AudioMode     string `json:"audioMode,omitempty"`
	TTL           int    `json:"ttl,omitempty"`
}

type ShareLease struct {
	Room          string  `json:"room"`
	LeaseID       string  `json:"leaseId"`
	ParticipantID string  `json:"participantId"`
	ClientType    string  `json:"clientType,omitempty"`
	Name          string  `json:"name,omitempty"`
	Active        bool    `json:"active"`
	MediaMode     string  `json:"mediaMode,omitempty"`
	Phase         string  `json:"phase,omitempty"`
	SFU           bool    `json:"sfu"`
	AudioMode     string  `json:"audioMode,omitempty"`
	TTL           int     `json:"ttl,omitempty"`
	ExpiresAtUnix float64 `json:"expiresAtUnix,omitempty"`
	AgeSeconds    float64 `json:"ageSeconds,omitempty"`
}

type ShareLeaseResponse struct {
	OK    bool        `json:"ok"`
	Lease *ShareLease `json:"lease,omitempty"`
	Room  *RoomState  `json:"room,omitempty"`
	Code  string      `json:"code,omitempty"`
	Error string      `json:"error,omitempty"`
}

type RoomStateResponse struct {
	OK    bool       `json:"ok"`
	State *RoomState `json:"state"`
	Error string     `json:"error,omitempty"`
}

type RoomsResponse struct {
	OK    bool        `json:"ok"`
	Count int         `json:"count"`
	Rooms []RoomState `json:"rooms"`
	Error string      `json:"error,omitempty"`
}

type ViewerIntent struct {
	Room     string `json:"room"`
	Active   bool   `json:"active"`
	WantsSFU bool   `json:"wantsSfu"`
	Client   string `json:"client,omitempty"`
	User     string `json:"user,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

type ViewerIntentResponse struct {
	OK     bool          `json:"ok"`
	Intent *ViewerIntent `json:"intent,omitempty"`
	Error  string        `json:"error,omitempty"`
}

type QualityReport struct {
	Room                 string  `json:"room"`
	ParticipantID        string  `json:"participantId"`
	Generation           string  `json:"generation"`
	PairID               string  `json:"pairId"`
	PacketsReceivedDelta int64   `json:"packetsReceivedDelta"`
	PacketsLostDelta     int64   `json:"packetsLostDelta"`
	FramesDroppedDelta   int64   `json:"framesDroppedDelta"`
	DecodedFPS           float64 `json:"decodedFps"`
	IncomingBitrateKbps  float64 `json:"incomingBitrateKbps"`
	JitterMS             float64 `json:"jitterMs"`
}

type QualitySample struct {
	QualityReport
	ReportedAtUnix float64 `json:"reportedAtUnix"`
	AgeSeconds     float64 `json:"ageSeconds"`
}

type QualitySnapshot struct {
	Room                string          `json:"room"`
	Count               int             `json:"count"`
	SafeVideoEgressKbps int             `json:"safeVideoEgressKbps"`
	Samples             []QualitySample `json:"samples"`
}

type QualitySnapshotResponse struct {
	OK bool `json:"ok"`
	QualitySnapshot
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}
