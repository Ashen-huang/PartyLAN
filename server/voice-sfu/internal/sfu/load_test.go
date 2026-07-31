package sfu

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"
	gamecastvoice "PartyLAN/internal/gamecast/voice"
	voicesfuauth "PartyLAN/server/voice-sfu/internal/auth"
)

const loadRoom = "voice-load-room"

type loadClient struct {
	participantID string
	connection    gamecastvoice.Connection
	expectedClose atomic.Bool
	sent          atomic.Uint64
	received      atomic.Uint64
	tracks        sync.Map
	cancel        context.CancelFunc
}

func TestVoiceSFUTwentyClientLoad(t *testing.T) {
	durationText := os.Getenv("PARTYLAN_VOICE_LOAD_TEST_DURATION")
	if durationText == "" {
		t.Skip("set PARTYLAN_VOICE_LOAD_TEST_DURATION to run the voice SFU load test")
	}
	duration, err := time.ParseDuration(durationText)
	if err != nil || duration < 10*time.Second {
		t.Fatalf("PARTYLAN_VOICE_LOAD_TEST_DURATION must be at least 10s, got %q", durationText)
	}

	server, err := NewServer(Config{
		Version:         "load-test-v1",
		Secret:          []byte("load-test-secret"),
		MaxParticipants: 20,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/voice-ws" {
			request.URL.Path = "/ws"
		}
		handler.ServeHTTP(w, request)
	}))
	defer httpServer.Close()

	testContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 256)
	clients := make([]*loadClient, 20)
	defer func() {
		for _, client := range clients {
			closeLoadClient(client)
		}
	}()

	for index := range clients {
		participantID := fmt.Sprintf("load-client-%02d", index+1)
		client, startErr := startLoadClient(testContext, httpServer.URL, participantID, errors)
		if startErr != nil {
			t.Fatalf("start %s: %v", participantID, startErr)
		}
		clients[index] = client
	}
	waitForLoadCounts(t, server, 1, 20, 30*time.Second)
	waitForLoadConvergence(t, clients, 19, 90*time.Second)
	assertLoadRoomFull(t, testContext, httpServer.URL)

	churnInterval := 5 * time.Minute
	if duration < 15*time.Minute {
		churnInterval = duration / 3
		if churnInterval < 2*time.Second {
			churnInterval = 2 * time.Second
		}
	}
	churnTicker := time.NewTicker(churnInterval)
	defer churnTicker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	churnIndex := 0

	for {
		select {
		case loadErr := <-errors:
			t.Fatalf("load client failure: %v", loadErr)
		case <-churnTicker.C:
			index := churnIndex % len(clients)
			old := clients[index]
			old.expectedClose.Store(true)
			if churnIndex%2 == 0 {
				old.connection.Close()
				waitForLoadCounts(t, server, 1, 19, 15*time.Second)
			}
			replacement, startErr := startLoadClient(testContext, httpServer.URL, old.participantID, errors)
			if startErr != nil {
				t.Fatalf("reconnect %s: %v", old.participantID, startErr)
			}
			clients[index] = replacement
			waitForLoadDone(t, old.connection, 15*time.Second)
			waitForLoadCounts(t, server, 1, 20, 15*time.Second)
			waitForClientTraffic(t, replacement, 19, 30*time.Second)
			churnIndex++
		case <-deadline.C:
			goto completed
		}
	}

completed:
	var sent uint64
	var received uint64
	for _, client := range clients {
		if client.sent.Load() == 0 || client.received.Load() == 0 || loadTrackCount(client) < 19 {
			t.Fatalf(
				"client %s traffic sent=%d received=%d tracks=%d",
				client.participantID,
				client.sent.Load(),
				client.received.Load(),
				loadTrackCount(client),
			)
		}
		sent += client.sent.Load()
		received += client.received.Load()
		client.expectedClose.Store(true)
		client.connection.Close()
	}
	waitForLoadCounts(t, server, 0, 0, 30*time.Second)
	waitForLoadRelays(t, server, 30*time.Second)
	select {
	case loadErr := <-errors:
		t.Fatalf("load client cleanup failure: %v", loadErr)
	default:
	}
	t.Logf(
		"voice_load_ok duration=%s clients=20 churns=%d sent_packets=%d received_packets=%d rooms=0 connections=0",
		duration,
		churnIndex,
		sent,
		received,
	)
}

func startLoadClient(parent context.Context, baseURL, participantID string, errors chan<- error) (*loadClient, error) {
	var connection gamecastvoice.Connection
	for attempt := 1; attempt <= 5; attempt++ {
		connection = gamecastvoice.NewPionConnection(gamecast.Profile{BaseURL: baseURL})
		if err := connection.Connect(parent, signLoadToken(participantID)); err != nil {
			connection.Close()
			if !isRetryableLoadConnect(err) || attempt == 5 {
				return nil, err
			}
			time.Sleep(time.Duration(1<<(attempt-1)) * 200 * time.Millisecond)
			continue
		}
		break
	}
	connectedTimer := time.NewTimer(30 * time.Second)
	defer connectedTimer.Stop()
	for {
		select {
		case event := <-connection.Events():
			switch event.State {
			case gamecastvoice.ConnectionConnected:
				clientContext, cancel := context.WithCancel(parent)
				client := &loadClient{participantID: participantID, connection: connection, cancel: cancel}
				client.run(clientContext, errors)
				return client, nil
			case gamecastvoice.ConnectionFull, gamecastvoice.ConnectionUnauthorized, gamecastvoice.ConnectionError:
				connection.Close()
				return nil, loadEventError("", event)
			}
		case <-connection.Done():
			return nil, fmt.Errorf("connection closed before connected")
		case <-connectedTimer.C:
			connection.Close()
			return nil, fmt.Errorf("connection timed out")
		}
	}
}

func isRetryableLoadConnect(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) || strings.Contains(err.Error(), "dial tcp")
}

func (client *loadClient) run(ctx context.Context, errors chan<- error) {
	go func() {
		for {
			select {
			case event := <-client.connection.Events():
				if event.State == gamecastvoice.ConnectionError || event.State == gamecastvoice.ConnectionUnauthorized || event.State == gamecastvoice.ConnectionFull {
					client.report(errors, loadEventError(client.participantID, event))
				}
			case <-client.connection.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case packet := <-client.connection.RemotePackets():
				if !packet.End && len(packet.Payload) > 0 {
					client.received.Add(1)
					client.tracks.Store(packet.TrackID, struct{}{})
				}
			case <-client.connection.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		packet := []byte{0xf8, 0xff, 0xfe}
		for {
			select {
			case <-ticker.C:
				if err := client.connection.WriteOpus(packet, 20*time.Millisecond); err != nil {
					client.report(errors, fmt.Errorf("%s write RTP: %w", client.participantID, err))
					return
				}
				client.sent.Add(1)
			case <-client.connection.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				refreshContext, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := client.connection.RefreshAuth(refreshContext, signLoadToken(client.participantID))
				cancel()
				if err != nil {
					client.report(errors, fmt.Errorf("%s refresh auth: %w", client.participantID, err))
					return
				}
			case <-client.connection.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (client *loadClient) report(errors chan<- error, err error) {
	if client.expectedClose.Load() {
		return
	}
	select {
	case errors <- err:
	default:
	}
}

func loadEventError(participantID string, event gamecastvoice.ConnectionEvent) error {
	prefix := ""
	if participantID != "" {
		prefix = participantID + " "
	}
	if event.Err != nil {
		return fmt.Errorf("%sstate=%s code=%s: %w", prefix, event.State, event.ErrorCode, event.Err)
	}
	return fmt.Errorf("%sstate=%s code=%s", prefix, event.State, event.ErrorCode)
}

func closeLoadClient(client *loadClient) {
	if client == nil {
		return
	}
	client.expectedClose.Store(true)
	if client.cancel != nil {
		client.cancel()
	}
	client.connection.Close()
}

func assertLoadRoomFull(t *testing.T, parent context.Context, baseURL string) {
	t.Helper()
	connection := gamecastvoice.NewPionConnection(gamecast.Profile{BaseURL: baseURL})
	if err := connection.Connect(parent, signLoadToken("load-client-overflow")); err != nil {
		t.Fatalf("connect overflow client: %v", err)
	}
	defer connection.Close()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-connection.Events():
			if event.State == gamecastvoice.ConnectionFull && event.ErrorCode == "voice_room_full" {
				return
			}
			if event.State == gamecastvoice.ConnectionConnected || event.State == gamecastvoice.ConnectionError {
				t.Fatalf("overflow state=%s code=%s", event.State, event.ErrorCode)
			}
		case <-connection.Done():
			t.Fatal("overflow client closed without voice_room_full")
		case <-timer.C:
			t.Fatal("overflow client did not receive voice_room_full")
		}
	}
}

func waitForLoadConvergence(t *testing.T, clients []*loadClient, tracks int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := true
		for _, client := range clients {
			if client.received.Load() == 0 || loadTrackCount(client) < tracks {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, client := range clients {
		t.Logf("convergence client=%s received=%d tracks=%d", client.participantID, client.received.Load(), loadTrackCount(client))
	}
	t.Fatal("voice clients did not converge")
}

func waitForClientTraffic(t *testing.T, client *loadClient, tracks int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if client.sent.Load() > 0 && client.received.Load() > 0 && loadTrackCount(client) >= tracks {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("client %s did not resume full traffic sent=%d received=%d tracks=%d", client.participantID, client.sent.Load(), client.received.Load(), loadTrackCount(client))
}

func waitForLoadCounts(t *testing.T, server *Server, rooms, connections int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		gotRooms, gotConnections := server.registry.Counts()
		if gotRooms == rooms && gotConnections == connections {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	gotRooms, gotConnections := server.registry.Counts()
	t.Fatalf("load counts=(%d,%d), want=(%d,%d)", gotRooms, gotConnections, rooms, connections)
}

func waitForLoadDone(t *testing.T, connection gamecastvoice.Connection, timeout time.Duration) {
	t.Helper()
	select {
	case <-connection.Done():
	case <-time.After(timeout):
		t.Fatal("replaced voice connection did not close")
	}
}

func waitForLoadRelays(t *testing.T, server *Server, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		server.relayMu.Lock()
		count := len(server.relays)
		server.relayMu.Unlock()
		if count == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("voice relays were not cleaned up")
}

func loadTrackCount(client *loadClient) int {
	count := 0
	client.tracks.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func signLoadToken(participantID string) string {
	now := time.Now().Unix()
	claims := voicesfuauth.Claims{
		Audience:      "voice-sfu",
		Room:          loadRoom,
		ParticipantID: participantID,
		ClientType:    "client",
		IssuedAt:      now,
		ExpiresAt:     now + 15,
		TokenID:       fmt.Sprintf("load-%d", time.Now().UnixNano()),
	}
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, []byte("load-test-secret"))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
