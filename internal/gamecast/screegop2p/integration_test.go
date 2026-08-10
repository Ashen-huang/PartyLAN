package screegop2p

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

type integrationRouter struct {
	mu        sync.Mutex
	publisher *Publisher
	viewers   map[string]*Viewer
}

func (r *integrationRouter) Send(_ context.Context, message OutgoingMessage) error {
	if message.Type != MessageHostOffer && message.Type != MessageHostICE {
		return nil
	}
	payload, ok := message.Payload.(P2PPayload)
	if !ok {
		return fmt.Errorf("unexpected host signal payload %T", message.Payload)
	}
	r.mu.Lock()
	viewer := r.viewers[payload.SID]
	r.mu.Unlock()
	if viewer == nil {
		return fmt.Errorf("viewer session %s is missing", payload.SID)
	}
	return viewer.HandleMessage(incomingMessageForIntegration(message.Type, payload))
}

type integrationViewerSignaler struct {
	router *integrationRouter
}

func (s integrationViewerSignaler) Send(_ context.Context, message OutgoingMessage) error {
	s.router.mu.Lock()
	publisher := s.router.publisher
	s.router.mu.Unlock()
	if publisher == nil {
		return fmt.Errorf("publisher is missing")
	}
	return publisher.HandleMessage(incomingMessageForIntegration(message.Type, message.Payload))
}

type browserHarness struct {
	mu            sync.Mutex
	viewer        *Viewer
	pc            *webrtc.PeerConnection
	pendingICE    []webrtc.ICECandidateInit
	connected     chan struct{}
	connectedOnce sync.Once
	videoRTP      chan struct{}
	audioRTP      chan struct{}
}

func newBrowserHarness(t *testing.T, router *integrationRouter) *browserHarness {
	t.Helper()
	harness := &browserHarness{
		connected: make(chan struct{}),
		videoRTP:  make(chan struct{}, 4),
		audioRTP:  make(chan struct{}, 4),
	}
	harness.viewer = NewViewer(ViewerConfig{
		Signaler: integrationViewerSignaler{router: router},
		OnSignal: func(raw json.RawMessage) {
			if err := harness.handleSignal(raw); err != nil {
				t.Errorf("browser signal: %v", err)
			}
		},
	})
	return harness
}

func (h *browserHarness) handleSignal(raw json.RawMessage) error {
	var signal frontendSignal
	if err := json.Unmarshal(raw, &signal); err != nil {
		return err
	}
	switch signal.Type {
	case MessageHostOfferFrontend:
		return h.acceptOffer(signal)
	case MessageICECandidateFrontend:
		return h.acceptICE(signal)
	case MessageEndShare:
		h.close()
	}
	return nil
}

func (h *browserHarness) acceptOffer(signal frontendSignal) error {
	var offer webrtc.SessionDescription
	if err := json.Unmarshal(signal.SDP, &offer); err != nil {
		return err
	}
	configuration := webrtc.Configuration{ICEServers: make([]webrtc.ICEServer, 0, len(signal.ICEServers))}
	for _, server := range signal.ICEServers {
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs: server.URLs, Username: server.Username, Credential: server.Credential,
		})
	}
	pc, err := webrtc.NewPeerConnection(configuration)
	if err != nil {
		return err
	}
	h.mu.Lock()
	old := h.pc
	h.pc = pc
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		rawCandidate, marshalErr := json.Marshal(candidate.ToJSON())
		if marshalErr != nil {
			return
		}
		body, marshalErr := json.Marshal(frontendSignal{
			Type: MessageICECandidateFrontend, SessionID: signal.SessionID, ICE: rawCandidate,
		})
		if marshalErr == nil {
			_ = h.viewer.SendFrontendSignal(context.Background(), body)
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			h.connectedOnce.Do(func() { close(h.connected) })
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			if _, _, readErr := track.ReadRTP(); readErr != nil {
				return
			}
			if track.Kind() == webrtc.RTPCodecTypeVideo {
				h.videoRTP <- struct{}{}
			} else if track.Kind() == webrtc.RTPCodecTypeAudio {
				h.audioRTP <- struct{}{}
			}
		}()
	})
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}
	h.mu.Lock()
	pending := append([]webrtc.ICECandidateInit(nil), h.pendingICE...)
	h.pendingICE = nil
	h.mu.Unlock()
	for _, candidate := range pending {
		if err := pc.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	body, err := json.Marshal(frontendSignal{
		Type: MessageClientAnswerFrontend, SessionID: signal.SessionID, SDP: mustJSON(answer.SDP),
	})
	if err != nil {
		return err
	}
	return h.viewer.SendFrontendSignal(context.Background(), body)
}

func (h *browserHarness) acceptICE(signal frontendSignal) error {
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(signal.ICE, &candidate); err != nil {
		return err
	}
	h.mu.Lock()
	pc := h.pc
	if pc == nil || pc.RemoteDescription() == nil {
		h.pendingICE = append(h.pendingICE, candidate)
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	return pc.AddICECandidate(candidate)
}

func (h *browserHarness) close() {
	h.mu.Lock()
	pc := h.pc
	h.pc = nil
	h.pendingICE = nil
	h.mu.Unlock()
	if pc != nil {
		_ = pc.Close()
	}
}

func incomingMessageForIntegration(messageType string, payload interface{}) IncomingMessage {
	raw, _ := json.Marshal(payload)
	return IncomingMessage{Type: messageType, Payload: raw}
}

func TestNativePublisherNegotiatesVideoAndAudioWithTwoBrowserStyleViewers(t *testing.T) {
	router := &integrationRouter{viewers: make(map[string]*Viewer)}
	publisher := NewPublisher(PublisherConfig{
		Signaler: router, Width: 1280, Height: 720, FPS: 60, AudioEnabled: true,
	})
	router.publisher = publisher
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("start publisher: %v", err)
	}
	defer publisher.Close()

	harnesses := make([]*browserHarness, 0, 2)
	for index := 1; index <= 2; index++ {
		sessionID := fmt.Sprintf("session-%d", index)
		harness := newBrowserHarness(t, router)
		harnesses = append(harnesses, harness)
		router.viewers[sessionID] = harness.viewer
		defer harness.close()
		defer harness.viewer.Close()
		session := SessionPayload{ID: sessionID, Peer: fmt.Sprintf("viewer-%d", index)}
		if err := harness.viewer.HandleMessage(incomingMessageForIntegration(MessageClientSession, session)); err != nil {
			t.Fatalf("client session %d: %v", index, err)
		}
		if err := publisher.HandleMessage(incomingMessageForIntegration(MessageHostSession, session)); err != nil {
			t.Fatalf("host session %d: %v", index, err)
		}
	}

	for index, harness := range harnesses {
		select {
		case <-harness.connected:
		case <-time.After(8 * time.Second):
			t.Fatalf("viewer %d did not connect", index+1)
		}
	}
	if got := publisher.SessionCount(); got != 2 {
		t.Fatalf("publisher sessions=%d want 2", got)
	}

	video := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84}
	audio := []byte{0xf8, 0xff, 0xfe}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := publisher.WriteH264Sample(video, time.Second/60); err != nil {
			t.Fatalf("write H264: %v", err)
		}
		if err := publisher.WriteOpusSample(audio, 20*time.Millisecond); err != nil {
			t.Fatalf("write Opus: %v", err)
		}
		allReceived := true
		for _, harness := range harnesses {
			if len(harness.videoRTP) == 0 || len(harness.audioRTP) == 0 {
				allReceived = false
			}
		}
		if allReceived {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both viewers did not receive H264 and Opus RTP")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNativePublisherRoutesOneH264LayerPerViewerWithoutRenegotiation(t *testing.T) {
	router := &integrationRouter{viewers: make(map[string]*Viewer)}
	publisher := NewPublisher(PublisherConfig{Signaler: router, Width: 1280, Height: 720, FPS: 60})
	router.publisher = publisher
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("start publisher: %v", err)
	}
	defer publisher.Close()

	harnesses := make([]*browserHarness, 0, 2)
	for index := 1; index <= 2; index++ {
		sessionID := fmt.Sprintf("layer-session-%d", index)
		harness := newBrowserHarness(t, router)
		harnesses = append(harnesses, harness)
		router.viewers[sessionID] = harness.viewer
		defer harness.close()
		defer harness.viewer.Close()
		session := SessionPayload{ID: sessionID, Peer: fmt.Sprintf("layer-viewer-%d", index)}
		if err := harness.viewer.HandleMessage(incomingMessageForIntegration(MessageClientSession, session)); err != nil {
			t.Fatalf("client session %d: %v", index, err)
		}
		if err := publisher.HandleMessage(incomingMessageForIntegration(MessageHostSession, session)); err != nil {
			t.Fatalf("host session %d: %v", index, err)
		}
	}
	for index, harness := range harnesses {
		select {
		case <-harness.connected:
		case <-time.After(8 * time.Second):
			t.Fatalf("viewer %d did not connect", index+1)
		}
	}
	if err := publisher.SetViewerLayer("layer-session-1", "0"); err != nil {
		t.Fatalf("bind first viewer: %v", err)
	}
	if err := publisher.SetViewerLayer("layer-session-2", "1"); err != nil {
		t.Fatalf("bind second viewer: %v", err)
	}

	idr := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0, 0, 1, 0x68, 0xce, 0, 0, 0, 1, 0x65, 0x88}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := publisher.WriteH264LayerSample("0", idr, time.Second/60); err != nil {
			t.Fatalf("write high layer: %v", err)
		}
		select {
		case <-harnesses[0].videoRTP:
			goto highReceived
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("high viewer did not receive its assigned layer")
		}
		time.Sleep(20 * time.Millisecond)
	}

highReceived:
	select {
	case <-harnesses[1].videoRTP:
		t.Fatal("low viewer received high layer before switching")
	case <-time.After(200 * time.Millisecond):
	}
	if err := publisher.SwitchViewerLayer("layer-session-2", "1", idr, time.Second/30); err != nil {
		t.Fatalf("switch low viewer: %v", err)
	}
	select {
	case <-harnesses[1].videoRTP:
	case <-time.After(5 * time.Second):
		t.Fatal("low viewer did not receive switch IDR on its existing track")
	}
}
