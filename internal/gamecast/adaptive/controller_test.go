package adaptive

import (
	"testing"
	"time"
)

func TestBuildLadderPreservesRequestedMaximumAndPrefers24FPS(t *testing.T) {
	ladder := BuildLadder(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000})
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

func TestLocalPressureRequiresFourOfFiveAndLastThreeBad(t *testing.T) {
	start := time.Unix(1000, 0)
	controller := NewController(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000}, start)
	bad := func(at time.Time) LocalSample {
		return LocalSample{At: at, Valid: true, FreshFPS: 30, EncodedFPS: 30, EncodeBudgetRatio: 0.9}
	}
	good := func(at time.Time) LocalSample {
		return LocalSample{At: at, Valid: true, FreshFPS: 60, EncodedFPS: 60, EncodeBudgetRatio: 0.4}
	}

	samples := []LocalSample{
		bad(start.Add(2 * time.Second)),
		bad(start.Add(4 * time.Second)),
		good(start.Add(6 * time.Second)),
		bad(start.Add(8 * time.Second)),
		bad(start.Add(10 * time.Second)),
	}
	for _, sample := range samples {
		if decision := controller.Observe(sample.At, &sample, nil); decision != nil {
			t.Fatalf("unexpected decision before trailing-three confirmation: %#v", decision)
		}
	}
	last := bad(start.Add(12 * time.Second))
	decision := controller.Observe(last.At, &last, nil)
	if decision == nil || decision.Type != DecisionDownshift || decision.To.FPS != 45 {
		t.Fatalf("decision = %#v, want downshift to 1080p45", decision)
	}
}

func TestNetworkPressureNeedsLossAndSecondSignal(t *testing.T) {
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
		decision := controller.Observe(at, nil, []ViewerSample{sample})
		if i < 14 && decision != nil {
			t.Fatalf("network downshift occurred too early at sample %d: %#v", i, decision)
		}
		if i == 14 && (decision == nil || decision.Type != DecisionDownshift || decision.To.FPS != 24) {
			t.Fatalf("decision = %#v, want 720p24", decision)
		}
	}
}

func TestRecoveryNeedsThirtyStableSecondsAndMovesOneLevel(t *testing.T) {
	start := time.Unix(3000, 0)
	controller := NewController(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000}, start)
	controller.current = 1
	controller.cooldownUntil = start

	for i := 0; i < 15; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		local := LocalSample{At: at, Valid: true, FreshFPS: 45, EncodedFPS: 45, EncodeBudgetRatio: 0.4}
		viewer := ViewerSample{
			At: at, ViewerID: "viewer-1", Generation: "g1", PairID: "p1", Valid: true,
			PacketsReceivedDelta: 199, PacketsLostDelta: 1, DecodedFPS: 44,
			IncomingBitrateKbps: 6800, JitterMS: 20,
		}
		if decision := controller.Observe(at, &local, []ViewerSample{viewer}); decision != nil {
			t.Fatalf("recovered before 30 seconds at %s: %#v", at.Sub(start), decision)
		}
	}
	at := start.Add(30 * time.Second)
	local := LocalSample{At: at, Valid: true, FreshFPS: 45, EncodedFPS: 45, EncodeBudgetRatio: 0.4}
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

	for i := 0; i <= 15; i++ {
		at := start.Add(time.Duration(i*2) * time.Second)
		local := LocalSample{At: at, Valid: true, FreshFPS: 24, EncodedFPS: 24, EncodeBudgetRatio: 0.4}
		decision := controller.Observe(at, &local, nil)
		if i < 15 && decision != nil {
			t.Fatalf("unexpected early recovery: %#v", decision)
		}
		if i == 15 && (decision == nil || decision.Type != DecisionRecover) {
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
