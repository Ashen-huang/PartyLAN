package sfu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	voicesfuauth "PartyLAN/server/voice-sfu/internal/auth"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

const maxSignalBytes = 64 * 1024

type PeerConnection interface {
	AddTransceiverFromKind(webrtc.RTPCodecType, ...webrtc.RTPTransceiverInit) (*webrtc.RTPTransceiver, error)
	AddTrack(webrtc.TrackLocal) (*webrtc.RTPSender, error)
	RemoveTrack(*webrtc.RTPSender) error
	CreateOffer(*webrtc.OfferOptions) (webrtc.SessionDescription, error)
	SetLocalDescription(webrtc.SessionDescription) error
	SetRemoteDescription(webrtc.SessionDescription) error
	AddICECandidate(webrtc.ICECandidateInit) error
	OnICECandidate(func(*webrtc.ICECandidate))
	OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver))
	OnConnectionStateChange(func(webrtc.PeerConnectionState))
	Close() error
}

type PeerConnectionFactory func() (PeerConnection, error)

type Config struct {
	Version         string
	Secret          []byte
	MaxParticipants int
	API             *webrtc.API
	ICEServers      []webrtc.ICEServer
	Now             func() time.Time
	PeerFactory     PeerConnectionFactory
}

type Server struct {
	config      Config
	registry    *Registry
	verifier    voicesfuauth.Verifier
	peerFactory PeerConnectionFactory
	handler     http.Handler
	upgrader    websocket.Upgrader
	relayMu     sync.Mutex
	relays      map[string]map[string]*sourceRelay
	nextKey     atomic.Uint64
}

func NewServer(config Config) (*Server, error) {
	if len(config.Secret) == 0 {
		return nil, errors.New("voice secret is required")
	}
	if config.Version == "" {
		config.Version = "dev"
	}
	if config.API == nil {
		config.API = webrtc.NewAPI()
	}
	peerFactory := config.PeerFactory
	if peerFactory == nil {
		peerFactory = func() (PeerConnection, error) {
			return config.API.NewPeerConnection(webrtc.Configuration{ICEServers: config.ICEServers})
		}
	}
	server := &Server{
		config:      config,
		registry:    NewRegistry(config.MaxParticipants),
		verifier:    voicesfuauth.Verifier{Secret: config.Secret, Audience: "voice-sfu", Now: config.Now},
		peerFactory: peerFactory,
		relays:      make(map[string]map[string]*sourceRelay),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/ws", server.handleWebSocket)
	server.handler = mux
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) now() time.Time {
	if s.config.Now != nil {
		return s.config.Now()
	}
	return time.Now()
}

func (s *Server) handleHealth(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	rooms, connections := s.registry.Counts()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.config.Version,
		"roomCount":       rooms,
		"connectionCount": connections,
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		writeHTTPError(w, http.StatusUnauthorized, "voice_unauthorized")
		return
	}
	claims, err := s.verifier.Verify(token)
	if err != nil {
		writeHTTPError(w, http.StatusUnauthorized, tokenErrorCode(err))
		return
	}
	connection, err := s.upgrader.Upgrade(w, request, nil)
	if err != nil {
		return
	}

	var session *peerSession
	member, err := s.registry.Admit(claims.Room, claims.ParticipantID, func() (Member, error) {
		var createErr error
		session, createErr = newPeerSession(s, connection, claims)
		if createErr != nil {
			return nil, createErr
		}
		return session, nil
	})
	if err != nil {
		code := "voice_connection_failed"
		if errors.Is(err, ErrRoomFull) {
			code = "voice_room_full"
		}
		_ = connection.WriteJSON(wireSignal{Type: "error", Code: code})
		_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code), time.Now().Add(time.Second))
		_ = connection.Close()
		return
	}
	session = member.(*peerSession)
	defer session.Close("voice_disconnected")

	s.attachExistingRelays(session)
	if err := session.start(); err != nil {
		session.Close("voice_negotiation_failed")
		return
	}
	s.broadcastState(claims.Room)
	session.run()
}

func bearerToken(header string) (string, bool) {
	if !strings.HasPrefix(header, "Bearer ") || strings.Count(header, " ") != 1 {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return token, token != ""
}

func tokenErrorCode(err error) string {
	if errors.Is(err, voicesfuauth.ErrExpiredToken) {
		return "voice_token_expired"
	}
	return "voice_unauthorized"
}

func writeHTTPError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) broadcastState(room string) {
	members := s.registry.Members(room)
	count := len(members)
	for _, member := range members {
		if session, ok := member.(*peerSession); ok {
			_ = session.send(wireSignal{Type: "state", State: "connected", ParticipantCount: count})
		}
	}
}

func privateID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func trackID(participantID string) string {
	return fmt.Sprintf("voice-%s", privateID(participantID))
}
