package sfu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	voicesfuauth "PartyLAN/server/voice-sfu/internal/auth"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type wireSignal struct {
	Type             string                   `json:"type"`
	Revision         uint64                   `json:"revision,omitempty"`
	SDP              string                   `json:"sdp,omitempty"`
	Candidate        *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Token            string                   `json:"token,omitempty"`
	State            string                   `json:"state,omitempty"`
	ParticipantCount int                      `json:"participantCount,omitempty"`
	Code             string                   `json:"code,omitempty"`
}

type peerSession struct {
	server        *Server
	connection    *websocket.Conn
	pc            PeerConnection
	room          string
	participantID string
	relayKey      string
	gate          NegotiationGate
	writeMu       sync.Mutex
	signalMu      sync.Mutex
	closeOnce     sync.Once
	closed        chan struct{}
	authUpdated   chan struct{}
	expiresAt     atomic.Int64
}

func newPeerSession(server *Server, connection *websocket.Conn, claims voicesfuauth.Claims) (*peerSession, error) {
	pc, err := server.peerFactory()
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("add microphone transceiver: %w", err)
	}
	key := server.nextKey.Add(1)
	session := &peerSession{
		server:        server,
		connection:    connection,
		pc:            pc,
		room:          claims.Room,
		participantID: claims.ParticipantID,
		relayKey:      fmt.Sprintf("%s-%d", privateID(claims.ParticipantID), key),
		closed:        make(chan struct{}),
		authUpdated:   make(chan struct{}, 1),
	}
	session.expiresAt.Store(claims.ExpiresAt)
	pc.OnICECandidate(session.onICECandidate)
	pc.OnTrack(session.onTrack)
	pc.OnConnectionStateChange(session.onConnectionStateChange)
	return session, nil
}

func (p *peerSession) start() error {
	if err := p.requestOffer(); err != nil {
		return err
	}
	go p.watchExpiry()
	return nil
}

func (p *peerSession) run() {
	p.connection.SetReadLimit(maxSignalBytes)
	for {
		messageType, payload, err := p.connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			_ = p.send(wireSignal{Type: "error", Code: "voice_signal_invalid"})
			continue
		}
		var signal wireSignal
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&signal); err != nil {
			_ = p.send(wireSignal{Type: "error", Code: "voice_signal_invalid"})
			continue
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			_ = p.send(wireSignal{Type: "error", Code: "voice_signal_invalid"})
			continue
		}
		p.handleSignal(signal)
	}
}

func (p *peerSession) handleSignal(signal wireSignal) {
	switch signal.Type {
	case "answer":
		if !p.gate.AcceptsAnswer(signal.Revision) {
			_ = p.send(wireSignal{Type: "error", Code: "voice_stale_revision"})
			return
		}
		answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: signal.SDP}
		if strings.TrimSpace(signal.SDP) == "" || p.pc.SetRemoteDescription(answer) != nil {
			_ = p.send(wireSignal{Type: "error", Code: "voice_answer_invalid"})
			return
		}
		nextRevision, start, err := p.gate.Answer(signal.Revision)
		if err != nil {
			_ = p.send(wireSignal{Type: "error", Code: "voice_stale_revision"})
			return
		}
		if start {
			if err := p.sendOffer(nextRevision); err != nil {
				p.Close("voice_negotiation_failed")
			}
		}
	case "ice-candidate":
		if signal.Candidate == nil || !p.gate.AcceptsCandidate(signal.Revision) {
			_ = p.send(wireSignal{Type: "error", Code: "voice_stale_revision"})
			return
		}
		if err := p.pc.AddICECandidate(*signal.Candidate); err != nil {
			_ = p.send(wireSignal{Type: "error", Code: "voice_candidate_invalid"})
		}
	case "auth-refresh":
		claims, err := p.server.verifier.Verify(signal.Token)
		if err != nil || claims.Room != p.room || claims.ParticipantID != p.participantID {
			p.Close("voice_unauthorized")
			return
		}
		p.expiresAt.Store(claims.ExpiresAt)
		select {
		case p.authUpdated <- struct{}{}:
		default:
		}
	default:
		_ = p.send(wireSignal{Type: "error", Code: "voice_signal_type_invalid"})
	}
}

func (p *peerSession) requestOffer() error {
	revision, start := p.gate.Request()
	if !start {
		return nil
	}
	return p.sendOffer(revision)
}

func (p *peerSession) sendOffer(revision uint64) error {
	p.signalMu.Lock()
	defer p.signalMu.Unlock()
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set local offer: %w", err)
	}
	return p.send(wireSignal{Type: "offer", Revision: revision, SDP: offer.SDP})
}

func (p *peerSession) onICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}
	p.signalMu.Lock()
	defer p.signalMu.Unlock()
	revision := p.gate.CurrentRevision()
	value := candidate.ToJSON()
	if err := p.send(wireSignal{Type: "ice-candidate", Revision: revision, Candidate: &value}); err != nil {
		p.Close("voice_signal_failed")
	}
}

func (p *peerSession) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	if track.Kind() != webrtc.RTPCodecTypeAudio || !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeOpus) {
		return
	}
	p.server.publish(p, track)
}

func (p *peerSession) onConnectionStateChange(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		p.Close("voice_peer_failed")
	}
}

func (p *peerSession) watchExpiry() {
	for {
		expiresAt := time.Unix(p.expiresAt.Load(), 0)
		remaining := expiresAt.Sub(p.server.now())
		if remaining <= 0 {
			p.Close("voice_unauthorized")
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
		case <-p.authUpdated:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-p.closed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (p *peerSession) send(signal wireSignal) error {
	select {
	case <-p.closed:
		return netClosedError{}
	default:
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.connection.WriteJSON(signal)
}

func (p *peerSession) Close(code string) {
	p.closeOnce.Do(func() {
		if code != "" && code != "voice_disconnected" {
			_ = p.send(wireSignal{Type: "error", Code: code})
		}
		close(p.closed)
		_ = p.pc.Close()
		_ = p.connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, code), time.Now().Add(time.Second))
		_ = p.connection.Close()
		p.server.registry.Leave(p.room, p.participantID, p)
		p.server.detach(p)
		p.server.broadcastState(p.room)
	})
}

func (p *peerSession) readRTCP(sender *webrtc.RTPSender) {
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()
}

type netClosedError struct{}

func (netClosedError) Error() string { return "voice connection closed" }
