package screegop2p

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestViewerForwardsSessionOfferAndICEToFrontend(t *testing.T) {
	signaler := newRecordingSignaler()
	signals := make([]json.RawMessage, 0, 2)
	viewer := NewViewer(ViewerConfig{
		Signaler: signaler,
		OnSignal: func(signal json.RawMessage) {
			signals = append(signals, append(json.RawMessage(nil), signal...))
		},
	})
	session := SessionPayload{
		ID: "session-1", Peer: "host-1",
		ICEServers: []ICEServer{{
			URLs:     []string{"turn:8.134.126.54:3478?transport=udp"},
			Username: "temporary-user", Credential: "temporary-secret",
		}},
	}
	if err := viewer.HandleMessage(incomingMessage(t, MessageClientSession, session)); err != nil {
		t.Fatalf("client session: %v", err)
	}
	offer := json.RawMessage(`{"type":"offer","sdp":"v=0\\r\\n"}`)
	if err := viewer.HandleMessage(incomingMessage(t, MessageHostOffer, P2PPayload{SID: session.ID, Value: offer})); err != nil {
		t.Fatalf("host offer: %v", err)
	}
	candidate := json.RawMessage(`{"candidate":"candidate:1 1 udp 1 127.0.0.1 9000 typ host"}`)
	if err := viewer.HandleMessage(incomingMessage(t, MessageHostICE, P2PPayload{SID: session.ID, Value: candidate})); err != nil {
		t.Fatalf("host ICE: %v", err)
	}

	if len(signals) != 2 {
		t.Fatalf("signals=%d want 2", len(signals))
	}
	var forwarded struct {
		Type       string          `json:"type"`
		SessionID  string          `json:"sessionId"`
		SDP        json.RawMessage `json:"sdp"`
		ICE        json.RawMessage `json:"ice"`
		ICEServers []ICEServer     `json:"iceServers"`
	}
	if err := json.Unmarshal(signals[0], &forwarded); err != nil {
		t.Fatalf("decode offer signal: %v", err)
	}
	if forwarded.Type != "offer" || forwarded.SessionID != session.ID {
		t.Fatalf("forwarded offer=%s", signals[0])
	}
	if string(forwarded.SDP) != string(offer) {
		t.Fatalf("offer SDP=%s want %s", forwarded.SDP, offer)
	}
	if len(forwarded.ICEServers) != 1 || forwarded.ICEServers[0].Username != "temporary-user" || forwarded.ICEServers[0].Credential != "temporary-secret" {
		t.Fatalf("ICE servers=%+v", forwarded.ICEServers)
	}
	if err := json.Unmarshal(signals[1], &forwarded); err != nil {
		t.Fatalf("decode ICE signal: %v", err)
	}
	if forwarded.Type != "ice-candidate" || forwarded.SessionID != session.ID || string(forwarded.ICE) != string(candidate) {
		t.Fatalf("forwarded ICE=%s", signals[1])
	}
}

func TestViewerMapsFrontendAnswerAndICEToCurrentSession(t *testing.T) {
	signaler := newRecordingSignaler()
	viewer := NewViewer(ViewerConfig{Signaler: signaler})
	if err := viewer.HandleMessage(incomingMessage(t, MessageClientSession, SessionPayload{ID: "session-1", Peer: "host-1"})); err != nil {
		t.Fatalf("client session: %v", err)
	}

	answer := []byte(`{"type":"answer","sessionId":"session-1","sdp":"v=0\r\n"}`)
	if err := viewer.SendFrontendSignal(context.Background(), answer); err != nil {
		t.Fatalf("send answer: %v", err)
	}
	candidate := []byte(`{"type":"ice-candidate","sessionId":"session-1","ice":{"candidate":"candidate:1"}}`)
	if err := viewer.SendFrontendSignal(context.Background(), candidate); err != nil {
		t.Fatalf("send candidate: %v", err)
	}

	answerMessage := signaler.waitForType(t, MessageClientAnswer)
	answerPayload, ok := answerMessage.Payload.(P2PPayload)
	if !ok || answerPayload.SID != "session-1" {
		t.Fatalf("client answer payload=%#v", answerMessage.Payload)
	}
	var description struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if err := json.Unmarshal(answerPayload.Value, &description); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if description.Type != "answer" || description.SDP != "v=0\r\n" {
		t.Fatalf("answer=%+v", description)
	}
	iceMessage := signaler.waitForType(t, MessageClientICE)
	icePayload, ok := iceMessage.Payload.(P2PPayload)
	if !ok || icePayload.SID != "session-1" || string(icePayload.Value) != `{"candidate":"candidate:1"}` {
		t.Fatalf("client ICE payload=%#v", iceMessage.Payload)
	}
}

func TestViewerReplacesSessionRejectsStaleFrontendSignalsAndEndsCurrentShare(t *testing.T) {
	signaler := newRecordingSignaler()
	signals := make([]json.RawMessage, 0, 2)
	viewer := NewViewer(ViewerConfig{
		Signaler: signaler,
		OnSignal: func(signal json.RawMessage) {
			signals = append(signals, append(json.RawMessage(nil), signal...))
		},
	})
	for _, sessionID := range []string{"session-1", "session-2"} {
		if err := viewer.HandleMessage(incomingMessage(t, MessageClientSession, SessionPayload{ID: sessionID, Peer: "host"})); err != nil {
			t.Fatalf("client session %s: %v", sessionID, err)
		}
	}
	if got := viewer.CurrentSessionID(); got != "session-2" {
		t.Fatalf("CurrentSessionID=%q want session-2", got)
	}
	err := viewer.SendFrontendSignal(context.Background(), []byte(`{"type":"answer","sessionId":"session-1","sdp":"v=0\r\n"}`))
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != ErrorSessionFailed {
		t.Fatalf("stale signal error=%v", err)
	}
	if err := viewer.HandleMessage(incomingMessage(t, MessageEndShare, "session-2")); err != nil {
		t.Fatalf("end share: %v", err)
	}
	if got := viewer.CurrentSessionID(); got != "" {
		t.Fatalf("CurrentSessionID=%q after endshare", got)
	}
	if len(signals) != 2 {
		t.Fatalf("endshare signals=%d want 2", len(signals))
	}
	for index, raw := range signals {
		var signal struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &signal); err != nil {
			t.Fatalf("decode signal %d: %v", index, err)
		}
		if signal.Type != "endshare" || signal.SessionID != []string{"session-1", "session-2"}[index] {
			t.Fatalf("signal %d=%s", index, raw)
		}
	}
}
