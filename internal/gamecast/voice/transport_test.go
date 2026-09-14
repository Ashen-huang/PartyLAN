package voice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func TestVoiceWebSocketURLHasFixedPathAndNoTokenQuery(t *testing.T) {
	got, err := voiceWebSocketURL(gamecast.Profile{BaseURL: "https://example.com/base"})
	if err != nil {
		t.Fatalf("voiceWebSocketURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "example.com" || parsed.Path != "/base/voice-ws" || parsed.RawQuery != "" {
		t.Fatalf("voice websocket URL = %q", got)
	}
}

func TestAttachMicrophoneTrackUsesServerReservedAudioTransceiver(t *testing.T) {
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create offerer: %v", err)
	}
	defer offerer.Close()
	if _, err := offerer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("reserve microphone transceiver: %v", err)
	}
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}

	answerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create answerer: %v", err)
	}
	defer answerer.Close()
	if err := answerer.SetRemoteDescription(offer); err != nil {
		t.Fatalf("set remote offer: %v", err)
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  2,
	}, "microphone", "voice-client-test")
	if err != nil {
		t.Fatalf("create microphone track: %v", err)
	}
	before := len(answerer.GetTransceivers())
	sender, err := attachMicrophoneTrack(answerer, track)
	if err != nil {
		t.Fatalf("attach microphone track: %v", err)
	}
	defer sender.Stop()
	if after := len(answerer.GetTransceivers()); after != before {
		t.Fatalf("transceivers after attach = %d, want %d", after, before)
	}
	answer, err := answerer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	if !strings.Contains(answer.SDP, "a=sendonly") {
		t.Fatalf("answer does not send the microphone on the reserved m-line")
	}
	if err := answerer.SetLocalDescription(answer); err != nil {
		t.Fatalf("set local answer: %v", err)
	}
}

func TestPionConnectionIgnoresRejectedStaleSignal(t *testing.T) {
	connection := &PionConnection{
		state:  ConnectionConnecting,
		events: make(chan ConnectionEvent, 1),
		done:   make(chan struct{}),
	}
	if err := connection.handleSignal(clientSignal{Type: "error", Code: "voice_stale_revision"}); err != nil {
		t.Fatalf("handle stale signal: %v", err)
	}
	select {
	case event := <-connection.events:
		t.Fatalf("stale signal emitted fatal event: %#v", event)
	default:
	}
}

func TestPionConnectionSendsTokenOnlyInAuthorizationHeader(t *testing.T) {
	headerReceived := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		headerReceived <- request.Header.Get("Authorization")
		if request.URL.RawQuery != "" {
			t.Errorf("voice websocket query = %q", request.URL.RawQuery)
		}
		connection, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()
	connection := NewPionConnection(gamecast.Profile{BaseURL: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	if err := connection.Connect(ctx, "short-lived-token"); err != nil {
		cancel()
		t.Fatalf("Connect: %v", err)
	}
	select {
	case header := <-headerReceived:
		if header != "Bearer short-lived-token" {
			t.Fatalf("Authorization = %q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("websocket handshake was not received")
	}
	cancel()
	connection.Close()
}
