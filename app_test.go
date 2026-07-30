package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"PartyLAN/internal/audio"
	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/gamecast/adaptive"
	hostwindowcapture "PartyLAN/internal/gamecast/host/windowcapture"
	"PartyLAN/internal/gamecast/session"
	gamecastws "PartyLAN/internal/gamecast/ws"
	"PartyLAN/internal/native/windowsaudio"
	"PartyLAN/internal/network"
	"PartyLAN/internal/video"
	"PartyLAN/pkg/config"

	ps "github.com/mitchellh/go-ps"
)

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
	application.gamecastShareLease = &gamecast.ShareLease{
		Room:          "room-1",
		LeaseID:       "lease-1",
		ParticipantID: application.gamecastParticipantID,
		Active:        true,
	}

	presence := application.gameCastPresenceRequest("room-1", "owner", "client")
	if presence.Room != "room-1" || presence.ClientType != "client" || presence.Origin != "client" || presence.Role != "owner" {
		t.Fatalf("presence request = %#v", presence)
	}
	lease := application.gameCastShareLeaseRequest("room-1", true, "system")
	if lease.ParticipantID != application.gamecastParticipantID || lease.LeaseID != "lease-1" || !lease.SFU || lease.AudioMode != "system" {
		t.Fatalf("lease request = %#v", lease)
	}
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
	shareActive := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(`{"user":"admin","loggedIn":true}`))
		case "/api/presence/join":
			_, _ = w.Write([]byte(`{"ok":true,"participant":{"participantId":"client-test","room":"admin","clientType":"client","role":"owner","active":true},"room":{"room":"admin","active":true,"origin":"client","ownerParticipantId":"client-test","ownerName":"admin","sharing":false,"sfu":false,"audioMode":"off","ttl":15}}`))
		case "/api/history":
			_, _ = w.Write([]byte(`{"ok":true,"history":[]}`))
		case "/api/share/claim":
			shareActive = true
			_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":true,"sfu":false,"audioMode":"off","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":true,"sfu":false,"audioMode":"off"}}`))
		case "/api/share/heartbeat":
			shareActive = true
			_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":true,"sfu":true,"audioMode":"system","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":true,"sfu":true,"audioMode":"system"}}`))
		case "/api/share/release":
			shareActive = false
			_, _ = w.Write([]byte(`{"ok":true,"lease":{"room":"admin","leaseId":"lease-1","participantId":"client-test","active":false,"sfu":false,"audioMode":"off","ttl":15},"room":{"room":"admin","active":true,"origin":"client","sharing":false,"sfu":false,"audioMode":"off"}}`))
		case "/api/room-state":
			if r.Method != http.MethodGet {
				http.Error(w, `{"code":"room_origin_conflict"}`, http.StatusConflict)
				return
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"state":{"room":"admin","active":true,"origin":"client","sharing":%t,"sfu":%t,"audioMode":"off"}}`, shareActive, shareActive)
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
	if _, _, err := application.claimGameCastShare("admin", false, "off"); err != nil {
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
