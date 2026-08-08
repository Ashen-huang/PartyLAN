package screegop2p

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"PartyLAN/internal/gamecast"
)

const (
	MessageRoom          = "room"
	MessageError         = "Error"
	MessageCreate        = "create"
	MessageJoin          = "join"
	MessageName          = "name"
	MessageShare         = "share"
	MessageStopShare     = "stopshare"
	MessageHostSession   = "hostsession"
	MessageClientSession = "clientsession"
	MessageHostOffer     = "hostoffer"
	MessageClientAnswer  = "clientanswer"
	MessageHostICE       = "hostice"
	MessageClientICE     = "clientice"
	MessageICERestart    = "icerestartrequest"
	MessageEndShare      = "endshare"
	RoomModeTURN         = "turn"
	RoomModeSTUN         = "stun"
	RoomModeLocal        = "local"
	ErrorAuthRequired    = "screego_auth_required"
	ErrorRoomNotFound    = "screego_room_not_found"
	ErrorProtocol        = "screego_protocol_error"
	ErrorUnavailable     = "screego_unavailable"
	ErrorSessionFailed   = "screego_session_failed"
)

type ProtocolError struct {
	Code   string
	detail string
}

func (e *ProtocolError) Error() string {
	if e == nil || strings.TrimSpace(e.Code) == "" {
		return ErrorProtocol
	}
	return e.Code
}

func (e *ProtocolError) Detail() string {
	if e == nil {
		return ""
	}
	return e.detail
}

type OutgoingMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type IncomingMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type CreatePayload struct {
	ID                string `json:"id"`
	Mode              string `json:"mode"`
	CloseOnOwnerLeave bool   `json:"closeOnOwnerLeave"`
	Username          string `json:"username,omitempty"`
	JoinIfExist       bool   `json:"joinIfExist,omitempty"`
}

type JoinPayload struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
}

type RoomPayload struct {
	ID    string     `json:"id"`
	Mode  string     `json:"mode,omitempty"`
	Users []RoomUser `json:"users,omitempty"`
}

type RoomUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Streaming bool   `json:"streaming"`
	You       bool   `json:"you"`
	Owner     bool   `json:"owner"`
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Credential string   `json:"credential,omitempty"`
	Username   string   `json:"username,omitempty"`
}

type SessionPayload struct {
	ID         string      `json:"id"`
	Peer       string      `json:"peer"`
	ICEServers []ICEServer `json:"iceServers"`
}

type P2PPayload struct {
	SID   string          `json:"sid"`
	Value json.RawMessage `json:"value"`
}

type ICERecoveryPayload struct {
	SID    string `json:"sid"`
	Action string `json:"action"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

func StreamURL(baseURL string) (string, error) {
	normalized, err := gamecast.NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported websocket scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/stream"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func DecodeIncoming(raw []byte) (IncomingMessage, error) {
	var message IncomingMessage
	if !json.Valid(raw) {
		return message, &ProtocolError{Code: ErrorProtocol, detail: "invalid JSON"}
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return message, &ProtocolError{Code: ErrorProtocol, detail: err.Error()}
	}
	message.Type = strings.TrimSpace(message.Type)
	if message.Type == "" || len(message.Payload) == 0 {
		return IncomingMessage{}, &ProtocolError{Code: ErrorProtocol, detail: "message is incomplete"}
	}
	return message, nil
}

func (m IncomingMessage) Room() (RoomPayload, error) {
	var payload RoomPayload
	if m.Type != MessageRoom {
		return payload, &ProtocolError{Code: ErrorProtocol, detail: "message is not a room"}
	}
	if err := json.Unmarshal(m.Payload, &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		return RoomPayload{}, &ProtocolError{Code: ErrorProtocol, detail: "invalid room payload"}
	}
	return payload, nil
}

func (m IncomingMessage) Session() (SessionPayload, error) {
	var payload SessionPayload
	if m.Type != MessageHostSession && m.Type != MessageClientSession {
		return payload, &ProtocolError{Code: ErrorProtocol, detail: "message is not a session"}
	}
	if err := json.Unmarshal(m.Payload, &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		return SessionPayload{}, &ProtocolError{Code: ErrorProtocol, detail: "invalid session payload"}
	}
	return payload, nil
}

func (m IncomingMessage) P2P() (P2PPayload, error) {
	var payload P2PPayload
	switch m.Type {
	case MessageHostOffer, MessageClientAnswer, MessageHostICE, MessageClientICE:
	default:
		return payload, &ProtocolError{Code: ErrorProtocol, detail: "message is not P2P signaling"}
	}
	if err := json.Unmarshal(m.Payload, &payload); err != nil || strings.TrimSpace(payload.SID) == "" || len(payload.Value) == 0 {
		return P2PPayload{}, &ProtocolError{Code: ErrorProtocol, detail: "invalid P2P payload"}
	}
	return payload, nil
}

func (m IncomingMessage) ServerError() *ProtocolError {
	if m.Type != MessageError {
		return nil
	}
	var payload ErrorPayload
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		return &ProtocolError{Code: ErrorProtocol, detail: "invalid error payload"}
	}
	return NormalizeServerError(payload.Message)
}

func NormalizeServerError(message string) *ProtocolError {
	detail := strings.TrimSpace(message)
	normalized := strings.ToLower(detail)
	code := ErrorProtocol
	switch {
	case strings.Contains(normalized, "login") || strings.Contains(normalized, "auth"):
		code = ErrorAuthRequired
	case strings.Contains(normalized, "room") && (strings.Contains(normalized, "does not exist") || strings.Contains(normalized, "not exist") || strings.Contains(normalized, "not found")):
		code = ErrorRoomNotFound
	}
	return &ProtocolError{Code: code, detail: detail}
}
