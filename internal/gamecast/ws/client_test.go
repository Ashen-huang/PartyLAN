package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/gorilla/websocket"
)

func TestViewerCanReconnectAfterServerClosesSignaling(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	connections := make(chan *websocket.Conn, 2)
	queries := make(chan map[string]string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sfu-ws" {
			t.Errorf("websocket path = %q, want /sfu-ws", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		queries <- map[string]string{
			"room":  r.URL.Query().Get("room"),
			"role":  r.URL.Query().Get("role"),
			"layer": r.URL.Query().Get("layer"),
			"name":  r.URL.Query().Get("name"),
		}
		connections <- conn
	}))
	defer server.Close()

	connect := func(messages chan json.RawMessage, statuses chan Status) *Client {
		t.Helper()
		client := NewClient(Options{
			Profile: gamecast.Profile{BaseURL: server.URL},
			RoomID:  "web-room",
			Role:    "viewer",
			Layer:   "h",
			Name:    "PartyLAN Viewer",
			OnMessage: func(message json.RawMessage) {
				messages <- message
			},
			OnStatus: func(status Status) {
				statuses <- status
			},
		})
		if err := client.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		return client
	}

	messages := make(chan json.RawMessage, 2)
	statuses := make(chan Status, 10)
	first := connect(messages, statuses)
	firstConn := receiveBefore(t, connections, time.Second, "first server connection")
	firstQuery := receiveBefore(t, queries, time.Second, "first websocket query")
	if firstQuery["room"] != "web-room" || firstQuery["role"] != "viewer" || firstQuery["layer"] != "h" {
		t.Fatalf("first query = %#v", firstQuery)
	}

	offer := []byte(`{"type":"offer","sdp":"v=0\\r\\n"}`)
	if err := firstConn.WriteMessage(websocket.TextMessage, offer); err != nil {
		t.Fatalf("send offer: %v", err)
	}
	if got := receiveBefore(t, messages, time.Second, "forwarded offer"); string(got) != string(offer) {
		t.Fatalf("forwarded offer = %s, want %s", got, offer)
	}

	answer := []byte(`{"type":"answer","sdp":"v=0\\r\\n"}`)
	if err := first.Send(context.Background(), answer); err != nil {
		t.Fatalf("send answer: %v", err)
	}
	_, payload, err := firstConn.ReadMessage()
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	if string(payload) != string(answer) {
		t.Fatalf("server answer = %s, want %s", payload, answer)
	}

	if err := firstConn.Close(); err != nil {
		t.Fatalf("close first server connection: %v", err)
	}
	waitForStatus(t, statuses, "closed", 2*time.Second)

	second := connect(messages, statuses)
	secondConn := receiveBefore(t, connections, time.Second, "second server connection")
	defer secondConn.Close()
	defer second.Close()
	secondQuery := receiveBefore(t, queries, time.Second, "second websocket query")
	if secondQuery["room"] != firstQuery["room"] ||
		secondQuery["role"] != firstQuery["role"] ||
		secondQuery["layer"] != firstQuery["layer"] ||
		secondQuery["name"] != firstQuery["name"] {
		t.Fatalf("reconnect query = %#v, want %#v", secondQuery, firstQuery)
	}
	waitForStatus(t, statuses, "connected", time.Second)

	_ = first.Close()
}

func receiveBefore[T any](t *testing.T, values <-chan T, timeout time.Duration, label string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(timeout):
		var zero T
		t.Fatalf("timed out waiting for %s", label)
		return zero
	}
}

func waitForStatus(t *testing.T, statuses <-chan Status, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case status := <-statuses:
			if status.State == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for websocket status %q", want)
		}
	}
}
