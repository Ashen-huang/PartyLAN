package main

import (
	"fmt"
	"testing"
	"time"

	"PartyLAN/internal/native/windowsaudio"
	"PartyLAN/internal/network"
	"PartyLAN/internal/video"

	ps "github.com/mitchellh/go-ps"
)

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
