package adaptive

import (
	"testing"
	"time"
)

func TestBuildLadderDerivesSameResolutionStepsForNonStandardSource(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 1654, Height: 940, FPS: 60, BitrateKbps: 8000})
	if len(ladder) < 3 {
		t.Fatalf("ladder = %#v, want original plus same-resolution 45/30 fps", ladder)
	}
	wantFPS := []int{60, 45, 30}
	for index, fps := range wantFPS {
		if ladder[index].Width != 1654 || ladder[index].Height != 940 || ladder[index].FPS != fps {
			t.Fatalf("ladder[%d] = %#v, want 1654x940@%d", index, ladder[index], fps)
		}
	}
	if ladder[1].BitrateKbps >= ladder[0].BitrateKbps || ladder[2].BitrateKbps >= ladder[1].BitrateKbps {
		t.Fatalf("same-resolution bitrate is not descending: %#v", ladder[:3])
	}
}

func TestBuildLadderUsesConfirmedP2PProfilesAndBitrates(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 50000})
	want := []Profile{
		{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 16000},
		{Width: 1920, Height: 1080, FPS: 45, BitrateKbps: 12000},
		{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 8000},
		{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 6000},
		{Width: 1280, Height: 720, FPS: 21, BitrateKbps: 4000},
		{Width: 960, Height: 540, FPS: 21, BitrateKbps: 2500},
		{Width: 640, Height: 360, FPS: 18, BitrateKbps: 800},
	}
	if len(ladder) != len(want) {
		t.Fatalf("ladder length = %d, want %d: %#v", len(ladder), len(want), ladder)
	}
	for index := range want {
		if ladder[index] != want[index] {
			t.Fatalf("ladder[%d] = %#v, want %#v", index, ladder[index], want[index])
		}
	}
}

func TestBuildLadderCaps4KSourceAtFortyMbps(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 3840, Height: 2160, FPS: 60, BitrateKbps: 50000})
	want := []Profile{
		{Width: 3840, Height: 2160, FPS: 60, BitrateKbps: 40000},
		{Width: 3840, Height: 2160, FPS: 45, BitrateKbps: 30000},
		{Width: 3840, Height: 2160, FPS: 30, BitrateKbps: 20000},
	}
	if len(ladder) < len(want) {
		t.Fatalf("4K ladder = %#v, want at least %#v", ladder, want)
	}
	for index := range want {
		if ladder[index] != want[index] {
			t.Fatalf("4K ladder[%d] = %#v, want %#v", index, ladder[index], want[index])
		}
	}
}

func TestBuildLadderKeepsAll720pFrameRateBandsBeforeScaling(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 1280, Height: 720, FPS: 60, BitrateKbps: 50000})
	want := []Profile{
		{Width: 1280, Height: 720, FPS: 60, BitrateKbps: 12000},
		{Width: 1280, Height: 720, FPS: 45, BitrateKbps: 9000},
		{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 6000},
		{Width: 1280, Height: 720, FPS: 21, BitrateKbps: 4000},
		{Width: 960, Height: 540, FPS: 21, BitrateKbps: 2500},
		{Width: 640, Height: 360, FPS: 18, BitrateKbps: 800},
	}
	if len(ladder) != len(want) {
		t.Fatalf("720p ladder length = %d, want %d: %#v", len(ladder), len(want), ladder)
	}
	for index := range want {
		if ladder[index] != want[index] {
			t.Fatalf("720p ladder[%d] = %#v, want %#v", index, ladder[index], want[index])
		}
	}
}

func TestLocalPressureReasonsAreDistinct(t *testing.T) {
	tests := []struct {
		name   string
		sample LocalSample
		reason string
	}{
		{
			name: "capture stalled",
			sample: LocalSample{Valid: true, FreshFPS: 0, EncodedFPS: 60,
				EncodeBudgetRatio: 0.3, RepeatedRatio: 1, CaptureUnavailable: 1},
			reason: "capture-stalled",
		},
		{
			name: "encoder overload",
			sample: LocalSample{Valid: true, FreshFPS: 60, FreshFPSValid: true,
				EncodedFPS: 30, EncodeBudgetRatio: 0.9},
			reason: "encoder-overload",
		},
		{
			name: "encoder failure",
			sample: LocalSample{Valid: true, FreshFPS: 60, FreshFPSValid: true,
				EncodedFPS: 60, EncodeBudgetRatio: 0.3, EncodeFailures: 1},
			reason: "encoder-overload",
		},
		{
			name: "transport write",
			sample: LocalSample{Valid: true, FreshFPS: 60, FreshFPSValid: true,
				EncodedFPS: 60, EncodeBudgetRatio: 0.3, WriteFailures: 1},
			reason: "transport-write",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(9000, 0)
			controller := NewController(Profile{Width: 1654, Height: 940, FPS: 60, BitrateKbps: 8000}, start)
			var decision *Decision
			for _, seconds := range []int{2, 4, 6} {
				sample := tc.sample
				sample.At = start.Add(time.Duration(seconds) * time.Second)
				decision = controller.Observe(sample.At, &sample, nil)
			}
			if decision == nil || decision.Type != DecisionDownshift || decision.Reason != tc.reason {
				t.Fatalf("decision = %#v, want downshift reason %q", decision, tc.reason)
			}
			if decision.To.Width != 1654 || decision.To.Height != 940 || decision.To.FPS != 45 {
				t.Fatalf("decision target = %#v, want same-resolution 45 fps", decision.To)
			}
		})
	}
}

func TestWeakViewerDoesNotChangeGlobalEncoderProfile(t *testing.T) {
	start := time.Unix(10000, 0)
	controller := NewController(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500}, start)
	for index := 1; index <= 10; index++ {
		at := start.Add(time.Duration(index*2) * time.Second)
		sample := ViewerSample{
			At: at, ViewerID: "weak", SampleID: time.Duration(index).String(), Generation: "g1", PairID: "p1", Valid: true,
			PacketsReceivedDelta: 180, PacketsLostDelta: 20, FramesDroppedDelta: 4,
			DecodedFPS: 8, IncomingBitrateKbps: 700, JitterMS: 140,
		}
		if decision := controller.Observe(at, nil, []ViewerSample{sample}); decision != nil {
			t.Fatalf("weak viewer changed global profile: %#v", decision)
		}
	}
	status := controller.Status(start.Add(20 * time.Second))
	if status.WeakViewers != 1 || status.Level != 0 {
		t.Fatalf("status = %#v, want one weak viewer and unchanged level", status)
	}
}

func TestBuildLadderPreservesRequestedMaximumAndPrefers24FPS(t *testing.T) {
	ladder := buildLegacyLadder(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000})
	want := []Profile{
		{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000},
		{Width: 1920, Height: 1080, FPS: 45, BitrateKbps: 7000},
		{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 6000},
		{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500},
		{Width: 1280, Height: 720, FPS: 24, BitrateKbps: 3000},
		{Width: 960, Height: 540, FPS: 24, BitrateKbps: 1700},
		{Width: 640, Height: 360, FPS: 24, BitrateKbps: 900},
		{Width: 640, Height: 360, FPS: 18, BitrateKbps: 600},
	}
	if len(ladder) != len(want) {
		t.Fatalf("ladder length = %d, want %d: %#v", len(ladder), len(want), ladder)
	}
	for i := range want {
		if ladder[i] != want[i] {
			t.Fatalf("ladder[%d] = %#v, want %#v", i, ladder[i], want[i])
		}
	}
}

func TestBuildLadderStartsAtOriginal4K60AndReducesFPSBeforeResolution(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 3840, Height: 2160, FPS: 60, BitrateKbps: 8000})
	if len(ladder) < 2 {
		t.Fatalf("4K ladder = %#v, want original and lower profiles", ladder)
	}
	if ladder[0] != (Profile{Width: 3840, Height: 2160, FPS: 60, BitrateKbps: 8000}) {
		t.Fatalf("first 4K profile = %#v, want requested original profile", ladder[0])
	}
	if ladder[1].Width != 3840 || ladder[1].Height != 2160 || ladder[1].FPS != 45 {
		t.Fatalf("second 4K profile = %#v, want same-resolution 45 fps", ladder[1])
	}
	for index := 1; index < len(ladder); index++ {
		if ladder[index].Width > ladder[index-1].Width || ladder[index].Height > ladder[index-1].Height || ladder[index].FPS > ladder[index-1].FPS {
			t.Fatalf("ladder increases at %d: %#v", index, ladder)
		}
	}
}

func TestBuildLadderNeverExceedsOrUpscalesRequestedProfile(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3200})
	if got := ladder[0]; got != (Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3200}) {
		t.Fatalf("first profile = %#v", got)
	}
	for _, profile := range ladder {
		if profile.Width > 1280 || profile.Height > 720 || profile.FPS > 30 || profile.BitrateKbps > 3200 {
			t.Fatalf("profile exceeds requested maximum: %#v", profile)
		}
	}
}

func TestBuildLadderKeepsLowResolutionAndFrameRateCeilings(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 854, Height: 480, FPS: 24, BitrateKbps: 1200})
	if ladder[0] != (Profile{Width: 854, Height: 480, FPS: 24, BitrateKbps: 1200}) {
		t.Fatalf("first low-source profile = %#v", ladder[0])
	}
	for _, profile := range ladder {
		if profile.Width > 854 || profile.Height > 480 || profile.FPS > 24 || profile.BitrateKbps > 1200 {
			t.Fatalf("low-source ladder exceeds requested ceiling: %#v", profile)
		}
	}
}

func TestLocalPressureRequiresTwoOfThreeAndLastTwoBad(t *testing.T) {
	start := time.Unix(1000, 0)
	controller := NewController(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000}, start)
	bad := func(at time.Time) LocalSample {
		return LocalSample{At: at, Valid: true, FreshFPS: 30, EncodedFPS: 30, EncodeBudgetRatio: 0.9}
	}
	good := func(at time.Time) LocalSample {
		return LocalSample{At: at, Valid: true, FreshFPS: 60, EncodedFPS: 60, EncodeBudgetRatio: 0.4}
	}

	samples := []LocalSample{good(start.Add(2 * time.Second)), bad(start.Add(4 * time.Second))}
	for _, sample := range samples {
		if decision := controller.Observe(sample.At, &sample, nil); decision != nil {
			t.Fatalf("unexpected decision before trailing-two confirmation: %#v", decision)
		}
	}
	last := bad(start.Add(6 * time.Second))
	decision := controller.Observe(last.At, &last, nil)
	if decision == nil || decision.Type != DecisionDownshift || decision.To.FPS != 45 {
		t.Fatalf("decision = %#v, want downshift to 1080p45", decision)
	}
}

func TestNetworkPressureIsDiagnosticOnly(t *testing.T) {
	start := time.Unix(2000, 0)
	controller := NewController(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500}, start)
	for i := 1; i <= 9; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		sample := ViewerSample{
			At: at, ViewerID: "viewer-1", Generation: "g1", PairID: "p1", Valid: true,
			PacketsReceivedDelta: 180, PacketsLostDelta: 20, DecodedFPS: 30,
			IncomingBitrateKbps: 3400, JitterMS: 20,
		}
		if decision := controller.Observe(at, nil, []ViewerSample{sample}); decision != nil {
			t.Fatalf("loss without second signal caused decision at sample %d: %#v", i, decision)
		}
	}

	for i := 10; i <= 14; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		sample := ViewerSample{
			At: at, ViewerID: "viewer-1", Generation: "g1", PairID: "p1", Valid: true,
			PacketsReceivedDelta: 180, PacketsLostDelta: 20, DecodedFPS: 12,
			IncomingBitrateKbps: 1200, JitterMS: 130, FramesDroppedDelta: 4,
		}
		if decision := controller.Observe(at, nil, []ViewerSample{sample}); decision != nil {
			t.Fatalf("network pressure changed global profile at sample %d: %#v", i, decision)
		}
	}
	status := controller.Status(start.Add(28 * time.Second))
	if status.WeakViewers != 1 || status.Level != 0 {
		t.Fatalf("status = %#v, want diagnostic weak viewer only", status)
	}
}

func TestRecoveryNeedsTenStableSecondsAndMovesOneLevel(t *testing.T) {
	start := time.Unix(3000, 0)
	controller := NewController(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000}, start)
	controller.current = 1
	controller.cooldownUntil = start

	for i := 0; i < 5; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		local := LocalSample{At: at, Valid: true, FreshFPS: 60, FreshFPSValid: true, EncodedFPS: 45, EncodeBudgetRatio: 0.4}
		viewer := ViewerSample{
			At: at, ViewerID: "viewer-1", Generation: "g1", PairID: "p1", Valid: true,
			PacketsReceivedDelta: 199, PacketsLostDelta: 1, DecodedFPS: 44,
			IncomingBitrateKbps: 6800, JitterMS: 20,
		}
		if decision := controller.Observe(at, &local, []ViewerSample{viewer}); decision != nil {
			t.Fatalf("recovered before 10 seconds at %s: %#v", at.Sub(start), decision)
		}
	}
	at := start.Add(10 * time.Second)
	local := LocalSample{At: at, Valid: true, FreshFPS: 60, FreshFPSValid: true, EncodedFPS: 45, EncodeBudgetRatio: 0.4}
	viewer := ViewerSample{
		At: at, ViewerID: "viewer-1", Generation: "g1", PairID: "p1", Valid: true,
		PacketsReceivedDelta: 200, PacketsLostDelta: 0, DecodedFPS: 45,
		IncomingBitrateKbps: 7000, JitterMS: 20,
	}
	decision := controller.Observe(at, &local, []ViewerSample{viewer})
	if decision == nil || decision.Type != DecisionRecover || decision.To.FPS != 60 {
		t.Fatalf("decision = %#v, want one-level recovery to 1080p60", decision)
	}
}

func TestExpiredViewerDoesNotBlockLocalOnlyRecovery(t *testing.T) {
	start := time.Unix(4000, 0)
	controller := NewController(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500}, start)
	controller.current = 1
	controller.cooldownUntil = start
	controller.viewers["old"] = &viewerState{
		lastAt: start.Add(-9 * time.Second),
		latest: ViewerSample{At: start.Add(-9 * time.Second), Valid: true, ViewerID: "old"},
	}

	for i := 0; i <= 5; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		local := LocalSample{At: at, Valid: true, FreshFPS: 30, FreshFPSValid: true, EncodedFPS: 24, EncodeBudgetRatio: 0.4}
		decision := controller.Observe(at, &local, nil)
		if i < 5 && decision != nil {
			t.Fatalf("unexpected early recovery: %#v", decision)
		}
		if i == 5 && (decision == nil || decision.Type != DecisionRecover) {
			t.Fatalf("expected local-only recovery after stale viewer expired, got %#v", decision)
		}
	}
}

func TestRepeatedViewerSnapshotDoesNotManufactureADegradedWindow(t *testing.T) {
	start := time.Unix(4500, 0)
	controller := NewController(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500}, start)
	sample := ViewerSample{
		ViewerID: "viewer-1", SampleID: "same-report", Generation: "g1", PairID: "p1", Valid: true,
		PacketsReceivedDelta: 180, PacketsLostDelta: 20, DecodedFPS: 10,
		IncomingBitrateKbps: 800, JitterMS: 150, FramesDroppedDelta: 3,
	}
	for index := 1; index <= 10; index++ {
		at := start.Add(time.Duration(index*2) * time.Second)
		sample.At = at
		if decision := controller.Observe(at, nil, []ViewerSample{sample}); decision != nil {
			t.Fatalf("repeated server snapshot manufactured a decision: %#v", decision)
		}
	}
}

func TestViewerGenerationChangeResetsDegradedConfirmation(t *testing.T) {
	start := time.Unix(5000, 0)
	controller := NewController(Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500}, start)
	bad := ViewerSample{
		ViewerID: "viewer-1", PairID: "pair-1", Valid: true,
		PacketsReceivedDelta: 180, PacketsLostDelta: 20, DecodedFPS: 10,
		IncomingBitrateKbps: 800, JitterMS: 150, FramesDroppedDelta: 3,
	}
	for index := 1; index <= 7; index++ {
		at := start.Add(time.Duration(index*2) * time.Second)
		bad.Generation = "generation-1"
		bad.SampleID = "g1-" + time.Duration(index).String()
		if decision := controller.Observe(at, nil, []ViewerSample{bad}); decision != nil {
			t.Fatalf("old generation degraded too early: %#v", decision)
		}
	}

	bad.Generation = "generation-2"
	bad.SampleID = "g2-first"
	if decision := controller.Observe(start.Add(16*time.Second), nil, []ViewerSample{bad}); decision != nil {
		t.Fatalf("new generation inherited old degraded window: %#v", decision)
	}
	if status := controller.Status(start.Add(16 * time.Second)); status.WeakViewers != 0 {
		t.Fatalf("new generation weak viewers = %d, want 0 during observation", status.WeakViewers)
	}
}
