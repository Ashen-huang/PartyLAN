package screegop2p

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

type recordingSignaler struct {
	mu       sync.Mutex
	messages []OutgoingMessage
	notify   chan struct{}
}

func newRecordingSignaler() *recordingSignaler {
	return &recordingSignaler{notify: make(chan struct{}, 32)}
}

func (s *recordingSignaler) Send(_ context.Context, message OutgoingMessage) error {
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *recordingSignaler) waitForType(t *testing.T, messageType string) OutgoingMessage {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		for _, message := range s.messages {
			if message.Type == messageType {
				s.mu.Unlock()
				return message
			}
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; messages=%v", messageType, s.messageTypes())
		}
	}
}

func (s *recordingSignaler) messageTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]string, 0, len(s.messages))
	for _, message := range s.messages {
		types = append(types, message.Type)
	}
	return types
}

func incomingMessage(t *testing.T, messageType string, payload interface{}) IncomingMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal incoming payload: %v", err)
	}
	return IncomingMessage{Type: messageType, Payload: raw}
}

func TestPublisherCreatesIndependentPeerForEveryHostSession(t *testing.T) {
	signaler := newRecordingSignaler()
	var configsMu sync.Mutex
	configs := make([]webrtc.Configuration, 0, 2)
	publisher := NewPublisher(PublisherConfig{
		Signaler:     signaler,
		Width:        1920,
		Height:       1080,
		FPS:          60,
		AudioEnabled: true,
		PeerConnectionFactory: func(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
			configsMu.Lock()
			configs = append(configs, configuration)
			configsMu.Unlock()
			return webrtc.NewPeerConnection(configuration)
		},
	})
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := signaler.waitForType(t, MessageShare).Type; got != MessageShare {
		t.Fatalf("start message=%q", got)
	}

	first := SessionPayload{
		ID: "session-1", Peer: "viewer-1",
		ICEServers: []ICEServer{{
			URLs: []string{"turn:8.134.126.54:3478"}, Username: "user-1", Credential: "secret-1",
		}},
	}
	second := SessionPayload{
		ID: "session-2", Peer: "viewer-2",
		ICEServers: []ICEServer{{
			URLs: []string{"turn:8.134.126.54:3478"}, Username: "user-2", Credential: "secret-2",
		}},
	}
	if err := publisher.HandleMessage(incomingMessage(t, MessageHostSession, first)); err != nil {
		t.Fatalf("handle first host session: %v", err)
	}
	if err := publisher.HandleMessage(incomingMessage(t, MessageHostSession, second)); err != nil {
		t.Fatalf("handle second host session: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for publisher.SessionCount() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := publisher.SessionCount(); got != 2 {
		t.Fatalf("SessionCount=%d want 2", got)
	}
	configsMu.Lock()
	if len(configs) != 2 {
		configsMu.Unlock()
		t.Fatalf("peer configurations=%d want 2", len(configs))
	}
	for index, wantUser := range []string{"user-1", "user-2"} {
		if len(configs[index].ICEServers) != 1 || configs[index].ICEServers[0].Username != wantUser {
			configsMu.Unlock()
			t.Fatalf("configuration %d ICE=%+v", index, configs[index].ICEServers)
		}
	}
	configsMu.Unlock()

	hostOffer := signaler.waitForType(t, MessageHostOffer)
	payload, ok := hostOffer.Payload.(P2PPayload)
	if !ok || (payload.SID != "session-1" && payload.SID != "session-2") || len(payload.Value) == 0 {
		t.Fatalf("host offer payload=%#v", hostOffer.Payload)
	}

	if err := publisher.HandleMessage(incomingMessage(t, MessageEndShare, "session-1")); err != nil {
		t.Fatalf("end first session: %v", err)
	}
	if got := publisher.SessionCount(); got != 1 {
		t.Fatalf("SessionCount after endshare=%d want 1", got)
	}
	if err := publisher.WriteH264Sample([]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}, time.Second/60); err != nil {
		t.Fatalf("WriteH264Sample with remaining viewer: %v", err)
	}
	if err := publisher.WriteOpusSample([]byte{0xf8, 0xff, 0xfe}, 20*time.Millisecond); err != nil {
		t.Fatalf("WriteOpusSample with remaining viewer: %v", err)
	}

	if err := publisher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := publisher.SessionCount(); got != 0 {
		t.Fatalf("SessionCount after close=%d", got)
	}
	if got := signaler.waitForType(t, MessageStopShare).Type; got != MessageStopShare {
		t.Fatalf("stop message=%q", got)
	}
}

func TestPublisherQueuesClientICEUntilAnswerAndIgnoresUnknownSession(t *testing.T) {
	signaler := newRecordingSignaler()
	publisher := NewPublisher(PublisherConfig{
		Signaler: signaler,
		Width:    1280, Height: 720, FPS: 60,
	})
	if err := publisher.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer publisher.Close()
	if err := publisher.HandleMessage(incomingMessage(t, MessageHostSession, SessionPayload{ID: "session-1", Peer: "viewer-1"})); err != nil {
		t.Fatalf("host session: %v", err)
	}
	candidate, _ := json.Marshal(webrtc.ICECandidateInit{Candidate: "candidate:1 1 udp 1 127.0.0.1 9999 typ host"})
	if err := publisher.HandleMessage(incomingMessage(t, MessageClientICE, P2PPayload{SID: "session-1", Value: candidate})); err != nil {
		t.Fatalf("queue client ICE: %v", err)
	}
	if got := publisher.PendingICECount("session-1"); got != 1 {
		t.Fatalf("PendingICECount=%d want 1", got)
	}
	if err := publisher.HandleMessage(incomingMessage(t, MessageClientICE, P2PPayload{SID: "unknown", Value: candidate})); err != nil {
		t.Fatalf("unknown session ICE should be ignored: %v", err)
	}
}
