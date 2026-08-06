package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"PartyLAN/internal/audio"
	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/gamecast/adaptive"
	hostwindowcapture "PartyLAN/internal/gamecast/host/windowcapture"
	"PartyLAN/internal/gamecast/nativehost"
	"PartyLAN/internal/gamecast/session"
	"PartyLAN/internal/gamecast/shareaudio"
	"PartyLAN/internal/gamecast/sharecatalog"
	gamecastvoice "PartyLAN/internal/gamecast/voice"
	gamecastws "PartyLAN/internal/gamecast/ws"
	"PartyLAN/internal/native/windowsaudio"
	"PartyLAN/internal/network"
	"PartyLAN/internal/video"
	"PartyLAN/pkg/config"

	ps "github.com/mitchellh/go-ps"
)

func TestMigrateGameCastConfigPreservesNonServerFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GameCast.BaseURL = "https://85.137.240.159:5050"
	cfg.GameCast.TrustedCertificateSHA256 = "old-pin"
	cfg.GameCast.DisplayName = "Viewer"
	cfg.GameCast.RoomHistory = []config.GameCastRoomHistoryEntry{{ID: "room-1", Time: 42}}
	cfg.AudioBitrate = 96

	if !migrateGameCastConfig(cfg) {
		t.Fatal("expected legacy GameCast config to migrate")
	}
	if cfg.GameCast.BaseURL != "https://partylanapp.com" {
		t.Fatalf("migrated base URL = %q", cfg.GameCast.BaseURL)
	}
	if cfg.GameCast.TrustedCertificateSHA256 != "" {
		t.Fatalf("legacy certificate pin was not cleared: %q", cfg.GameCast.TrustedCertificateSHA256)
	}
	if cfg.GameCast.DisplayName != "Viewer" || len(cfg.GameCast.RoomHistory) != 1 || cfg.AudioBitrate != 96 {
		t.Fatalf("non-server config changed: %#v", cfg)
	}
}

func TestMigrateGameCastConfigIsIdempotent(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GameCast.BaseURL = "https://113.142.217.42:5050"
	if !migrateGameCastConfig(cfg) {
		t.Fatal("expected first migration")
	}
	if migrateGameCastConfig(cfg) {
		t.Fatal("did not expect second migration")
	}
}

func TestNewAppAssignsStableGameCastParticipantID(t *testing.T) {
	first := NewApp(network.NewLoopbackNetwork("first"))
	second := NewApp(network.NewLoopbackNetwork("second"))
	if !strings.HasPrefix(first.gamecastParticipantID, "client-") {
		t.Fatalf("participant id = %q", first.gamecastParticipantID)
	}
	if first.gamecastParticipantID != first.gameCastPresenceRequest("room-1", "owner", "client").ParticipantID {
		t.Fatal("participant id changed within one App instance")
	}
	if first.gamecastParticipantID == second.gamecastParticipantID {
		t.Fatalf("separate App instances shared participant id %q", first.gamecastParticipantID)
	}
}

func TestGameCastWSGenerationRejectsStaleCallbacks(t *testing.T) {
	application := &App{}
	first := application.nextGameCastWSGeneration()
	called := 0
	if !application.withCurrentGameCastWSGeneration(first, func() {
		called++
	}) {
		t.Fatal("expected current GameCast websocket generation to be accepted")
	}

	second := application.nextGameCastWSGeneration()
	if second <= first {
		t.Fatalf("generation did not increase: first=%d second=%d", first, second)
	}
	if application.withCurrentGameCastWSGeneration(first, func() {
		called++
	}) {
		t.Fatal("expected stale GameCast websocket generation to be rejected")
	}
	if !application.withCurrentGameCastWSGeneration(second, func() {
		called++
	}) {
		t.Fatal("expected replacement GameCast websocket generation to be accepted")
	}
	if called != 2 {
		t.Fatalf("accepted callback count = %d, want 2", called)
	}
}

func TestGameCastNativeHostReadyResult(t *testing.T) {
	tests := []struct {
		name        string
		status      gamecastws.Status
		resolved    bool
		errContains string
	}{
		{
			name:     "media sending is ready",
			status:   gamecastws.Status{State: "connected", Detail: "native host sending media"},
			resolved: true,
		},
		{
			name:   "websocket connected is not media ready",
			status: gamecastws.Status{State: "connected", Detail: "native host connected"},
		},
		{
			name:   "intermediate state remains pending",
			status: gamecastws.Status{State: "connecting", Detail: "dialing native host"},
		},
		{
			name:        "error resolves with failure",
			status:      gamecastws.Status{State: "error", Detail: "codec unavailable"},
			resolved:    true,
			errContains: "codec unavailable",
		},
		{
			name:        "closed resolves with failure",
			status:      gamecastws.Status{State: "closed"},
			resolved:    true,
			errContains: "closed before media was ready",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := gameCastNativeHostReadyResult(test.status)
			if resolved != test.resolved {
				t.Fatalf("resolved = %t, want %t", resolved, test.resolved)
			}
			if test.errContains == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.errContains) {
				t.Fatalf("error = %v, want substring %q", err, test.errContains)
			}
		})
	}
}

func TestCloseGameCastWSInvalidatesCallbacksBeforeClientClose(t *testing.T) {
	application := &App{}
	generation := application.nextGameCastWSGeneration()
	staleCallbackAccepted := false
	client := gamecastws.NewClient(gamecastws.Options{
		OnStatus: func(gamecastws.Status) {
			staleCallbackAccepted = application.withCurrentGameCastWSGeneration(generation, func() {})
		},
	})
	if !application.setGameCastWSIfCurrent(generation, client) {
		t.Fatal("failed to install test GameCast websocket client")
	}

	application.closeGameCastWS()

	if staleCallbackAccepted {
		t.Fatal("expected close to invalidate the previous GameCast websocket generation")
	}
}

func TestStaleGameCastWSCompletionCannotClearReplacement(t *testing.T) {
	application := &App{}
	firstGeneration := application.nextGameCastWSGeneration()
	first := gamecastws.NewClient(gamecastws.Options{})
	if !application.setGameCastWSIfCurrent(firstGeneration, first) {
		t.Fatal("failed to install first GameCast websocket client")
	}

	application.closeGameCastWS()
	secondGeneration := application.nextGameCastWSGeneration()
	second := gamecastws.NewClient(gamecastws.Options{})
	if !application.setGameCastWSIfCurrent(secondGeneration, second) {
		t.Fatal("failed to install replacement GameCast websocket client")
	}

	if application.clearGameCastWSIfCurrent(firstGeneration, first) {
		t.Fatal("stale connection cleared the replacement GameCast websocket")
	}
	if !application.gameCastWSIsCurrent(secondGeneration, second) {
		t.Fatal("replacement GameCast websocket lost ownership")
	}
	application.closeGameCastWS()
}

func TestGameCastWSCallbacksRequireCurrentClientIdentity(t *testing.T) {
	application := &App{}
	generation := application.nextGameCastWSGeneration()
	current := gamecastws.NewClient(gamecastws.Options{})
	other := gamecastws.NewClient(gamecastws.Options{})
	if !application.setGameCastWSIfCurrent(generation, current) {
		t.Fatal("failed to install current GameCast websocket client")
	}

	if application.withCurrentGameCastWSClient(generation, other, func() {
		t.Fatal("callback from non-current client ran")
	}) {
		t.Fatal("expected same-generation callback from a different client to be rejected")
	}
	if !application.withCurrentGameCastWSClient(generation, current, func() {}) {
		t.Fatal("expected callback from the current generation and client to be accepted")
	}

	application.clearGameCastWSIfCurrent(generation, current)
	if application.withCurrentGameCastWSClient(generation, current, func() {
		t.Fatal("callback ran after current client was cleared")
	}) {
		t.Fatal("expected callback to be rejected after the current client was cleared")
	}
}

func TestGameCastControlRequestsUseCurrentParticipantAndLease(t *testing.T) {
	application := NewApp(network.NewLoopbackNetwork("self"))
	application.hostVideoCapability = video.BackendCapability{
		Backend:      video.BackendNative,
		SupportsHost: true,
		SupportsView: true,
	}
	var lease gamecast.ShareLease
	if err := json.Unmarshal([]byte(fmt.Sprintf(
		`{"room":"room-1","leaseId":"lease-1","participantId":%q,"active":true,"mediaMode":"sfu","phase":"starting"}`,
		application.gamecastParticipantID,
	)), &lease); err != nil {
		t.Fatalf("decode unified lease fixture: %v", err)
	}
	application.gamecastShareLease = &lease

	presence := application.gameCastPresenceRequest("room-1", "owner", "client")
	if presence.Room != "room-1" || presence.ClientType != "client" || presence.Origin != "client" || presence.Role != "owner" {
		t.Fatalf("presence request = %#v", presence)
	}
	presenceJSON := appJSONMap(t, presence)
	capabilities, _ := presenceJSON["capabilities"].(map[string]interface{})
	if presenceJSON["mediaProtocolVersion"] != float64(1) || capabilities["sfuViewer"] != true ||
		capabilities["sfuHost"] != true || capabilities["h264"] != true || capabilities["opus"] != true {
		t.Fatalf("unified presence request = %#v", presenceJSON)
	}
	leaseRequest := application.gameCastShareLeaseRequest("room-1", gamecast.SharePhaseStarting, "system")
	if leaseRequest.ParticipantID != application.gamecastParticipantID || leaseRequest.LeaseID != "lease-1" || !leaseRequest.SFU || leaseRequest.AudioMode != "system" {
		t.Fatalf("lease request = %#v", leaseRequest)
	}
	leaseJSON := appJSONMap(t, leaseRequest)
	if leaseJSON["mediaMode"] != "sfu" || leaseJSON["phase"] != "starting" {
		t.Fatalf("unified lease request = %#v", leaseJSON)
	}
}

func TestConnectGameCastViewerLeavesIdleUnifiedRoomPresenceOnly(t *testing.T) {
	var screenWSRequests atomic.Int32
	voiceSession := &fakeRoomVoiceSession{
		started: make(chan struct{}),
		state: gamecastvoice.State{
			Connection: gamecastvoice.ConnectionConnecting,
			Microphone: gamecastvoice.MicrophoneStarting,
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/presence/join":
			_, _ = w.Write([]byte(`{"ok":true,"participant":{"participantId":"client-test","room":"room-idle","clientType":"client","role":"member","active":true,"mediaProtocolVersion":1,"capabilities":{"sfuViewer":true,"sfuHost":true,"h264":true,"opus":true}},"room":{"room":"room-idle","active":true,"origin":"web","sharing":false,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":"idle","sfu":false,"audioMode":"off","ttl":15}}`))
		case "/api/room-state":
			_, _ = w.Write([]byte(`{"ok":true,"state":{"room":"room-idle","active":true,"origin":"web","sharing":false,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":"idle","sfu":false,"audioMode":"off","ttl":15}}`))
		case "/api/history":
			_, _ = w.Write([]byte(`{"ok":true,"history":[]}`))
		case "/api/viewer-intent":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/sfu-ws":
			screenWSRequests.Add(1)
			http.Error(w, "screen websocket must stay idle", http.StatusServiceUnavailable)
		default:
			http.Error(w, `{"error":"unexpected endpoint"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	jar, err := session.New(filepath.Join(t.TempDir(), "cookies.json"))
	if err != nil {
		t.Fatalf("create test cookie jar: %v", err)
	}
	application := NewApp(network.NewLoopbackNetwork("self"))
	application.cfg = config.DefaultConfig()
	application.cfg.GameCast.BaseURL = server.URL
	application.gamecastJar = jar
	application.gamecastParticipantID = "client-test"
	application.gamecastVoiceFactory = func(gamecastvoice.Config) roomVoiceSession {
		return voiceSession
	}
	defer application.stopGameCastPresence(false)

	joined, err := application.JoinGameCastRoom("room-idle")
	if err != nil {
		t.Fatalf("JoinGameCastRoom(idle): %v", err)
	}
	if joined.Role != "viewer" || joined.State.Origin != "web" || joined.State.SharePhase != gamecast.SharePhaseIdle {
		t.Fatalf("joined idle room = %#v", joined)
	}
	if err := application.ConnectGameCastViewer("room-idle", "auto"); err != nil {
		t.Fatalf("ConnectGameCastViewer(idle): %v", err)
	}
	select {
	case <-voiceSession.started:
	case <-time.After(time.Second):
		t.Fatal("voice session did not remain active in the idle room")
	}
	if screenWSRequests.Load() != 0 {
		t.Fatalf("idle room opened %d screen websocket connection(s)", screenWSRequests.Load())
	}
	application.mu.Lock()
	presenceActive := application.gamecastPresenceCancel != nil && application.gamecastPresenceRoom == "room-idle"
	screenClient := application.gamecastWS
	application.mu.Unlock()
	if !presenceActive || screenClient != nil {
		t.Fatalf("idle room state presenceActive=%t screenClient=%p", presenceActive, screenClient)
	}
}

func TestGameCastRoomLiveForViewerRequiresLiveUnifiedRemoteShare(t *testing.T) {
	live := GameCastRoomState{
		Room:                    "room-live",
		Active:                  true,
		Sharing:                 true,
		MediaMode:               gamecast.MediaModeSFU,
		MediaProtocolVersion:    gamecast.MediaProtocolVersion,
		SharePhase:              gamecast.SharePhaseLive,
		ShareOwnerParticipantID: "web-share",
		SFU:                     true,
	}
	if !gameCastRoomLiveForViewer(live, "client-self") {
		t.Fatal("expected a remote live unified share to open the viewer")
	}
	for name, state := range map[string]GameCastRoomState{
		"starting": func() GameCastRoomState {
			state := live
			state.SharePhase = gamecast.SharePhaseStarting
			state.SFU = false
			return state
		}(),
		"stopping": func() GameCastRoomState {
			state := live
			state.SharePhase = gamecast.SharePhaseStopping
			state.SFU = false
			return state
		}(),
		"idle": func() GameCastRoomState {
			state := live
			state.Sharing = false
			state.SharePhase = gamecast.SharePhaseIdle
			state.SFU = false
			return state
		}(),
		"legacy":     func() GameCastRoomState { state := live; state.MediaMode = gamecast.MediaModeLegacyP2P; return state }(),
		"old":        func() GameCastRoomState { state := live; state.MediaProtocolVersion = 0; return state }(),
		"self-share": func() GameCastRoomState { state := live; state.ShareOwnerParticipantID = "client-self"; return state }(),
	} {
		t.Run(name, func(t *testing.T) {
			if gameCastRoomLiveForViewer(state, "client-self") {
				t.Fatalf("unexpected viewer connection for %#v", state)
			}
		})
	}
}

func appJSONMap(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON object: %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode JSON object: %v", err)
	}
	return result
}

func TestNetworkStartTimeoutUsesLongerBudgetWithAuthKey(t *testing.T) {
	if got := networkStartTimeout(""); got != defaultNetworkStartTimeout {
		t.Fatalf("networkStartTimeout(empty) = %v, want %v", got, defaultNetworkStartTimeout)
	}

	if got := networkStartTimeout("tskey-auth-example"); got != authKeyNetworkStartTimeout {
		t.Fatalf("networkStartTimeout(auth) = %v, want %v", got, authKeyNetworkStartTimeout)
	}

	if authKeyNetworkStartTimeout <= defaultNetworkStartTimeout {
		t.Fatalf("expected auth timeout %v to be greater than default timeout %v", authKeyNetworkStartTimeout, defaultNetworkStartTimeout)
	}

	if authKeyNetworkStartTimeout < 10*time.Second {
		t.Fatalf("expected auth timeout to allow first-login startup, got %v", authKeyNetworkStartTimeout)
	}
}

func TestNormalizeJoinTargetStripsPortAndWrappers(t *testing.T) {
	tests := map[string]string{
		"100.89.178.4":                 "100.89.178.4",
		" 100.89.178.4:9000 ":          "100.89.178.4",
		"tcp://100.89.178.4:9000":      "100.89.178.4",
		"partylan://100.89.178.4:9000": "100.89.178.4",
		"[fd7a:115c:a1e0::1]:9000":     "fd7a:115c:a1e0::1",
	}

	for input, want := range tests {
		if got := normalizeJoinTarget(input); got != want {
			t.Fatalf("normalizeJoinTarget(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWebRTCBrowserTransportRejectsEmbeddedTsnet(t *testing.T) {
	ok, reason := webRTCBrowserTransportAvailable(network.NewNetworkedNetwork())
	if ok {
		t.Fatal("expected embedded tsnet backend to be rejected for browser WebRTC playback")
	}
	if reason == "" {
		t.Fatal("expected unavailable reason")
	}
}

func TestWebRTCBrowserTransportAllowsLoopback(t *testing.T) {
	ok, reason := webRTCBrowserTransportAvailable(network.NewLoopbackNetwork("self"))
	if !ok {
		t.Fatalf("expected loopback backend to be allowed, got reason %q", reason)
	}
}

func TestShouldDisableDirectWebRTCEncodeOnCaptureSizeMismatch(t *testing.T) {
	err := video.ErrNativeBackendUnavailable
	if shouldDisableDirectWebRTCEncode(err, 1) {
		t.Fatal("unexpected direct encode disable for generic first failure")
	}

	err = fmt.Errorf("%w: direct encode: capture size != encode target", video.ErrNativeBackendUnavailable)
	if !shouldDisableDirectWebRTCEncode(err, 1) {
		t.Fatal("expected capture/target size mismatch to disable direct encode immediately")
	}

	if !shouldDisableDirectWebRTCEncode(video.ErrNativeBackendUnavailable, 60) {
		t.Fatal("expected repeated direct encode failures to disable direct path")
	}
}

func TestPrepareWebRTCFrameForEncodeResizesToTarget(t *testing.T) {
	src := &video.Frame{
		Data:      make([]byte, 4*2*4),
		Timestamp: 123,
		Width:     4,
		Height:    2,
	}
	for i := range src.Data {
		src.Data[i] = byte(i)
	}

	got := prepareWebRTCFrameForEncode(src, 2, 2)

	if got == src {
		t.Fatal("expected resized frame to be a new frame")
	}
	if got.Width != 2 || got.Height != 2 {
		t.Fatalf("resized frame dimensions = %dx%d, want 2x2", got.Width, got.Height)
	}
	if got.Timestamp != src.Timestamp {
		t.Fatalf("resized frame timestamp = %d, want %d", got.Timestamp, src.Timestamp)
	}
	if len(got.Data) != 2*2*4 {
		t.Fatalf("resized frame data length = %d, want %d", len(got.Data), 2*2*4)
	}
}

func TestShouldUseDirectNativeEncodeOnlyWhenCaptureAndTargetMatch(t *testing.T) {
	if !shouldUseDirectNativeEncode(1280, 720, 1280, 720) {
		t.Fatal("expected matching capture/target size to allow direct native encode")
	}
	if shouldUseDirectNativeEncode(1920, 1080, 1280, 720) {
		t.Fatal("expected resize target to disable direct native encode")
	}
	if shouldUseDirectNativeEncode(0, 1080, 1280, 720) {
		t.Fatal("expected invalid capture dimensions to disable direct native encode")
	}
}

func TestGameCastSharePeerIDUsesExplicitCompatPublisher(t *testing.T) {
	request := GameCastHostShareRequest{PeerID: "gamecast-viewer"}
	if got := gameCastSharePeerID(request); got != "gamecast-viewer" {
		t.Fatalf("gameCastSharePeerID(explicit) = %q, want gamecast-viewer", got)
	}
}

func TestGameCastSharePeerIDDefaultsToNativeHost(t *testing.T) {
	if got := gameCastSharePeerID(GameCastHostShareRequest{}); got != "gamecast-host" {
		t.Fatalf("gameCastSharePeerID(default) = %q, want gamecast-host", got)
	}
}

func TestOriginalShareResolutionPreservesSourceAt60FPSWithoutUpscaling(t *testing.T) {
	width, height := resolveShareResolution(3841, 2161, 0, 0)
	if width != 3840 || height != 2160 {
		t.Fatalf("original resolution = %dx%d, want even 3840x2160", width, height)
	}
	if fps := normalizeShareFPS(60); fps != 60 {
		t.Fatalf("normalized original FPS = %d, want 60", fps)
	}

	width, height = resolveShareResolution(854, 480, 1920, 1080)
	if width > 854 || height > 480 {
		t.Fatalf("fixed request upscaled low-resolution source to %dx%d", width, height)
	}
}

func TestPresenceManagedGameCastRoomRequiresActiveMatchingPresence(t *testing.T) {
	if !presenceManagesGameCastRoom(true, " admin ", "ADMIN") {
		t.Fatal("expected active matching presence to manage the room")
	}
	if presenceManagesGameCastRoom(false, "admin", "admin") {
		t.Fatal("expected inactive presence not to manage the room")
	}
	if presenceManagesGameCastRoom(true, "other", "admin") {
		t.Fatal("expected a different presence room not to manage the room")
	}
}

func TestEnterGameCastHostRoomDoesNotWriteLegacyRoomState(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 4)
	leaseTransitions := make([]string, 0, 4)
	shareActive := false
	sharePhase := gamecast.SharePhaseIdle
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(`{"user":"admin","loggedIn":true}`))
		case "/api/presence/join":
			_, _ = w.Write([]byte(`{"ok":true,"participant":{"participantId":"client-test","room":"admin","clientType":"client","role":"owner","active":true,"mediaProtocolVersion":1,"capabilities":{"sfuViewer":true,"sfuHost":true,"h264":true,"opus":true}},"room":{"room":"admin","active":true,"origin":"client","ownerParticipantId":"client-test","ownerName":"admin","sharing":false,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":"idle","sfu":false,"audioMode":"off","ttl":15}}`))
		case "/api/history":
			_, _ = w.Write([]byte(`{"ok":true,"history":[]}`))
		case "/api/share/claim":
			payload := decodeRequestJSONMap(t, r)
			leaseTransitions = append(leaseTransitions, fmt.Sprintf("claim:%s:%s", payload["phase"], payload["mediaMode"]))
			shareActive = true
			sharePhase = gamecast.SharePhaseStarting
			_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":true,"mediaMode":"sfu","phase":"starting","sfu":false,"audioMode":"off","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":true,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":"starting","sfu":false,"audioMode":"off"}}`))
		case "/api/share/heartbeat":
			payload := decodeRequestJSONMap(t, r)
			phase := fmt.Sprint(payload["phase"])
			leaseTransitions = append(leaseTransitions, fmt.Sprintf("heartbeat:%s:%s", phase, payload["mediaMode"]))
			shareActive = true
			sharePhase = phase
			sfu := phase == gamecast.SharePhaseLive
			_, _ = fmt.Fprintf(w, `{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":true,"mediaMode":"sfu","phase":%q,"sfu":%t,"audioMode":"system","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":true,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":%q,"sfu":%t,"audioMode":"system"}}`, phase, sfu, phase, sfu)
		case "/api/share/release":
			payload := decodeRequestJSONMap(t, r)
			leaseTransitions = append(leaseTransitions, fmt.Sprintf("release:%s:%s", payload["phase"], payload["mediaMode"]))
			shareActive = false
			sharePhase = gamecast.SharePhaseIdle
			_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":false,"mediaMode":"sfu","phase":"stopping","sfu":false,"audioMode":"off","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":false,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":"idle","sfu":false,"audioMode":"off"}}`))
		case "/api/room-state":
			if r.Method != http.MethodGet {
				http.Error(w, `{"code":"room_origin_conflict"}`, http.StatusConflict)
				return
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"state":{"room":"admin","active":true,"origin":"client","sharing":%t,"mediaMode":"sfu","mediaProtocolVersion":1,"sharePhase":%q,"sfu":%t,"audioMode":"off"}}`, shareActive, sharePhase, shareActive && sharePhase == gamecast.SharePhaseLive)
		default:
			http.Error(w, `{"error":"unexpected endpoint"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("USERPROFILE", t.TempDir())
	jar, err := session.New(t.TempDir() + "/cookies.json")
	if err != nil {
		t.Fatalf("create test cookie jar: %v", err)
	}
	application := NewApp(network.NewLoopbackNetwork("self"))
	application.cfg = config.DefaultConfig()
	application.cfg.GameCast.BaseURL = server.URL
	application.gamecastJar = jar
	application.gamecastParticipantID = "client-test"
	defer application.stopGameCastPresence(false)

	room, err := application.enterGameCastHostRoom("admin")
	if err != nil {
		t.Fatalf("enterGameCastHostRoom: %v", err)
	}
	if room.State.Origin != "client" || !room.State.Active {
		t.Fatalf("room state = %#v, want active client room", room.State)
	}
	if _, _, err := application.claimGameCastShare("admin", "off"); err != nil {
		t.Fatalf("claimGameCastShare: %v", err)
	}
	sharing, err := application.SetGameCastHostSharing(GameCastHostShareRequest{
		RoomID: "admin", Sharing: true, AudioMode: "system",
	})
	if err != nil || !sharing.Sharing || !sharing.SFU {
		t.Fatalf("start modern share state = %#v err=%v", sharing, err)
	}
	stopped, err := application.SetGameCastHostSharing(GameCastHostShareRequest{
		RoomID: "admin", Sharing: false, AudioMode: "off",
	})
	if err != nil || stopped.Sharing || stopped.SFU {
		t.Fatalf("stop modern share state = %#v err=%v", stopped, err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, request := range requests {
		if request == "POST /api/room-state" {
			t.Fatalf("modern room entry unexpectedly wrote legacy state: %v", requests)
		}
	}
	wantTransitions := []string{
		"claim:starting:sfu",
		"heartbeat:live:sfu",
		"heartbeat:stopping:sfu",
		"release:stopping:sfu",
	}
	if strings.Join(leaseTransitions, ",") != strings.Join(wantTransitions, ",") {
		t.Fatalf("share lifecycle = %v, want %v", leaseTransitions, wantTransitions)
	}
}

func decodeRequestJSONMap(t *testing.T, request *http.Request) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request JSON: %v", err)
	}
	return payload
}

type fakeRoomVoiceSession struct {
	started chan struct{}
	stopped atomic.Bool
	state   gamecastvoice.State
}

func (s *fakeRoomVoiceSession) Start(context.Context) error {
	close(s.started)
	return nil
}

func (s *fakeRoomVoiceSession) Stop() {
	s.stopped.Store(true)
}

func (s *fakeRoomVoiceSession) Snapshot() gamecastvoice.State {
	return s.state
}

func (s *fakeRoomVoiceSession) SetMicrophoneEnabled(enabled bool) gamecastvoice.State {
	if enabled {
		s.state.Microphone = gamecastvoice.MicrophoneLive
	} else {
		s.state.Microphone = gamecastvoice.MicrophoneMuted
	}
	return s.state
}

func (s *fakeRoomVoiceSession) SetPlaybackMuted(muted bool) gamecastvoice.State {
	s.state.PlaybackMuted = muted
	return s.state
}

func TestGameCastPresenceStartsVoiceAndStopsItBeforeLeave(t *testing.T) {
	voiceSession := &fakeRoomVoiceSession{
		started: make(chan struct{}),
		state: gamecastvoice.State{
			Connection: gamecastvoice.ConnectionConnecting,
			Microphone: gamecastvoice.MicrophoneStarting,
		},
	}
	leaveObserved := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/presence/join":
			_, _ = w.Write([]byte(`{"ok":true,"participant":{"participantId":"client-test","room":"room-voice","clientType":"client","role":"member","active":true},"room":{"room":"room-voice","active":true,"origin":"client","sharing":false,"sfu":false,"audioMode":"off","ttl":15}}`))
		case "/api/presence/leave":
			leaveObserved <- voiceSession.stopped.Load()
			_, _ = w.Write([]byte(`{"ok":true,"participant":{"participantId":"client-test","room":"room-voice","clientType":"client","role":"member","active":false}}`))
		default:
			http.Error(w, `{"error":"unexpected endpoint"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	jar, err := session.New(filepath.Join(t.TempDir(), "cookies.json"))
	if err != nil {
		t.Fatalf("create test cookie jar: %v", err)
	}
	application := NewApp(network.NewLoopbackNetwork("self"))
	application.cfg = config.DefaultConfig()
	application.cfg.GameCast.BaseURL = server.URL
	application.gamecastJar = jar
	application.gamecastParticipantID = "client-test"
	application.gamecastVoiceFactory = func(gamecastvoice.Config) roomVoiceSession {
		return voiceSession
	}

	if _, _, err := application.startGameCastPresence("room-voice", "member", "client"); err != nil {
		t.Fatalf("startGameCastPresence: %v", err)
	}
	select {
	case <-voiceSession.started:
	case <-time.After(time.Second):
		t.Fatal("voice session did not start after Presence join")
	}
	application.stopGameCastPresence(true)
	select {
	case stopped := <-leaveObserved:
		if !stopped {
			t.Fatal("Presence leave was sent before the voice session stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("Presence leave was not sent")
	}
}

func TestNormalizeGameCastLayerKeepsAutomaticAndExplicitLayers(t *testing.T) {
	for input, want := range map[string]string{
		"":     "auto",
		"auto": "auto",
		"q":    "q",
		"H":    "h",
		"f":    "f",
		"bad":  "auto",
	} {
		if got := normalizeGameCastLayer(input); got != want {
			t.Fatalf("normalizeGameCastLayer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGameCastWindowCaptureRetriesTemporaryFrameLoss(t *testing.T) {
	err := fmt.Errorf("capture frame: %w", hostwindowcapture.ErrFrameUnavailable)
	if !shouldRetryGameCastWindowCapture(err) {
		t.Fatal("expected temporary window frame loss to be retried")
	}
}

func TestGameCastWindowCaptureStopsWhenWindowCloses(t *testing.T) {
	err := fmt.Errorf("capture frame: %w", hostwindowcapture.ErrWindowClosed)
	if shouldRetryGameCastWindowCapture(err) {
		t.Fatal("expected a closed window to stop capture")
	}
	if !errors.Is(err, hostwindowcapture.ErrWindowClosed) {
		t.Fatal("expected the closed-window cause to remain discoverable")
	}
}

func TestGameCastDisplayCaptureRetriesTemporaryFrameLoss(t *testing.T) {
	err := fmt.Errorf("capture frame: %w", video.ErrCaptureFrameUnavailable)
	if !shouldRetryGameCastDisplayCapture(err) {
		t.Fatal("expected temporary display frame loss to be retried")
	}
}

func TestGameCastDisplayCaptureStopsOnPermanentFailure(t *testing.T) {
	if shouldRetryGameCastDisplayCapture(video.ErrNativeBackendUnavailable) {
		t.Fatal("expected a permanent display capture failure to stop capture")
	}
}

func TestChooseGameCastVideoFrameKeepsLastFrameWhenCaptureHasNoUpdate(t *testing.T) {
	first := &video.Frame{Data: []byte{1, 2, 3, 4}, Timestamp: 10, Width: 1, Height: 1}
	if got := chooseGameCastVideoFrame(nil, nil); got != nil {
		t.Fatalf("frame before first capture = %#v, want nil", got)
	}
	if got := chooseGameCastVideoFrame(nil, first); got != first {
		t.Fatalf("first captured frame = %#v, want original frame", got)
	}
	if got := chooseGameCastVideoFrame(first, nil); got != first {
		t.Fatalf("frame during temporary capture gap = %#v, want last frame", got)
	}
	second := &video.Frame{Data: []byte{5, 6, 7, 8}, Timestamp: 20, Width: 1, Height: 1}
	if got := chooseGameCastVideoFrame(first, second); got != second {
		t.Fatalf("frame after capture recovery = %#v, want newest frame", got)
	}
}

func TestGameCastQualityReportFromPayloadRequiresValidViewerDeltas(t *testing.T) {
	payload := map[string]interface{}{
		"room": "room-1", "participantId": "viewer-1", "role": "viewer",
		"valid": true, "generation": "g1", "pairId": "p1",
		"packetsReceivedDelta": float64(100), "packetsLostDelta": float64(7),
		"framesDroppedDelta": float64(2), "decodedFps": float64(18),
		"incomingBitrateKbps": float64(1200), "jitterMs": float64(130),
	}

	report, ok := gameCastQualityReportFromPayload(payload)
	if !ok {
		t.Fatal("expected valid quality report")
	}
	if report.Room != "room-1" || report.ParticipantID != "viewer-1" || report.PacketsLostDelta != 7 {
		t.Fatalf("report = %#v", report)
	}

	invalidCases := []map[string]interface{}{
		{"valid": false},
		{"valid": true, "role": "host"},
		{"valid": true, "role": "viewer", "packetsLostDelta": float64(-1)},
	}
	for _, override := range invalidCases {
		copy := make(map[string]interface{}, len(payload))
		for key, item := range payload {
			copy[key] = item
		}
		for key, item := range override {
			copy[key] = item
		}
		if _, valid := gameCastQualityReportFromPayload(copy); valid {
			t.Fatalf("payload unexpectedly produced a quality report: %#v", copy)
		}
	}
}

func TestH264PreparedAccessUnitRequiresParameterSetsAndIDR(t *testing.T) {
	annexB := func(nalType byte) []byte { return []byte{0, 0, 0, 1, nalType} }
	valid := append(annexB(0x67), annexB(0x68)...)
	valid = append(valid, annexB(0x65)...)
	if !h264PreparedAccessUnit(valid) {
		t.Fatal("SPS/PPS/IDR access unit should be accepted")
	}
	if h264PreparedAccessUnit(append(annexB(0x67), annexB(0x61)...)) {
		t.Fatal("access unit without PPS and IDR should be rejected")
	}
}

func TestEffectiveAdaptiveProfileUsesFullSingleViewerAndDividesConfiguredBudget(t *testing.T) {
	profile := adaptive.Profile{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 6000}
	if got := effectiveAdaptiveProfile(profile, 0, 3); got.BitrateKbps != 6000 {
		t.Fatalf("unconfigured budget changed bitrate: %#v", got)
	}
	if got := effectiveAdaptiveProfile(profile, 12000, 1); got.BitrateKbps != 6000 {
		t.Fatalf("single viewer should keep full profile bitrate: %#v", got)
	}
	if got := effectiveAdaptiveProfile(profile, 12000, 3); got.BitrateKbps != 4000 {
		t.Fatalf("three viewers bitrate = %d, want 4000", got.BitrateKbps)
	}
}

func TestGameCastKeyFrameGateCoalescesStartupRequests(t *testing.T) {
	startedAt := time.Unix(100, 0)
	gate := newGameCastKeyFrameGate(startedAt)

	if !gate.Allow(startedAt) {
		t.Fatal("expected first keyframe request to pass")
	}
	if gate.Allow(startedAt.Add(500 * time.Millisecond)) {
		t.Fatal("expected duplicate startup keyframe request to be coalesced")
	}
	if !gate.Allow(startedAt.Add(1600 * time.Millisecond)) {
		t.Fatal("expected startup keyframe request after 1.5s to pass")
	}
	steady := startedAt.Add(6 * time.Second)
	if !gate.Allow(steady) {
		t.Fatal("expected first steady-state request to pass")
	}
	if gate.Allow(steady.Add(100 * time.Millisecond)) {
		t.Fatal("expected 100ms steady-state duplicate to be coalesced")
	}
	if !gate.Allow(steady.Add(350 * time.Millisecond)) {
		t.Fatal("expected steady-state request after 300ms to pass")
	}
}

func TestGameCastAudioTickUsesSilenceUntilPCMStarts(t *testing.T) {
	silence, err := gameCastAudioSamplesForTick(nil)
	if err != nil {
		t.Fatalf("gameCastAudioSamplesForTick(nil): %v", err)
	}
	wantValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
	if len(silence) != wantValues {
		t.Fatalf("silent sample values = %d, want %d", len(silence), wantValues)
	}
	for index, sample := range silence {
		if sample != 0 {
			t.Fatalf("silent sample[%d] = %d, want 0", index, sample)
		}
	}

	frame := &windowsaudio.PCMFrame{
		Data:       []byte{1, 0, 254, 255},
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}
	samples, err := gameCastAudioSamplesForTick(frame)
	if err != nil {
		t.Fatalf("gameCastAudioSamplesForTick(frame): %v", err)
	}
	if len(samples) != 2 || samples[0] != 1 || samples[1] != -2 {
		t.Fatalf("real PCM samples = %#v, want [1 -2]", samples)
	}
}

type fakeGameCastPCMSource struct {
	frames chan windowsaudio.PCMFrame
	errors chan error
}

func (s *fakeGameCastPCMSource) Frames() <-chan windowsaudio.PCMFrame { return s.frames }
func (s *fakeGameCastPCMSource) Errors() <-chan error                 { return s.errors }
func (s *fakeGameCastPCMSource) Stop()                                {}

type fakeGameCastOpusEncoder struct {
	mu      sync.Mutex
	inputs  [][]int16
	packets int
}

func (e *fakeGameCastOpusEncoder) EncodePCM16(samples []int16) ([][]byte, error) {
	e.mu.Lock()
	e.inputs = append(e.inputs, append([]int16(nil), samples...))
	e.packets++
	packet := []byte{byte(e.packets)}
	e.mu.Unlock()
	return [][]byte{packet}, nil
}

func (e *fakeGameCastOpusEncoder) FrameDuration() time.Duration {
	return audio.DefaultOpusFrameMillis * time.Millisecond
}

func (e *fakeGameCastOpusEncoder) Close() error { return nil }

type fakeOpusSampleSink struct {
	writes chan []byte
}

func (s *fakeOpusSampleSink) WriteOpusSample(data []byte, _ time.Duration) error {
	s.writes <- append([]byte(nil), data...)
	return nil
}

func TestRunOpusAudioLoopSendsSilenceThenLivePCMWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeGameCastPCMSource{
		frames: make(chan windowsaudio.PCMFrame, 1),
		errors: make(chan error),
	}
	encoder := &fakeGameCastOpusEncoder{}
	sink := &fakeOpusSampleSink{writes: make(chan []byte, 4)}
	done := make(chan struct{})
	application := NewApp(network.NewLoopbackNetwork("self"))
	go application.runOpusAudioLoop(ctx, sink, source, encoder, done, nil)

	select {
	case <-sink.writes:
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("timed out waiting for silent Opus keepalive")
	}

	frameValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
	liveData := make([]byte, frameValues*2)
	liveData[0] = 7
	source.frames <- windowsaudio.PCMFrame{
		Data:       liveData,
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}

	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case <-sink.writes:
			encoder.mu.Lock()
			inputs := append([][]int16(nil), encoder.inputs...)
			encoder.mu.Unlock()
			for _, input := range inputs {
				if len(input) > 0 && input[0] == 7 {
					cancel()
					<-done
					return
				}
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("live PCM did not replace silence without restarting the audio loop")
		}
	}
}

func TestRunOpusAudioLoopKeepsPartialPCMUntilACompleteFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeGameCastPCMSource{
		frames: make(chan windowsaudio.PCMFrame, 2),
		errors: make(chan error),
	}
	encoder := &fakeGameCastOpusEncoder{}
	sink := &fakeOpusSampleSink{writes: make(chan []byte, 8)}
	done := make(chan struct{})
	application := NewApp(network.NewLoopbackNetwork("self"))
	go application.runOpusAudioLoop(ctx, sink, source, encoder, done, nil)

	waitForAudioWrite := func(label string) {
		t.Helper()
		select {
		case <-sink.writes:
		case <-time.After(250 * time.Millisecond):
			cancel()
			<-done
			t.Fatalf("timed out waiting for %s audio tick", label)
		}
	}

	// Synchronize immediately after a tick so each half-frame arrives well before
	// the next one. System loopback commonly delivers PCM in sub-20ms packets.
	waitForAudioWrite("initial")
	frameValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
	halfValues := frameValues / 2
	firstHalf := make([]int16, halfValues)
	secondHalf := make([]int16, halfValues)
	for index := range firstHalf {
		firstHalf[index] = 101
		secondHalf[index] = 202
	}
	source.frames <- windowsaudio.PCMFrame{
		Data:       pcm16SamplesToBytes(firstHalf),
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}
	waitForAudioWrite("partial")
	source.frames <- windowsaudio.PCMFrame{
		Data:       pcm16SamplesToBytes(secondHalf),
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}
	waitForAudioWrite("complete")

	cancel()
	<-done
	encoder.mu.Lock()
	inputs := append([][]int16(nil), encoder.inputs...)
	encoder.mu.Unlock()

	var live []int16
	for _, input := range inputs {
		for _, sample := range input {
			if sample != 0 {
				live = input
				break
			}
		}
		if live != nil {
			break
		}
	}
	if live == nil {
		t.Fatal("expected a live PCM frame")
	}
	for index := 0; index < halfValues; index++ {
		if live[index] != 101 {
			t.Fatalf("live first half sample[%d] = %d, want 101", index, live[index])
		}
	}
	for index := halfValues; index < frameValues; index++ {
		if live[index] != 202 {
			t.Fatalf("live second half sample[%d] = %d, want 202", index, live[index])
		}
	}
}

func TestAudioSamplesForOpusTickDrainsQueuedSystemMixPackets(t *testing.T) {
	frameValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
	halfValues := frameValues / 2
	firstHalf := make([]int16, halfValues)
	secondHalf := make([]int16, halfValues)
	for index := range firstHalf {
		firstHalf[index] = 303
		secondHalf[index] = 404
	}
	frames := make(chan windowsaudio.PCMFrame, 2)
	frames <- windowsaudio.PCMFrame{
		Data:       pcm16SamplesToBytes(firstHalf),
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}
	frames <- windowsaudio.PCMFrame{
		Data:       pcm16SamplesToBytes(secondHalf),
		Channels:   audio.DefaultOpusChannels,
		SampleRate: audio.DefaultOpusSampleRate,
	}

	pending := make([]int16, 0, frameValues)
	samples, nextPending, open, err := audioSamplesForOpusTick(frames, pending, frameValues)
	if err != nil {
		t.Fatalf("audioSamplesForOpusTick: %v", err)
	}
	if !open {
		t.Fatal("expected PCM source to remain open")
	}
	if len(nextPending) != 0 {
		t.Fatalf("pending samples = %d, want 0", len(nextPending))
	}
	for index := 0; index < halfValues; index++ {
		if samples[index] != 303 {
			t.Fatalf("first queued half sample[%d] = %d, want 303", index, samples[index])
		}
	}
	for index := halfValues; index < frameValues; index++ {
		if samples[index] != 404 {
			t.Fatalf("second queued half sample[%d] = %d, want 404", index, samples[index])
		}
	}
}

func pcm16SamplesToBytes(samples []int16) []byte {
	data := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(data[index*2:], uint16(sample))
	}
	return data
}

func TestSelectGameCastProcessAudioTargetPrefersRequestedPID(t *testing.T) {
	resolverCalled := false
	target, ok := selectGameCastProcessAudioTarget(4242, "msedge.exe", func(string) (audioCaptureTarget, bool) {
		resolverCalled = true
		return audioCaptureTarget{PID: 9999}, true
	})
	if !ok {
		t.Fatal("expected requested process target to resolve")
	}
	if resolverCalled {
		t.Fatal("process-name resolver should not run when an explicit PID is available")
	}
	if target.PID != 4242 || target.Name != "msedge.exe" || target.Mode != windowsaudio.CaptureIncludeProcessTree {
		t.Fatalf("requested process target = %#v", target)
	}
}

func TestWaitForMediaLoopDoneTimesOut(t *testing.T) {
	done := make(chan struct{})
	started := time.Now()
	if waitForMediaLoopDone(done, 20*time.Millisecond) {
		t.Fatal("expected unfinished media loop wait to time out")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("media loop timeout took %v, want a bounded wait", elapsed)
	}
}

func TestWaitForMediaLoopDoneReturnsWhenClosed(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if !waitForMediaLoopDone(done, time.Second) {
		t.Fatal("expected closed media loop to report completion")
	}
}

func TestGameCastAudioStartupDoesNotFallbackToSystemMix(t *testing.T) {
	wantErr := errors.New("selected process audio is unavailable")
	attempts := 0
	mode, enabled, err := startGameCastAudioTarget(func(target audioCaptureTarget) error {
		attempts++
		if target.Mode != windowsaudio.CaptureIncludeProcessTree || target.PID != 42 {
			t.Fatalf("unexpected audio target: %#v", target)
		}
		return wantErr
	}, audioCaptureTarget{
		PID:  42,
		Name: "player.exe",
		Mode: windowsaudio.CaptureIncludeProcessTree,
	}, "process")

	if attempts != 1 {
		t.Fatalf("audio startup attempts = %d, want exactly one selected-target attempt", attempts)
	}
	if mode != "off" || enabled {
		t.Fatalf("failed selected audio result = (%q, %t), want (off, false)", mode, enabled)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("audio startup error = %v, want %v", err, wantErr)
	}
}

func TestResolveGameCastDisplayAudioExcludesPartyLANRegardlessOfRequestedMode(t *testing.T) {
	ensureTestOpusDLL(t)
	resolver := &scriptedShareAudioResolver{}
	application := &App{
		audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
		gamecastShareAudioResolver: resolver,
		windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
			t.Fatal("display audio must not resolve a window process")
			return sharecatalog.WindowProcess{}, nil
		},
	}
	session := &shareSession{sourceKind: "display"}

	target, enabled, mode := application.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{
		AudioMode:      "off",
		AudioProcessID: 4242,
	}, session)

	if !enabled {
		t.Fatal("expected safe display audio capture to be enabled")
	}
	if target.Mode != windowsaudio.CaptureExcludeProcessTree {
		t.Fatalf("display capture mode = %v, want CaptureExcludeProcessTree", target.Mode)
	}
	if target.PID != uint32(os.Getpid()) {
		t.Fatalf("display exclusion PID = %d, want current PID %d", target.PID, os.Getpid())
	}
	if mode != "exclude-process" {
		t.Fatalf("display audio mode = %q, want exclude-process", mode)
	}
	if len(resolver.inputs) != 0 {
		t.Fatalf("display audio unexpectedly called window resolver: %#v", resolver.inputs)
	}
}

func TestResolveGameCastWindowAudioIgnoresSystemOverride(t *testing.T) {
	ensureTestOpusDLL(t)
	windowProcessID := uint32(4242)
	applicationRootPID := uint32(4200)
	resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{{
		ProcessID:      applicationRootPID,
		Mode:           windowsaudio.CaptureIncludeProcessTree,
		Identity:       `c:\apps\game\game.exe`,
		IdentityStable: true,
		Reason:         shareaudio.ReasonSiblingAudio,
	}}}
	application := &App{
		audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
		gamecastShareAudioResolver: resolver,
		windowProcessResolver: func(handle uintptr) (sharecatalog.WindowProcess, error) {
			if handle != 77 {
				t.Fatalf("window handle = %d, want 77", handle)
			}
			return sharecatalog.WindowProcess{ProcessID: windowProcessID, ProcessName: "game.exe"}, nil
		},
	}
	session := &shareSession{sourceKind: "window", windowHandle: 77, audioProcessID: 9999, audioProcessName: "stale.exe"}

	target, enabled, mode := application.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{
		AudioMode:        "system",
		AudioProcessID:   9999,
		AudioProcessName: "untrusted.exe",
	}, session)

	if !enabled {
		t.Fatal("expected selected window process audio to be enabled")
	}
	if target.Mode != windowsaudio.CaptureIncludeProcessTree || target.PID != applicationRootPID {
		t.Fatalf("window audio target = %#v, want process tree PID %d", target, applicationRootPID)
	}
	if mode != "process" {
		t.Fatalf("window audio mode = %q, want process", mode)
	}
	if target.ApplicationIdentity != `c:\apps\game\game.exe` || !target.IdentityStable || target.Reason != shareaudio.ReasonSiblingAudio {
		t.Fatalf("window audio identity metadata = %#v", target)
	}
	if len(resolver.inputs) != 1 || resolver.inputs[0].WindowPID != windowProcessID || resolver.inputs[0].PartyLANPID != uint32(os.Getpid()) {
		t.Fatalf("resolver inputs = %#v", resolver.inputs)
	}
}

func TestResolveGameCastWindowAudioDisablesUnknownAndPartyLANProcesses(t *testing.T) {
	ensureTestOpusDLL(t)

	for name, resolveWindow := range map[string]func(uintptr) (sharecatalog.WindowProcess, error){
		"unknown": func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{}, errors.New("window unavailable")
		},
		"partylan": func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{ProcessID: uint32(os.Getpid()), ProcessName: "PartyLAN.exe"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &scriptedShareAudioResolver{errs: []error{shareaudio.ErrUnsafeTarget}}
			application := &App{
				audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
				gamecastShareAudioResolver: resolver,
				windowProcessResolver:      resolveWindow,
			}
			session := &shareSession{sourceKind: "window", windowHandle: 88}
			target, enabled, mode := application.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{
				AudioMode:      "system",
				AudioProcessID: 4242,
			}, session)
			if enabled || mode != "off" || target.PID != 0 {
				t.Fatalf("unsafe window audio resolved to target=%#v enabled=%t mode=%q", target, enabled, mode)
			}
		})
	}
}

func TestResolveGameCastWindowAudioDisablesExitedProcess(t *testing.T) {
	ensureTestOpusDLL(t)
	application := &App{
		audioCapability: windowsaudio.Capability{SupportsProcessCapture: true},
		windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{}, errors.New("window process exited")
		},
	}
	session := &shareSession{sourceKind: "window", windowHandle: 99, audioProcessID: ^uint32(0), audioProcessName: "exited.exe"}

	target, enabled, mode := application.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{}, session)
	if enabled || mode != "off" || target.PID != 0 {
		t.Fatalf("exited window process resolved to target=%#v enabled=%t mode=%q", target, enabled, mode)
	}
}

func TestGameCastWindowAudioStartupRefreshesOnceForStableApplication(t *testing.T) {
	ensureTestOpusDLL(t)
	resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{{
		ProcessID:      200,
		Mode:           windowsaudio.CaptureIncludeProcessTree,
		Identity:       `c:\apps\player\player.exe`,
		IdentityStable: true,
		Reason:         shareaudio.ReasonSiblingAudio,
	}}}
	attempts := make([]audioCaptureTarget, 0, 2)
	application := &App{
		audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
		gamecastShareAudioResolver: resolver,
		windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{ProcessID: 110, ProcessName: "player.exe"}, nil
		},
		gamecastAudioLoopStarter: func(_ *nativehost.Host, _ *shareSession, target audioCaptureTarget) error {
			attempts = append(attempts, target)
			if len(attempts) == 1 {
				return errors.New("initial process capture start failed")
			}
			return nil
		},
	}
	session := &shareSession{sourceKind: "window", windowHandle: 77}
	primary := stableWindowAudioTarget(100, `c:\apps\player\player.exe`)

	mode, enabled, err := application.startGameCastAudioLoopOrDisable(nil, session, primary, "process")
	if err != nil || !enabled || mode != "process" {
		t.Fatalf("startup result = (%q, %t, %v), want successful process retry", mode, enabled, err)
	}
	if len(attempts) != 2 || attempts[0].PID != 100 || attempts[1].PID != 200 {
		t.Fatalf("startup attempts = %#v, want original then refreshed application root", attempts)
	}
	if len(resolver.inputs) != 1 {
		t.Fatalf("resolver calls = %d, want one refresh", len(resolver.inputs))
	}
	if session.audioTarget.PID != 200 || session.audioRebindAttempted {
		t.Fatalf("stored session audio state = %#v rebind=%t", session.audioTarget, session.audioRebindAttempted)
	}
}

func TestGameCastWindowAudioStartupStopsAfterOneFailedRefreshRetry(t *testing.T) {
	ensureTestOpusDLL(t)
	resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{{
		ProcessID:      200,
		Mode:           windowsaudio.CaptureIncludeProcessTree,
		Identity:       `c:\apps\player\player.exe`,
		IdentityStable: true,
		Reason:         shareaudio.ReasonSiblingAudio,
	}}}
	attempts := 0
	application := &App{
		audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
		gamecastShareAudioResolver: resolver,
		windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{ProcessID: 110, ProcessName: "player.exe"}, nil
		},
		gamecastAudioLoopStarter: func(_ *nativehost.Host, _ *shareSession, target audioCaptureTarget) error {
			attempts++
			if target.Mode == windowsaudio.CaptureSystemMix {
				t.Fatal("startup attempted forbidden system-mix fallback")
			}
			return fmt.Errorf("capture attempt %d failed", attempts)
		},
	}
	session := &shareSession{sourceKind: "window", windowHandle: 77}

	mode, enabled, err := application.startGameCastAudioLoopOrDisable(
		nil,
		session,
		stableWindowAudioTarget(100, `c:\apps\player\player.exe`),
		"process",
	)
	if err == nil || enabled || mode != "off" {
		t.Fatalf("startup result = (%q, %t, %v), want video-only failure", mode, enabled, err)
	}
	if attempts != 2 || len(resolver.inputs) != 1 {
		t.Fatalf("startup attempts=%d resolver refreshes=%d, want 2 and 1", attempts, len(resolver.inputs))
	}
}

func TestGameCastWindowAudioStartupRejectsDifferentOrUnstableRetry(t *testing.T) {
	ensureTestOpusDLL(t)
	tests := []struct {
		name        string
		primary     audioCaptureTarget
		refreshed   shareaudio.Target
		wantRefresh bool
	}{
		{
			name:    "different application",
			primary: stableWindowAudioTarget(100, `c:\apps\player\player.exe`),
			refreshed: shareaudio.Target{ProcessID: 200, Mode: windowsaudio.CaptureIncludeProcessTree,
				Identity: `c:\apps\other\other.exe`, IdentityStable: true},
			wantRefresh: true,
		},
		{
			name:        "unstable original identity",
			primary:     audioCaptureTarget{PID: 100, Mode: windowsaudio.CaptureIncludeProcessTree},
			wantRefresh: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{test.refreshed}}
			attempts := 0
			application := &App{
				audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
				gamecastShareAudioResolver: resolver,
				windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
					return sharecatalog.WindowProcess{ProcessID: 110, ProcessName: "player.exe"}, nil
				},
				gamecastAudioLoopStarter: func(_ *nativehost.Host, _ *shareSession, target audioCaptureTarget) error {
					attempts++
					if target.Mode == windowsaudio.CaptureSystemMix {
						t.Fatal("startup attempted forbidden system-mix fallback")
					}
					return errors.New("capture unavailable")
				},
			}
			session := &shareSession{sourceKind: "window", windowHandle: 77}

			mode, enabled, err := application.startGameCastAudioLoopOrDisable(nil, session, test.primary, "process")
			if err == nil || enabled || mode != "off" || attempts != 1 {
				t.Fatalf("startup result = (%q, %t, %v) attempts=%d", mode, enabled, err, attempts)
			}
			if got := len(resolver.inputs); (got == 1) != test.wantRefresh {
				t.Fatalf("resolver refresh calls = %d, wantRefresh=%t", got, test.wantRefresh)
			}
		})
	}
}

func TestGameCastRuntimeWindowAudioRebindsOnlyOnceWithinSameApplication(t *testing.T) {
	ensureTestOpusDLL(t)
	resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{{
		ProcessID:      200,
		Mode:           windowsaudio.CaptureIncludeProcessTree,
		Identity:       `c:\apps\player\player.exe`,
		IdentityStable: true,
		Reason:         shareaudio.ReasonSiblingAudio,
	}}}
	starts := 0
	videoStopped := atomic.Bool{}
	voiceSession := &fakeRoomVoiceSession{started: make(chan struct{})}
	lease := &gamecast.ShareLease{Room: "room-1", LeaseID: "lease-1", Active: true}
	session := &shareSession{
		roomID:       "room-1",
		sourceKind:   "window",
		windowHandle: 77,
		audioMode:    "process",
		audioTarget:  stableWindowAudioTarget(100, `c:\apps\player\player.exe`),
	}
	host := nativehost.New(nativehost.Config{})
	application := &App{
		audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
		gamecastNativeShare:        session,
		gamecastNativeHost:         host,
		gamecastShareLease:         lease,
		gamecastVoice:              voiceSession,
		gamecastMediaCancel:        func() { videoStopped.Store(true) },
		gamecastShareAudioResolver: resolver,
		windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
			return sharecatalog.WindowProcess{ProcessID: 110, ProcessName: "player.exe"}, nil
		},
		gamecastAudioLoopStarter: func(_ *nativehost.Host, _ *shareSession, target audioCaptureTarget) error {
			starts++
			if target.Mode != windowsaudio.CaptureIncludeProcessTree {
				t.Fatalf("runtime target mode = %v", target.Mode)
			}
			return nil
		},
	}

	if !application.tryRebindGameCastWindowAudio(host, session) {
		t.Fatal("expected one safe runtime audio rebind")
	}
	if application.tryRebindGameCastWindowAudio(host, session) {
		t.Fatal("second runtime audio rebind must be refused")
	}
	if starts != 1 || len(resolver.inputs) != 1 || !session.audioRebindAttempted || session.audioTarget.PID != 200 {
		t.Fatalf("runtime rebind state starts=%d resolver=%d attempted=%t target=%#v", starts, len(resolver.inputs), session.audioRebindAttempted, session.audioTarget)
	}
	if videoStopped.Load() || application.gamecastNativeHost != host || application.gamecastNativeShare != session {
		t.Fatal("audio rebind changed the active video host or session")
	}
	if application.gamecastShareLease != lease || application.gamecastVoice != voiceSession || voiceSession.stopped.Load() {
		t.Fatal("audio rebind changed the share lease or room voice session")
	}
}

func TestGameCastRuntimeAudioDoesNotRebindStaleDisplayOrDifferentApplication(t *testing.T) {
	ensureTestOpusDLL(t)
	for _, name := range []string{"stale generation", "display", "different application"} {
		t.Run(name, func(t *testing.T) {
			resolver := &scriptedShareAudioResolver{targets: []shareaudio.Target{{
				ProcessID: 200, Mode: windowsaudio.CaptureIncludeProcessTree,
				Identity: `c:\apps\other\other.exe`, IdentityStable: true,
			}}}
			session := &shareSession{
				sourceKind: "window", windowHandle: 77, audioMode: "process",
				audioTarget: stableWindowAudioTarget(100, `c:\apps\player\player.exe`),
			}
			current := session
			if name == "stale generation" {
				current = &shareSession{}
			}
			if name == "display" {
				session.sourceKind = "display"
			}
			starts := 0
			host := nativehost.New(nativehost.Config{})
			application := &App{
				audioCapability:            windowsaudio.Capability{SupportsProcessCapture: true},
				gamecastNativeShare:        current,
				gamecastNativeHost:         host,
				gamecastShareAudioResolver: resolver,
				windowProcessResolver: func(uintptr) (sharecatalog.WindowProcess, error) {
					return sharecatalog.WindowProcess{ProcessID: 110, ProcessName: "player.exe"}, nil
				},
				gamecastAudioLoopStarter: func(_ *nativehost.Host, _ *shareSession, _ audioCaptureTarget) error {
					starts++
					return nil
				},
			}

			if application.tryRebindGameCastWindowAudio(host, session) {
				t.Fatal("unsafe runtime audio rebind was accepted")
			}
			if starts != 0 {
				t.Fatalf("runtime audio starts = %d, want 0", starts)
			}
		})
	}
}

func TestGameCastRuntimeAudioFailureDisablesOnlySharedAudio(t *testing.T) {
	var heartbeat gamecast.ShareLeaseRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/share/heartbeat" {
			http.Error(w, `{"error":"unexpected endpoint"}`, http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"room-1","leaseId":"lease-1","participantId":"client-1","active":true,"sfu":true,"audioMode":"off","ttl":15},"room":{"room":"room-1","active":true,"origin":"client","sharing":true,"sfu":true,"audioMode":"off"}}`))
	}))
	defer server.Close()

	jar, err := session.New(filepath.Join(t.TempDir(), "cookies.json"))
	if err != nil {
		t.Fatalf("create test cookie jar: %v", err)
	}
	ready := make(chan struct{})
	close(ready)
	videoStopped := atomic.Bool{}
	voiceSession := &fakeRoomVoiceSession{started: make(chan struct{})}
	host := nativehost.New(nativehost.Config{})
	share := &shareSession{
		roomID:               "room-1",
		peerID:               "gamecast-host",
		sourceKind:           "window",
		windowHandle:         77,
		width:                1920,
		height:               1080,
		fps:                  60,
		audioMode:            "process",
		audioTarget:          stableWindowAudioTarget(100, `c:\apps\player\player.exe`),
		audioRebindAttempted: true,
		ready:                ready,
	}
	application := NewApp(network.NewLoopbackNetwork("self"))
	application.cfg = config.DefaultConfig()
	application.cfg.GameCast.BaseURL = server.URL
	application.gamecastJar = jar
	application.gamecastParticipantID = "client-1"
	application.gamecastPresenceCancel = func() {}
	application.gamecastPresenceRoom = "room-1"
	application.gamecastNativeHost = host
	application.gamecastNativeShare = share
	application.gamecastShareLease = &gamecast.ShareLease{
		Room: "room-1", LeaseID: "lease-1", ParticipantID: "client-1", Active: true, SFU: true, AudioMode: "process", TTL: 15,
	}
	application.gamecastVoice = voiceSession
	application.gamecastMediaCancel = func() { videoStopped.Store(true) }
	defer func() {
		application.mu.Lock()
		cancel := application.gamecastShareCancel
		application.gamecastShareCancel = nil
		application.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}()

	application.handleGameCastNativeAudioFailure(share, errors.New("process loopback ended"))

	if heartbeat.Room != "room-1" || heartbeat.LeaseID != "lease-1" || !heartbeat.SFU || heartbeat.AudioMode != "off" {
		t.Fatalf("audio-disabled heartbeat = %#v", heartbeat)
	}
	if share.audioMode != "off" {
		t.Fatalf("share audio mode = %q, want off", share.audioMode)
	}
	if videoStopped.Load() || application.gamecastNativeHost != host || application.gamecastNativeShare != share {
		t.Fatal("shared-audio failure stopped or replaced the active video session")
	}
	if application.gamecastVoice != voiceSession || voiceSession.stopped.Load() {
		t.Fatal("shared-audio failure stopped or replaced the room voice session")
	}
	if application.gamecastShareLease == nil || application.gamecastShareLease.LeaseID != "lease-1" || !application.gamecastShareLease.Active {
		t.Fatalf("shared-audio failure lost the share lease: %#v", application.gamecastShareLease)
	}
}

type scriptedShareAudioResolver struct {
	targets []shareaudio.Target
	errs    []error
	inputs  []shareaudio.WindowTarget
}

func (r *scriptedShareAudioResolver) ResolveWindow(input shareaudio.WindowTarget) (shareaudio.Target, error) {
	r.inputs = append(r.inputs, input)
	index := len(r.inputs) - 1
	if index < len(r.errs) && r.errs[index] != nil {
		return shareaudio.Target{}, r.errs[index]
	}
	if index >= len(r.targets) {
		return shareaudio.Target{}, shareaudio.ErrSnapshotUnavailable
	}
	return r.targets[index], nil
}

func stableWindowAudioTarget(pid uint32, identity string) audioCaptureTarget {
	return audioCaptureTarget{
		PID:                 pid,
		Name:                "player.exe",
		Mode:                windowsaudio.CaptureIncludeProcessTree,
		ApplicationIdentity: identity,
		IdentityStable:      true,
		Reason:              shareaudio.ReasonWindowProcess,
	}
}

func TestProcessBelongsToTreeIncludesDescendantsAndStopsCycles(t *testing.T) {
	processes := map[int]ps.Process{
		30: testProcess{pid: 30, ppid: 20},
		20: testProcess{pid: 20, ppid: 10},
		10: testProcess{pid: 10, ppid: 0},
		40: testProcess{pid: 40, ppid: 50},
		50: testProcess{pid: 50, ppid: 40},
	}
	lookup := func(pid int) (ps.Process, error) {
		process, ok := processes[pid]
		if !ok {
			return nil, fmt.Errorf("process %d not found", pid)
		}
		return process, nil
	}

	if !processBelongsToTree(30, 10, lookup) {
		t.Fatal("expected descendant process to belong to the PartyLAN process tree")
	}
	if processBelongsToTree(30, 99, lookup) {
		t.Fatal("unexpected unrelated process tree match")
	}
	if processBelongsToTree(40, 10, lookup) {
		t.Fatal("cycle must terminate without a false process tree match")
	}
}

type testProcess struct {
	pid  int
	ppid int
}

func (p testProcess) Pid() int           { return p.pid }
func (p testProcess) PPid() int          { return p.ppid }
func (p testProcess) Executable() string { return "test.exe" }

func ensureTestOpusDLL(t *testing.T) {
	t.Helper()
	opusDir, err := filepath.Abs(filepath.Join("third_party", "opus", "win64"))
	if err != nil {
		t.Fatalf("resolve test opus directory: %v", err)
	}
	t.Setenv("PATH", opusDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if capability := audio.QueryOpusCapability(); !capability.Available {
		t.Fatalf("test opus capability unavailable: %s", capability.Reason)
	}
}

func TestWebRTCVideoTxStatsSnapshotUsesRecentWindow(t *testing.T) {
	stats := newWebRTCVideoTxStats()
	stats.lastLog = time.Now().Add(-2 * time.Second)
	snapshot := stats.record(4000, 1, "direct", 1280, 720)

	if snapshot == nil {
		t.Fatal("expected tx stats snapshot")
	}
	if snapshot.Width != 1280 || snapshot.Height != 720 {
		t.Fatalf("snapshot dimensions = %dx%d, want 1280x720", snapshot.Width, snapshot.Height)
	}
	if snapshot.EncodedFPS <= 0 {
		t.Fatalf("snapshot encoded fps = %f, want > 0", snapshot.EncodedFPS)
	}
	if snapshot.BitrateKbps <= 0 {
		t.Fatalf("snapshot bitrate = %f, want > 0", snapshot.BitrateKbps)
	}
}

func TestResolveDisplayShareSelection(t *testing.T) {
	index, err := resolveDisplayShareSelection("", "", 2)
	if err != nil || index != 0 {
		t.Fatalf("default display selection = (%d, %v), want (0, nil)", index, err)
	}

	index, err = resolveDisplayShareSelection("display:1", "display", 2)
	if err != nil || index != 1 {
		t.Fatalf("display:1 selection = (%d, %v), want (1, nil)", index, err)
	}

	if _, err := resolveDisplayShareSelection("window:123", "window", 2); err == nil {
		t.Fatal("expected window selection to return not-yet-supported error")
	}

	if _, err := resolveDisplayShareSelection("display:9", "display", 2); err == nil {
		t.Fatal("expected out-of-range display selection error")
	}
}

func TestSelectableAudioProcessNameFiltersSystemAndVoiceApps(t *testing.T) {
	blocked := []string{
		"svchost.exe",
		"RuntimeBroker.exe",
		"PartyLAN.exe",
		"ts3client_win64.exe",
		"TeamSpeak3.exe",
		"Discord.exe",
	}
	for _, name := range blocked {
		if isSelectableAudioProcessName(name) {
			t.Fatalf("expected %q to be filtered from shared audio candidates", name)
		}
	}

	allowed := []string{
		"chrome.exe",
		"firefox.exe",
		"Game.exe",
	}
	for _, name := range allowed {
		if !isSelectableAudioProcessName(name) {
			t.Fatalf("expected %q to be selectable", name)
		}
	}
}

type fakeProcess struct {
	pid  int
	ppid int
	name string
}

func (p fakeProcess) Pid() int           { return p.pid }
func (p fakeProcess) PPid() int          { return p.ppid }
func (p fakeProcess) Executable() string { return p.name }

func TestSelectedAudioCaptureTargetsResolveSelectedProcessNames(t *testing.T) {
	processes := []ps.Process{
		fakeProcess{pid: 100, name: "chrome.exe"},
		fakeProcess{pid: 101, name: "Chrome.exe"},
		fakeProcess{pid: 200, name: "Discord.exe"},
		fakeProcess{pid: 300, name: "Game.exe"},
		fakeProcess{pid: 400, name: "svchost.exe"},
	}

	targets := selectedAudioCaptureTargetsFromProcesses(processes, []string{
		"Chrome.exe",
		"Discord.exe",
		"svchost.exe",
	})

	if len(targets) != 2 {
		t.Fatalf("expected two chrome targets, got %#v", targets)
	}
	for _, target := range targets {
		if normalizeAudioProcessNameKey(target.Name) != "chrome" {
			t.Fatalf("unexpected target selected: %#v", target)
		}
	}
	if targets[0].PID != 101 || targets[1].PID != 100 {
		t.Fatalf("targets sorted by display name then PID = %#v", targets)
	}
}

func TestNormalizeAudioShareSelectionKeepsFirstSelectableCanonicalSource(t *testing.T) {
	got := normalizeAudioShareSelection([]string{
		"Discord.exe",
		" Chrome.EXE ",
		"Game.exe",
	})

	if len(got) != 1 || got[0] != "chrome.exe" {
		t.Fatalf("normalized audio share selection = %#v, want [chrome.exe]", got)
	}
}

func TestNormalizeAudioShareSelectionClearsWhenNoSelectableSource(t *testing.T) {
	got := normalizeAudioShareSelection([]string{
		"PartyLAN.exe",
		"svchost.exe",
		" ",
	})

	if len(got) != 0 {
		t.Fatalf("normalized blocked audio share selection = %#v, want empty", got)
	}
}

func TestBuildAudioShareCandidatesUsesNormalizedSelectionAndDedupe(t *testing.T) {
	processes := []ps.Process{
		fakeProcess{pid: 101, name: "Chrome.exe"},
		fakeProcess{pid: 100, name: "chrome.exe"},
		fakeProcess{pid: 200, name: "Discord.exe"},
		fakeProcess{pid: 300, name: "Game.exe"},
	}

	candidates := buildAudioShareCandidates(processes, map[string]bool{"chrome": true})

	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want chrome and game only", candidates)
	}
	if candidates[0].Name != "Chrome.exe" || !candidates[0].Selected {
		t.Fatalf("chrome candidate selected state = %#v, want selected normalized match", candidates[0])
	}
	if candidates[1].Name != "Game.exe" || candidates[1].Selected {
		t.Fatalf("game candidate selected state = %#v, want unselected", candidates[1])
	}
}

func TestBuildAudioShareCandidatesFromAudioSourcesShowsOnlyActiveSelectableApps(t *testing.T) {
	sources := []windowsaudio.AudioSource{
		{ProcessID: 100, Name: "Chrome.exe", DisplayName: "Google Chrome", IconDataURL: "data:image/bmp;base64,abc", Active: true},
		{ProcessID: 101, Name: "chrome.exe", DisplayName: "Chrome Renderer", Active: true},
		{ProcessID: 200, Name: "Game.exe", Active: false},
		{ProcessID: 300, Name: "Discord.exe", Active: true},
	}

	candidates := buildAudioShareCandidatesFromAudioSources(sources, map[string]bool{"chrome": true})

	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one active selectable app", candidates)
	}
	if candidates[0].Name != "Chrome.exe" || candidates[0].DisplayName != "Google Chrome" || !candidates[0].Selected {
		t.Fatalf("chrome candidate = %#v, want selected display candidate", candidates[0])
	}
	if candidates[0].IconDataURL == "" {
		t.Fatalf("expected icon data URL to be preserved")
	}
}

func TestSelectedAudioCaptureTargetsPreferActiveAudioSessionPID(t *testing.T) {
	sources := []windowsaudio.AudioSource{
		{ProcessID: 42, Name: "Game.exe", Active: true},
		{ProcessID: 99, Name: "Game.exe", Active: false},
	}

	targets := selectedAudioCaptureTargetsFromAudioSources(sources, []string{"game.exe"})

	if len(targets) != 1 || targets[0].PID != 42 {
		t.Fatalf("audio session targets = %#v, want active PID 42", targets)
	}
}

func TestNormalizeDisplayNameTrimsCollapsesAndLimitsLength(t *testing.T) {
	got := normalizeDisplayName("  Player   One  ")
	if got != "Player One" {
		t.Fatalf("normalizeDisplayName collapsed whitespace = %q, want Player One", got)
	}

	longName := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	got = normalizeDisplayName(longName)
	if len([]rune(got)) != maxDisplayNameRunes {
		t.Fatalf("normalizeDisplayName length = %d, want %d", len([]rune(got)), maxDisplayNameRunes)
	}
}
