package screegop2p

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/gorilla/websocket"
)

func TestClientConnectCreatesRoomWithAuthenticatedCookieAndWaitsForAck(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	received := make(chan OutgoingMessage, 1)
	cookies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Errorf("path=%q want /stream", r.URL.Path)
		}
		cookies <- r.Header.Get("Cookie")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read create: %v", err)
			return
		}
		var message OutgoingMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode create: %v", err)
			return
		}
		received <- message
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "room",
			"payload": map[string]interface{}{
				"id": "test", "mode": "turn", "users": []interface{}{},
			},
		})
		<-r.Context().Done()
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	base, _ := url.Parse(server.URL)
	jar.SetCookies(base, []*http.Cookie{{Name: "session", Value: "secret-cookie", Path: "/"}})

	client := NewClient(Options{
		Profile: gamecast.Profile{BaseURL: server.URL},
		Jar:     jar,
		RoomID:  "test",
		Name:    "Desktop",
		Owner:   true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	select {
	case cookie := <-cookies:
		if cookie != "session=secret-cookie" {
			t.Fatalf("cookie header=%q", cookie)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket cookie")
	}
	select {
	case message := <-received:
		if message.Type != MessageCreate {
			t.Fatalf("type=%q", message.Type)
		}
		payload, ok := message.Payload.(map[string]interface{})
		if !ok {
			t.Fatalf("payload type=%T", message.Payload)
		}
		if payload["id"] != "test" || payload["mode"] != "turn" || payload["joinIfExist"] != true {
			t.Fatalf("payload=%v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for create message")
	}
}

func TestClientConnectMapsRoomNotFoundWithoutLeakingServerDetail(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteJSON(map[string]interface{}{
			"type":    "Error",
			"payload": map[string]string{"message": "room with id private-room does not exist"},
		})
	}))
	defer server.Close()

	client := NewClient(Options{
		Profile: gamecast.Profile{BaseURL: server.URL},
		RoomID:  "private-room",
		Name:    "Viewer",
	})
	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect succeeded")
	}
	protocolError, ok := err.(*ProtocolError)
	if !ok {
		t.Fatalf("error type=%T error=%v", err, err)
	}
	if protocolError.Code != ErrorRoomNotFound || err.Error() != ErrorRoomNotFound {
		t.Fatalf("error=%v code=%q", err, protocolError.Code)
	}
}
