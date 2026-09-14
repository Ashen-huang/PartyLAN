package screegop2p

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	gamecastws "PartyLAN/internal/gamecast/ws"
)

type ViewerConfig struct {
	Signaler Signaler
	OnSignal func(json.RawMessage)
	OnStatus func(gamecastws.Status)
}

type Viewer struct {
	config ViewerConfig

	mu      sync.Mutex
	session SessionPayload
}

type frontendSignal struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"sessionId,omitempty"`
	SDP        json.RawMessage `json:"sdp,omitempty"`
	ICE        json.RawMessage `json:"ice,omitempty"`
	ICEServers []ICEServer     `json:"iceServers,omitempty"`
}

func NewViewer(config ViewerConfig) *Viewer {
	return &Viewer{config: config}
}

func (v *Viewer) HandleMessage(message IncomingMessage) error {
	switch message.Type {
	case MessageClientSession:
		session, err := message.Session()
		if err != nil {
			return err
		}
		v.mu.Lock()
		previousID := strings.TrimSpace(v.session.ID)
		v.session = session
		v.mu.Unlock()
		if previousID != "" && previousID != session.ID {
			v.emit(frontendSignal{Type: MessageEndShare, SessionID: previousID})
		}
		v.emitStatus(gamecastws.Status{State: "connecting", Detail: "native P2P viewer session"})
	case MessageHostOffer, MessageHostICE:
		signal, err := message.P2P()
		if err != nil {
			return err
		}
		v.mu.Lock()
		session := v.session
		v.mu.Unlock()
		if session.ID == "" || signal.SID != session.ID {
			return nil
		}
		if message.Type == MessageHostOffer {
			v.emit(frontendSignal{
				Type: MessageHostOfferFrontend, SessionID: session.ID,
				SDP: cloneRaw(signal.Value), ICEServers: cloneICEServers(session.ICEServers),
			})
			return nil
		}
		v.emit(frontendSignal{
			Type: MessageICECandidateFrontend, SessionID: session.ID, ICE: cloneRaw(signal.Value),
		})
	case MessageEndShare:
		var sessionID string
		if err := json.Unmarshal(message.Payload, &sessionID); err != nil {
			return &ProtocolError{Code: ErrorProtocol, detail: "invalid endshare payload"}
		}
		sessionID = strings.TrimSpace(sessionID)
		v.mu.Lock()
		if sessionID == "" || sessionID != v.session.ID {
			v.mu.Unlock()
			return nil
		}
		v.session = SessionPayload{}
		v.mu.Unlock()
		v.emit(frontendSignal{Type: MessageEndShare, SessionID: sessionID})
		v.emitStatus(gamecastws.Status{State: "closed", Detail: "native P2P viewer session ended"})
	}
	return nil
}

const (
	MessageHostOfferFrontend    = "offer"
	MessageICECandidateFrontend = "ice-candidate"
	MessageClientAnswerFrontend = "answer"
)

func (v *Viewer) SendFrontendSignal(ctx context.Context, raw []byte) error {
	if v.config.Signaler == nil {
		return fmt.Errorf("Screego P2P viewer requires a signaler")
	}
	var signal frontendSignal
	if !json.Valid(raw) || json.Unmarshal(raw, &signal) != nil {
		return &ProtocolError{Code: ErrorProtocol, detail: "invalid frontend signal"}
	}
	signal.Type = strings.TrimSpace(signal.Type)
	signal.SessionID = strings.TrimSpace(signal.SessionID)
	v.mu.Lock()
	currentID := strings.TrimSpace(v.session.ID)
	v.mu.Unlock()
	if currentID == "" || signal.SessionID == "" || signal.SessionID != currentID {
		return &ProtocolError{Code: ErrorSessionFailed, detail: "frontend signal session is not current"}
	}

	switch signal.Type {
	case MessageClientAnswerFrontend:
		description, err := normalizeAnswer(signal.SDP)
		if err != nil {
			return err
		}
		return v.config.Signaler.Send(ctx, OutgoingMessage{
			Type:    MessageClientAnswer,
			Payload: P2PPayload{SID: currentID, Value: description},
		})
	case MessageICECandidateFrontend:
		if len(signal.ICE) == 0 || string(signal.ICE) == "null" {
			return &ProtocolError{Code: ErrorProtocol, detail: "frontend ICE candidate is missing"}
		}
		return v.config.Signaler.Send(ctx, OutgoingMessage{
			Type:    MessageClientICE,
			Payload: P2PPayload{SID: currentID, Value: cloneRaw(signal.ICE)},
		})
	default:
		return &ProtocolError{Code: ErrorProtocol, detail: "unsupported frontend signal type"}
	}
}

func (v *Viewer) CurrentSessionID() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.session.ID
}

func (v *Viewer) Close() {
	v.mu.Lock()
	sessionID := strings.TrimSpace(v.session.ID)
	v.session = SessionPayload{}
	v.mu.Unlock()
	if sessionID != "" {
		v.emit(frontendSignal{Type: MessageEndShare, SessionID: sessionID})
	}
}

func (v *Viewer) emit(signal frontendSignal) {
	if v.config.OnSignal == nil {
		return
	}
	raw, err := json.Marshal(signal)
	if err != nil {
		v.emitStatus(gamecastws.Status{State: "error", Detail: ErrorProtocol})
		return
	}
	v.config.OnSignal(raw)
}

func (v *Viewer) emitStatus(status gamecastws.Status) {
	if v.config.OnStatus != nil {
		v.config.OnStatus(status)
	}
}

func normalizeAnswer(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &ProtocolError{Code: ErrorProtocol, detail: "frontend answer is missing"}
	}
	var sdp string
	if err := json.Unmarshal(raw, &sdp); err == nil {
		if strings.TrimSpace(sdp) == "" {
			return nil, &ProtocolError{Code: ErrorProtocol, detail: "frontend answer SDP is empty"}
		}
		description, marshalErr := json.Marshal(struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}{Type: MessageClientAnswerFrontend, SDP: sdp})
		if marshalErr != nil {
			return nil, &ProtocolError{Code: ErrorProtocol, detail: marshalErr.Error()}
		}
		return description, nil
	}
	var description struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if err := json.Unmarshal(raw, &description); err != nil || strings.TrimSpace(description.SDP) == "" {
		return nil, &ProtocolError{Code: ErrorProtocol, detail: "invalid frontend answer"}
	}
	if strings.TrimSpace(description.Type) == "" {
		description.Type = MessageClientAnswerFrontend
	}
	normalized, err := json.Marshal(description)
	if err != nil {
		return nil, &ProtocolError{Code: ErrorProtocol, detail: err.Error()}
	}
	return normalized, nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func cloneICEServers(servers []ICEServer) []ICEServer {
	cloned := make([]ICEServer, len(servers))
	for index, server := range servers {
		cloned[index] = server
		cloned[index].URLs = append([]string(nil), server.URLs...)
	}
	return cloned
}
