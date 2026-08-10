package adaptive

import (
	"testing"
	"time"
)

func TestSelectActiveLevelsKeepsExtremesAndWeightedMiddle(t *testing.T) {
	targets := []ViewerTarget{
		{ViewerID: "high", Level: 0},
		{ViewerID: "near-high", Level: 1},
		{ViewerID: "middle-a", Level: 2},
		{ViewerID: "middle-b", Level: 2},
		{ViewerID: "middle-c", Level: 2},
		{ViewerID: "near-low", Level: 3},
		{ViewerID: "low", Level: 6},
	}
	active := SelectActiveLevels(targets, 3)
	want := []int{0, 2, 6}
	if len(active) != len(want) {
		t.Fatalf("active levels = %v, want %v", active, want)
	}
	for index := range want {
		if active[index] != want[index] {
			t.Fatalf("active levels = %v, want %v", active, want)
		}
	}

	assignments := AssignViewerLevels(targets, active)
	byViewer := make(map[string]ViewerAssignment, len(assignments))
	for _, assignment := range assignments {
		byViewer[assignment.ViewerID] = assignment
	}
	if got := byViewer["near-high"]; got.AssignedLevel != 2 || got.Constrained {
		t.Fatalf("near-high assignment = %#v, want safe level 2", got)
	}
	if got := byViewer["near-low"]; got.AssignedLevel != 6 || got.Constrained {
		t.Fatalf("near-low assignment = %#v, want safe level 6", got)
	}
}

func TestSelectActiveLevelsNeverDuplicatesWhenMoreThanThreeLayersAreRequested(t *testing.T) {
	targets := []ViewerTarget{
		{ViewerID: "v0", Level: 0}, {ViewerID: "v1", Level: 1},
		{ViewerID: "v2", Level: 2}, {ViewerID: "v3", Level: 3},
		{ViewerID: "v4", Level: 4}, {ViewerID: "v5", Level: 5},
	}
	active := SelectActiveLevels(targets, 5)
	seen := make(map[int]bool, len(active))
	for _, level := range active {
		if seen[level] {
			t.Fatalf("active levels contain duplicate %d: %v", level, active)
		}
		seen[level] = true
	}
	if len(active) != 5 {
		t.Fatalf("active levels = %v, want five unique levels", active)
	}
}

func TestAssignViewerLevelsMarksConstrainedWithoutALowerLayer(t *testing.T) {
	assignments := AssignViewerLevels(
		[]ViewerTarget{{ViewerID: "weak", Level: 6}},
		[]int{0, 2},
	)
	if len(assignments) != 1 || assignments[0].AssignedLevel != 2 || !assignments[0].Constrained {
		t.Fatalf("assignments = %#v, want lowest available level 2 constrained", assignments)
	}
}

func TestPerViewerControllerDownshiftsOnlyTheWeakViewer(t *testing.T) {
	start := time.Unix(12000, 0)
	controller := NewPerViewerController(BuildLadder(Profile{
		Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000,
	}), start)

	for index, seconds := range []int{2, 4, 6} {
		at := start.Add(time.Duration(seconds) * time.Second)
		weakEstimate := 12000
		if index > 0 {
			weakEstimate = 7000
		}
		decisions := controller.Observe(at, []PerViewerSample{
			goodPerViewerSample("strong", at, 15000, index),
			goodPerViewerSample("weak", at, weakEstimate, index),
		})
		if seconds < 6 && len(decisions) != 0 {
			t.Fatalf("early decisions at %ds = %#v", seconds, decisions)
		}
		if seconds == 6 {
			if len(decisions) != 1 || decisions[0].ViewerID != "weak" || decisions[0].ToLevel != 1 {
				t.Fatalf("decisions at 6s = %#v, want weak viewer to level 1", decisions)
			}
		}
	}

	statuses := controller.Status(start.Add(6 * time.Second))
	if statuses["strong"].Level != 0 || statuses["weak"].Level != 1 {
		t.Fatalf("statuses = %#v, want strong=0 weak=1", statuses)
	}
}

func TestPerViewerControllerRecoveryNeedsNextLevelHeadroomForTenSeconds(t *testing.T) {
	start := time.Unix(13000, 0)
	controller := NewPerViewerController(BuildLadder(Profile{
		Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000,
	}), start)

	for index, seconds := range []int{2, 4, 6} {
		estimate := 12000
		if index > 0 {
			estimate = 7000
		}
		controller.Observe(start.Add(time.Duration(seconds)*time.Second), []PerViewerSample{
			goodPerViewerSample("viewer", start.Add(time.Duration(seconds)*time.Second), estimate, index),
		})
	}

	for index, seconds := range []int{8, 10, 12, 14, 16} {
		at := start.Add(time.Duration(seconds) * time.Second)
		decisions := controller.Observe(at, []PerViewerSample{
			goodPerViewerSample("viewer", at, 11999, index+10),
		})
		if len(decisions) != 0 {
			t.Fatalf("recovered without 120%% headroom at %ds: %#v", seconds, decisions)
		}
	}

	for index, seconds := range []int{18, 20, 22, 24, 26, 28} {
		at := start.Add(time.Duration(seconds) * time.Second)
		decisions := controller.Observe(at, []PerViewerSample{
			goodPerViewerSample("viewer", at, 12000, index+20),
		})
		if seconds < 28 && len(decisions) != 0 {
			t.Fatalf("recovered before ten stable seconds at %ds: %#v", seconds, decisions)
		}
		if seconds == 28 {
			if len(decisions) != 1 || decisions[0].Type != DecisionRecover || decisions[0].ToLevel != 0 {
				t.Fatalf("recovery decisions = %#v, want one-level recovery", decisions)
			}
		}
	}
}

func TestPerViewerControllerDoesNotRecoverWithoutBandwidthEstimate(t *testing.T) {
	start := time.Unix(14000, 0)
	controller := NewPerViewerController(BuildLadder(Profile{
		Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000,
	}), start)

	for index, seconds := range []int{2, 4, 6} {
		estimate := 12000
		if index > 0 {
			estimate = 7000
		}
		controller.Observe(start.Add(time.Duration(seconds)*time.Second), []PerViewerSample{
			goodPerViewerSample("viewer", start.Add(time.Duration(seconds)*time.Second), estimate, index),
		})
	}
	for index := 4; index <= 12; index++ {
		at := start.Add(time.Duration(index*2) * time.Second)
		sample := goodPerViewerSample("viewer", at, 0, index)
		sample.EstimateValid = false
		if decisions := controller.Observe(at, []PerViewerSample{sample}); len(decisions) != 0 {
			t.Fatalf("recovered without bandwidth estimate: %#v", decisions)
		}
	}
	if got := controller.Status(start.Add(24 * time.Second))["viewer"].Level; got != 1 {
		t.Fatalf("level = %d, want 1", got)
	}
}

func TestPerViewerControllerPathChangeResetsBadWindow(t *testing.T) {
	start := time.Unix(15000, 0)
	controller := NewPerViewerController(BuildLadder(Profile{
		Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000,
	}), start)
	for index, seconds := range []int{2, 4} {
		at := start.Add(time.Duration(seconds) * time.Second)
		sample := goodPerViewerSample("viewer", at, 7000, index)
		sample.PairID = "old"
		controller.Observe(at, []PerViewerSample{sample})
	}
	at := start.Add(6 * time.Second)
	sample := goodPerViewerSample("viewer", at, 7000, 3)
	sample.PairID = "new"
	if decisions := controller.Observe(at, []PerViewerSample{sample}); len(decisions) != 0 {
		t.Fatalf("new path inherited old bad window: %#v", decisions)
	}
	if got := controller.Status(at)["viewer"].Level; got != 0 {
		t.Fatalf("new path level = %d, want 0", got)
	}
}

func goodPerViewerSample(viewerID string, at time.Time, estimateKbps, sequence int) PerViewerSample {
	return PerViewerSample{
		At: at, ViewerID: viewerID, SampleID: time.Duration(sequence).String(),
		Generation: "g1", PairID: "p1", ReceiverValid: true,
		EstimateValid: estimateKbps > 0, EstimatedBitrateKbps: estimateKbps,
		PacketsReceivedDelta: 200, PacketsLostDelta: 0, DecodedFPS: 60,
		IncomingBitrateKbps: 10000, JitterMS: 10,
	}
}
