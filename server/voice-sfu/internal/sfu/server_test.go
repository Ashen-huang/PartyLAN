package sfu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	voicesfuauth "PartyLAN/server/voice-sfu/internal/auth"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server, err := NewServer(Config{Version: "test-v1", Secret: []byte("test-secret")})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws"
}

func currentToken(t *testing.T, room, participant string) string {
	t.Helper()
	now := time.Now().Unix()
	return buildToken(t, "test-secret", voicesfuauth.Claims{
		Audience:      "voice-sfu",
		Room:          room,
		ParticipantID: participant,
		ClientType:    "client",
		IssuedAt:      now,
		ExpiresAt:     now + 15,
		TokenID:       "test-jti",
	})
}

func buildToken(t *testing.T, secret string, claims voicesfuauth.Claims) string {
	t.Helper()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestHealthExposesOnlyVersionAndAggregateCounts(t *testing.T) {
	server, err := NewServer(Config{Version: "test-v1", Secret: []byte("test-secret")})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	admitFake(t, server.registry, "room-a", "client-1")
	admitFake(t, server.registry, "room-b", "client-2")

	request := httptest.NewRequest(http.MethodGet, "http://voice-sfu.local/health", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d body=%s", response.StatusCode, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if len(payload) != 3 || payload["version"] != "test-v1" || payload["roomCount"] != float64(2) || payload["connectionCount"] != float64(2) {
		t.Fatalf("health payload = %#v", payload)
	}
	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{"participant", "token", "sdp", "candidate", "authorization"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("health leaks %q: %s", forbidden, body)
		}
	}
}

func TestWebSocketRequiresAuthorizationHeaderAndIgnoresURLToken(t *testing.T) {
	_, httpServer := newTestServer(t)
	token := currentToken(t, "room", "client")
	for _, target := range []string{websocketURL(httpServer.URL), websocketURL(httpServer.URL) + "?token=" + token} {
		connection, response, err := websocket.DefaultDialer.Dial(target, nil)
		if connection != nil {
			_ = connection.Close()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Dial(%s) response=%v err=%v", target, response, err)
		}
		_ = response.Body.Close()
	}
}

func TestServerIsOnlyOffererAndRejectsOldAnswer(t *testing.T) {
	_, httpServer := newTestServer(t)
	header := http.Header{"Authorization": []string{"Bearer " + currentToken(t, "room", "client")}}
	connection, response, err := websocket.DefaultDialer.Dial(websocketURL(httpServer.URL), header)
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("Dial status=%d body=%s err=%v", response.StatusCode, body, err)
		}
		t.Fatalf("Dial: %v", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	var offer struct {
		Type     string `json:"type"`
		Revision uint64 `json:"revision"`
		SDP      string `json:"sdp"`
	}
	if err := connection.ReadJSON(&offer); err != nil {
		t.Fatalf("read offer: %v", err)
	}
	if offer.Type != "offer" || offer.Revision != 1 || !strings.Contains(offer.SDP, "m=audio") {
		t.Fatalf("offer = %#v", offer)
	}
	if err := connection.WriteJSON(map[string]any{
		"type":     "answer",
		"revision": 0,
		"sdp":      "v=0\r\n",
	}); err != nil {
		t.Fatalf("write old answer: %v", err)
	}
	found := false
	for index := 0; index < 8; index++ {
		var failure struct {
			Type string `json:"type"`
			Code string `json:"code"`
		}
		if err := connection.ReadJSON(&failure); err != nil {
			t.Fatalf("read stale-answer error: %v", err)
		}
		if failure.Type == "error" && failure.Code == "voice_stale_revision" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("stale-answer error was not received")
	}
}

func TestRoomAllowsTwentyUniqueClientsAndRejectsTwentyFirstBeforePeerCreation(t *testing.T) {
	api := webrtc.NewAPI()
	var created atomic.Int32
	server, err := NewServer(Config{
		Version:         "test-v1",
		Secret:          []byte("test-secret"),
		MaxParticipants: 20,
		PeerFactory: func() (PeerConnection, error) {
			created.Add(1)
			return api.NewPeerConnection(webrtc.Configuration{})
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	connections := make([]*websocket.Conn, 0, 20)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()

	for index := 0; index < 20; index++ {
		header := http.Header{"Authorization": []string{"Bearer " + currentToken(t, "full-room", "client-"+string(rune('a'+index)))}}
		connection, _, err := websocket.DefaultDialer.Dial(websocketURL(httpServer.URL), header)
		if err != nil {
			t.Fatalf("Dial client %d: %v", index+1, err)
		}
		connections = append(connections, connection)
		_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		var offer wireSignal
		if err := connection.ReadJSON(&offer); err != nil || offer.Type != "offer" {
			t.Fatalf("client %d initial offer=%#v err=%v", index+1, offer, err)
		}
	}

	header := http.Header{"Authorization": []string{"Bearer " + currentToken(t, "full-room", "client-overflow")}}
	overflow, _, err := websocket.DefaultDialer.Dial(websocketURL(httpServer.URL), header)
	if err != nil {
		t.Fatalf("Dial overflow client: %v", err)
	}
	defer overflow.Close()
	_ = overflow.SetReadDeadline(time.Now().Add(5 * time.Second))
	var failure wireSignal
	if err := overflow.ReadJSON(&failure); err != nil {
		t.Fatalf("read overflow error: %v", err)
	}
	if failure.Type != "error" || failure.Code != "voice_room_full" {
		t.Fatalf("overflow response = %#v", failure)
	}
	if got := created.Load(); got != 20 {
		t.Fatalf("peer connections created = %d, want 20", got)
	}
}

func TestAuthRefreshCannotChangeRoomOrParticipant(t *testing.T) {
	_, httpServer := newTestServer(t)
	header := http.Header{"Authorization": []string{"Bearer " + currentToken(t, "room-a", "client-a")}}
	connection, _, err := websocket.DefaultDialer.Dial(websocketURL(httpServer.URL), header)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var initial wireSignal
	if err := connection.ReadJSON(&initial); err != nil || initial.Type != "offer" {
		t.Fatalf("initial signal=%#v err=%v", initial, err)
	}
	if err := connection.WriteJSON(wireSignal{
		Type:  "auth-refresh",
		Token: currentToken(t, "room-b", "client-a"),
	}); err != nil {
		t.Fatalf("write auth refresh: %v", err)
	}
	found := false
	for index := 0; index < 8; index++ {
		var signal wireSignal
		if err := connection.ReadJSON(&signal); err != nil {
			break
		}
		if signal.Type == "error" && signal.Code == "voice_unauthorized" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cross-room auth refresh did not close with voice_unauthorized")
	}
}
