package media

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"PartyLAN/internal/relaycfg"
	"PartyLAN/internal/room"
)

type recordingSignaler struct {
	mu      sync.Mutex
	signals []room.MediaSignalPayload
}

func (s *recordingSignaler) SendMediaSignal(peerID string, payload room.MediaSignalPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if payload.ToPeerID != peerID {
		payload.ToPeerID = peerID
	}
	s.signals = append(s.signals, payload)
	return nil
}

func (s *recordingSignaler) snapshot() []room.MediaSignalPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]room.MediaSignalPayload(nil), s.signals...)
}

func TestICEServersFromEnv(t *testing.T) {
	t.Setenv("PARTYLAN_ICE_SERVERS", " stun:stun.example.com:3478, turn:turn.example.com:3478 ")
	t.Setenv("PARTYLAN_TURN_USERNAME", "user")
	t.Setenv("PARTYLAN_TURN_CREDENTIAL", "secret")

	servers := ICEServersFromEnv()
	if len(servers) != 1 {
		t.Fatalf("expected one ICE server group, got %#v", servers)
	}
	if got := servers[0].URLs; len(got) != 2 || got[0] != "stun:stun.example.com:3478" || got[1] != "turn:turn.example.com:3478" {
		t.Fatalf("unexpected ICE URLs: %#v", got)
	}
	if servers[0].Username != "user" || servers[0].Credential != "secret" {
		t.Fatalf("TURN credentials not applied: %#v", servers[0])
	}
}

func TestICEServersFromEnvIgnoresEmptyInput(t *testing.T) {
	t.Setenv("PARTYLAN_ICE_SERVERS", " , ")
	t.Setenv("PARTYLAN_TURN_USERNAME", "user")

	if servers := ICEServersFromEnv(); len(servers) != 0 {
		t.Fatalf("expected no ICE servers for empty input, got %#v", servers)
	}
}

func TestStartShareRequiresSessionID(t *testing.T) {
	manager := NewManager("self", nil)
	if err := manager.StartShare(context.Background(), ShareConfig{}); err == nil {
		t.Fatal("expected missing session id to fail")
	}
}

func TestStartShareAttachesViewerAndCreatesTracks(t *testing.T) {
	signaler := &recordingSignaler{}
	manager := NewManager("self", signaler)
	defer manager.StopShare()

	err := manager.StartShare(context.Background(), ShareConfig{
		SessionID:    "share-1",
		Width:        1280,
		Height:       720,
		FPS:          30,
		Peers:        []room.PeerInfo{{ID: "peer-1", Hostname: "viewer"}},
		AudioEnabled: true,
	})
	if err != nil {
		t.Fatalf("StartShare: %v", err)
	}

	signals := signaler.snapshot()
	if len(signals) != 1 {
		t.Fatalf("expected one offer signal, got %#v", signals)
	}
	if signals[0].Kind != "offer" || signals[0].FromPeerID != "self" || signals[0].ToPeerID != "peer-1" {
		t.Fatalf("unexpected offer signal: %#v", signals[0])
	}

	manager.mu.Lock()
	session := manager.sessions["peer-1"]
	shareConf := manager.shareConf
	manager.mu.Unlock()

	if shareConf.HostID != "self" {
		t.Fatalf("expected default host id to be self, got %q", shareConf.HostID)
	}
	if session == nil {
		t.Fatal("expected peer session to be registered")
	}
	if session.videoTrack == nil {
		t.Fatal("expected video track")
	}
	if session.audioTrack == nil {
		t.Fatal("expected audio track when AudioEnabled is true")
	}
}

func TestHandleSignalWithoutSession(t *testing.T) {
	manager := NewManager("self", nil)
	err := manager.HandleSignal(room.MediaSignalPayload{
		SessionID:  "share-1",
		FromPeerID: "missing-peer",
		ToPeerID:   "self",
		Kind:       "answer",
	})
	if !errors.Is(err, ErrUnhandledSignal) {
		t.Fatalf("expected ErrUnhandledSignal, got %v", err)
	}
}

func TestManagerConfigFromRelayConfigIncludesHealth(t *testing.T) {
	cfg := relaycfg.RuntimeConfig{
		ICEServers: []relaycfg.ICEServer{{
			URLs:       []string{"stun:stun.example.com:3478", "turn:turn.example.com:3478"},
			Username:   "user",
			Credential: "secret",
		}},
	}

	managerCfg := ManagerConfigFromRelayConfig(cfg)
	if len(managerCfg.ICEServers) != 1 {
		t.Fatalf("expected one ICE server, got %#v", managerCfg.ICEServers)
	}
	wantHealth := []string{
		"media ICE servers configured: groups=1 urls=2 turn=true",
		"media TURN credentials configured",
	}
	if !reflect.DeepEqual(managerCfg.Health, wantHealth) {
		t.Fatalf("unexpected manager health:\n got: %#v\nwant: %#v", managerCfg.Health, wantHealth)
	}
}

func TestManagerDiagnosticsReturnsConfiguredHealthWithoutPeers(t *testing.T) {
	manager := NewManagerWithConfig("self", nil, ManagerConfig{
		Health: []string{"media ICE servers configured: groups=1 urls=2 turn=true"},
	})

	if got := manager.Diagnostics(); !reflect.DeepEqual(got, []string{"media ICE servers configured: groups=1 urls=2 turn=true"}) {
		t.Fatalf("unexpected diagnostics: %#v", got)
	}
}
