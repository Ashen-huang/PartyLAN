package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

const maxClientSignalBytes = 64 * 1024

type clientSignal struct {
	Type             string                   `json:"type"`
	Revision         uint64                   `json:"revision,omitempty"`
	SDP              string                   `json:"sdp,omitempty"`
	Candidate        *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Token            string                   `json:"token,omitempty"`
	State            string                   `json:"state,omitempty"`
	ParticipantCount int                      `json:"participantCount,omitempty"`
	Code             string                   `json:"code,omitempty"`
}

type PionConnection struct {
	profile gamecast.Profile

	mu               sync.Mutex
	ws               *websocket.Conn
	pc               *webrtc.PeerConnection
	outbound         *webrtc.TrackLocalStaticSample
	outboundAttached bool
	pendingICE       []webrtc.ICECandidateInit
	state            ConnectionState
	cancel           context.CancelFunc
	writeMu          sync.Mutex
	negotiationMu    sync.Mutex
	closeOnce        sync.Once
	revision         atomic.Uint64
	events           chan ConnectionEvent
	remotePackets    chan RemotePacket
	done             chan struct{}
}

func NewPionConnection(profile gamecast.Profile) Connection {
	return &PionConnection{
		profile:       profile,
		state:         ConnectionConnecting,
		events:        make(chan ConnectionEvent, 16),
		remotePackets: make(chan RemotePacket, 128),
		done:          make(chan struct{}),
	}
}

func voiceWebSocketURL(profile gamecast.Profile) (string, error) {
	endpoint, err := gamecast.URLForEndpoint(profile.BaseURL, "/voice-ws")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported voice websocket scheme %q", parsed.Scheme)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (p *PionConnection) Connect(parent context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return &ConnectError{Code: "voice_unauthorized"}
	}
	wsURL, err := voiceWebSocketURL(p.profile)
	if err != nil {
		return err
	}
	tlsConfig, err := gamecast.TLSConfig(p.profile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second, TLSClientConfig: tlsConfig}
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	connection, response, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return &ConnectError{Code: "voice_unauthorized", Err: err}
			}
		}
		return fmt.Errorf("connect voice websocket: %w", err)
	}

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		_ = connection.Close()
		return err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: gamecast.STUNURLs(p.profile.BaseURL)}},
	})
	if err != nil {
		_ = connection.Close()
		return fmt.Errorf("create voice peer connection: %w", err)
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  2,
	}, "microphone", fmt.Sprintf("voice-client-%d", time.Now().UnixNano()))
	if err != nil {
		_ = pc.Close()
		_ = connection.Close()
		return err
	}
	runCtx, runCancel := context.WithCancel(parent)
	p.mu.Lock()
	if p.ws != nil || p.pc != nil {
		p.mu.Unlock()
		runCancel()
		_ = pc.Close()
		_ = connection.Close()
		return errors.New("voice connection already started")
	}
	p.ws = connection
	p.pc = pc
	p.outbound = track
	p.cancel = runCancel
	p.mu.Unlock()

	pc.OnICECandidate(p.onICECandidate)
	pc.OnTrack(p.onTrack)
	pc.OnConnectionStateChange(p.onConnectionStateChange)
	connection.SetReadLimit(maxClientSignalBytes)
	go p.readLoop(connection)
	go func() {
		<-runCtx.Done()
		p.Close()
	}()
	return nil
}

func (p *PionConnection) Events() <-chan ConnectionEvent     { return p.events }
func (p *PionConnection) RemotePackets() <-chan RemotePacket { return p.remotePackets }
func (p *PionConnection) Done() <-chan struct{}              { return p.done }

func (p *PionConnection) WriteOpus(packet []byte, duration time.Duration) error {
	if len(packet) == 0 {
		return nil
	}
	p.mu.Lock()
	track := p.outbound
	p.mu.Unlock()
	if track == nil {
		return errors.New("voice microphone track is closed")
	}
	return track.WriteSample(pionmedia.Sample{Data: append([]byte(nil), packet...), Duration: duration})
}

func (p *PionConnection) RefreshAuth(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return &ConnectError{Code: "voice_unauthorized"}
	}
	return p.send(ctx, clientSignal{Type: "auth-refresh", Token: token})
}

func (p *PionConnection) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.mu.Lock()
		cancel := p.cancel
		pc := p.pc
		connection := p.ws
		p.cancel = nil
		p.pc = nil
		p.ws = nil
		p.outbound = nil
		p.pendingICE = nil
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if pc != nil {
			_ = pc.Close()
		}
		if connection != nil {
			_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closed"), time.Now().Add(time.Second))
			_ = connection.Close()
		}
	})
}

func (p *PionConnection) readLoop(connection *websocket.Conn) {
	defer p.Close()
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var signal clientSignal
		if err := json.Unmarshal(payload, &signal); err != nil {
			p.emit(ConnectionEvent{State: ConnectionError, ErrorCode: "voice_signal_invalid"})
			continue
		}
		if err := p.handleSignal(signal); err != nil {
			p.emit(ConnectionEvent{State: ConnectionError, ErrorCode: "voice_signal_failed", Err: err})
			return
		}
	}
}

func (p *PionConnection) handleSignal(signal clientSignal) error {
	switch signal.Type {
	case "offer":
		return p.answerOffer(signal)
	case "ice-candidate":
		return p.addRemoteCandidate(signal)
	case "state":
		p.mu.Lock()
		state := p.state
		p.mu.Unlock()
		p.emit(ConnectionEvent{State: state, ParticipantCount: signal.ParticipantCount})
		return nil
	case "error":
		if signal.Code == "voice_stale_revision" {
			return nil
		}
		event := ConnectionEvent{State: ConnectionError, ErrorCode: signal.Code}
		switch signal.Code {
		case "voice_room_full":
			event.State = ConnectionFull
		case "voice_unauthorized", "voice_token_expired":
			event.State = ConnectionUnauthorized
		}
		p.emit(event)
		return nil
	default:
		return nil
	}
}

func (p *PionConnection) answerOffer(signal clientSignal) error {
	p.negotiationMu.Lock()
	defer p.negotiationMu.Unlock()
	if signal.Revision == 0 || signal.Revision <= p.revision.Load() || strings.TrimSpace(signal.SDP) == "" {
		return nil
	}
	p.mu.Lock()
	pc := p.pc
	track := p.outbound
	p.mu.Unlock()
	if pc == nil || track == nil {
		return errors.New("voice peer connection is closed")
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: signal.SDP}); err != nil {
		return err
	}
	p.mu.Lock()
	attached := p.outboundAttached
	p.mu.Unlock()
	if !attached {
		sender, err := attachMicrophoneTrack(pc, track)
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.outboundAttached = true
		p.mu.Unlock()
		go readVoiceSenderRTCP(sender)
	}
	p.mu.Lock()
	pending := append([]webrtc.ICECandidateInit(nil), p.pendingICE...)
	p.pendingICE = nil
	p.mu.Unlock()
	for _, candidate := range pending {
		if err := pc.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	p.revision.Store(signal.Revision)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	return p.send(context.Background(), clientSignal{Type: "answer", Revision: signal.Revision, SDP: answer.SDP})
}

func attachMicrophoneTrack(pc *webrtc.PeerConnection, track webrtc.TrackLocal) (*webrtc.RTPSender, error) {
	transceiverCount := len(pc.GetTransceivers())
	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, fmt.Errorf("attach microphone track: %w", err)
	}
	if len(pc.GetTransceivers()) != transceiverCount {
		_ = pc.RemoveTrack(sender)
		return nil, errors.New("voice offer did not reserve a microphone sender")
	}
	return sender, nil
}

func (p *PionConnection) addRemoteCandidate(signal clientSignal) error {
	if signal.Candidate == nil || signal.Revision < p.revision.Load() {
		return nil
	}
	p.mu.Lock()
	pc := p.pc
	if pc == nil {
		p.mu.Unlock()
		return nil
	}
	if pc.RemoteDescription() == nil {
		p.pendingICE = append(p.pendingICE, *signal.Candidate)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	return pc.AddICECandidate(*signal.Candidate)
}

func (p *PionConnection) onICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}
	value := candidate.ToJSON()
	_ = p.send(context.Background(), clientSignal{
		Type:      "ice-candidate",
		Revision:  p.revision.Load(),
		Candidate: &value,
	})
}

func (p *PionConnection) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	if track.Kind() != webrtc.RTPCodecTypeAudio || !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeOpus) {
		return
	}
	trackID := track.StreamID() + "/" + track.ID()
	go func() {
		defer p.emitRemote(RemotePacket{TrackID: trackID, End: true})
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			p.emitRemote(RemotePacket{
				TrackID:        trackID,
				SequenceNumber: packet.SequenceNumber,
				Payload:        append([]byte(nil), packet.Payload...),
			})
		}
	}()
}

func (p *PionConnection) onConnectionStateChange(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		p.mu.Lock()
		p.state = ConnectionConnected
		p.mu.Unlock()
		p.emit(ConnectionEvent{State: ConnectionConnected})
	case webrtc.PeerConnectionStateFailed:
		p.emit(ConnectionEvent{State: ConnectionError, ErrorCode: "voice_peer_failed"})
		go p.Close()
	}
}

func (p *PionConnection) send(ctx context.Context, signal clientSignal) error {
	p.mu.Lock()
	connection := p.ws
	p.mu.Unlock()
	if connection == nil {
		return errors.New("voice websocket is closed")
	}
	done := make(chan error, 1)
	go func() {
		p.writeMu.Lock()
		defer p.writeMu.Unlock()
		_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
		done <- connection.WriteJSON(signal)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("voice connection is closed")
	case err := <-done:
		return err
	}
}

func (p *PionConnection) emit(event ConnectionEvent) {
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case p.events <- event:
	default:
	}
}

func (p *PionConnection) emitRemote(packet RemotePacket) {
	select {
	case <-p.done:
		return
	case p.remotePackets <- packet:
	default:
	}
}

func readVoiceSenderRTCP(sender *webrtc.RTPSender) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
	}
}

var _ Connection = (*PionConnection)(nil)
