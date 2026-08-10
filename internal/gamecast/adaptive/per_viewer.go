package adaptive

import (
	"sort"
	"time"
)

const (
	PerViewerPolicyVersion = "native-per-viewer-layer-v1"
	perViewerObservation   = 4 * time.Second
	perViewerDownCooldown  = 8 * time.Second
	perViewerStableWindow  = 10 * time.Second
	perViewerUpCooldown    = 12 * time.Second
	perViewerSampleTTL     = 8 * time.Second
)

type ViewerTarget struct {
	ViewerID string
	Level    int
}

type ViewerAssignment struct {
	ViewerID      string
	TargetLevel   int
	AssignedLevel int
	Constrained   bool
}

// SelectActiveLevels retains the strongest and weakest requested levels and uses
// weighted distance to choose a deterministic middle level when the pool is full.
func SelectActiveLevels(targets []ViewerTarget, maxLayers int) []int {
	if maxLayers <= 0 || len(targets) == 0 {
		return nil
	}
	counts := make(map[int]int)
	for _, target := range targets {
		counts[target.Level]++
	}
	levels := make([]int, 0, len(counts))
	for level := range counts {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	if len(levels) <= maxLayers {
		return levels
	}
	if maxLayers == 1 {
		return []int{levels[len(levels)-1]}
	}
	active := []int{levels[0], levels[len(levels)-1]}
	for len(active) < maxLayers {
		bestLevel := 0
		bestCost := 0
		found := false
		for _, level := range levels[1 : len(levels)-1] {
			if containsLevel(active, level) {
				continue
			}
			cost := weightedLevelDistance(level, levels, counts)
			if !found || cost < bestCost || (cost == bestCost && level > bestLevel) {
				bestLevel = level
				bestCost = cost
				found = true
			}
		}
		if !found {
			break
		}
		active = append(active, bestLevel)
	}
	sort.Ints(active)
	return active
}

func weightedLevelDistance(candidate int, levels []int, counts map[int]int) int {
	distance := 0
	for _, level := range levels {
		delta := candidate - level
		if delta < 0 {
			delta = -delta
		}
		distance += delta * counts[level]
	}
	return distance
}

func containsLevel(levels []int, target int) bool {
	for _, level := range levels {
		if level == target {
			return true
		}
	}
	return false
}

func AssignViewerLevels(targets []ViewerTarget, active []int) []ViewerAssignment {
	if len(active) == 0 {
		return nil
	}
	levels := append([]int(nil), active...)
	sort.Ints(levels)
	assignments := make([]ViewerAssignment, 0, len(targets))
	for _, target := range targets {
		assigned := levels[len(levels)-1]
		constrained := true
		for _, level := range levels {
			if level == target.Level {
				assigned = level
				constrained = false
				break
			}
			if level >= target.Level {
				assigned = level
				constrained = false
				break
			}
		}
		assignments = append(assignments, ViewerAssignment{
			ViewerID: target.ViewerID, TargetLevel: target.Level,
			AssignedLevel: assigned, Constrained: constrained,
		})
	}
	return assignments
}

type PerViewerSample struct {
	At                   time.Time
	ViewerID             string
	SampleID             string
	Generation           string
	PairID               string
	ReceiverValid        bool
	PacketsReceivedDelta int64
	PacketsLostDelta     int64
	FramesDroppedDelta   int64
	DecodedFPS           float64
	IncomingBitrateKbps  float64
	JitterMS             float64
	EstimateValid        bool
	EstimatedBitrateKbps int
}

type ViewerDecision struct {
	ViewerID  string
	Type      DecisionType
	Reason    string
	FromLevel int
	ToLevel   int
}

type PerViewerStatus struct {
	ViewerID      string
	Level         int
	State         string
	Reason        string
	Generation    string
	PairID        string
	Weak          bool
	Constrained   bool
	LastSampleAt  time.Time
	CooldownUntil time.Time
}

type perViewerState struct {
	viewerID      string
	generation    string
	pairID        string
	connectedAt   time.Time
	lastAt        time.Time
	lastSampleID  string
	level         int
	window        []bool
	stableSince   time.Time
	cooldownUntil time.Time
	state         string
	reason        string
	weak          bool
}

type PerViewerController struct {
	ladder  []Profile
	viewers map[string]*perViewerState
}

func NewPerViewerController(ladder []Profile, startedAt time.Time) *PerViewerController {
	if len(ladder) == 0 {
		ladder = BuildLadder(Profile{})
	}
	return &PerViewerController{ladder: append([]Profile(nil), ladder...), viewers: make(map[string]*perViewerState)}
}

func (c *PerViewerController) Observe(now time.Time, samples []PerViewerSample) []ViewerDecision {
	decisions := make([]ViewerDecision, 0)
	for _, sample := range samples {
		if sample.ViewerID == "" || !validPerViewerSample(sample) {
			continue
		}
		state := c.viewers[sample.ViewerID]
		if state == nil || state.generation != sample.Generation || state.pairID != sample.PairID {
			state = &perViewerState{
				viewerID: sample.ViewerID, generation: sample.Generation, pairID: sample.PairID,
				connectedAt: now, state: "observing",
			}
			c.viewers[sample.ViewerID] = state
		}
		if sample.SampleID != "" && state.lastSampleID == sample.SampleID {
			continue
		}
		state.lastSampleID = sample.SampleID
		state.lastAt = now
		bad := perViewerBadSample(sample, c.currentProfile(state.level))
		state.weak = bad
		if bad {
			state.stableSince = time.Time{}
		} else if state.level > 0 && perViewerStableSample(sample, c.currentProfile(state.level), c.nextProfile(state.level)) {
			if state.stableSince.IsZero() {
				state.stableSince = now
			}
			if !now.Before(state.cooldownUntil) && now.Sub(state.stableSince) >= perViewerStableWindow {
				from := state.level
				state.level--
				state.window = nil
				state.stableSince = time.Time{}
				state.cooldownUntil = now.Add(perViewerUpCooldown)
				state.state = "recovering"
				state.reason = "stable-headroom"
				decisions = append(decisions, ViewerDecision{
					ViewerID: sample.ViewerID, Type: DecisionRecover, Reason: "stable-headroom",
					FromLevel: from, ToLevel: state.level,
				})
				continue
			}
		} else {
			state.stableSince = time.Time{}
		}
		if now.Before(state.cooldownUntil) {
			state.state = "cooldown"
			continue
		}
		state.window = appendPerViewerWindow(state.window, bad, 3)
		if now.Sub(state.connectedAt) < perViewerObservation {
			state.state = "observing"
			continue
		}
		if confirmedPerViewerWindow(state.window, 3, 2, 2) && state.level < len(c.ladder)-1 {
			from := state.level
			state.level++
			state.window = nil
			state.cooldownUntil = now.Add(perViewerDownCooldown)
			state.state = "degraded-confirmed"
			state.reason = perViewerReason(sample, c.currentProfile(from))
			decisions = append(decisions, ViewerDecision{
				ViewerID: sample.ViewerID, Type: DecisionDownshift, Reason: state.reason,
				FromLevel: from, ToLevel: state.level,
			})
			continue
		}
		if bad {
			state.state = "degraded-suspected"
		} else {
			state.state = "normal"
			state.reason = ""
		}
	}
	c.prune(now)
	return decisions
}

func (c *PerViewerController) Status(now time.Time) map[string]PerViewerStatus {
	status := make(map[string]PerViewerStatus, len(c.viewers))
	for viewerID, state := range c.viewers {
		if now.Sub(state.lastAt) > perViewerSampleTTL && !state.lastAt.IsZero() {
			continue
		}
		status[viewerID] = PerViewerStatus{
			ViewerID: viewerID, Level: state.level, State: state.state, Reason: state.reason,
			Generation: state.generation, PairID: state.pairID, Weak: state.weak,
			LastSampleAt: state.lastAt, CooldownUntil: state.cooldownUntil,
		}
	}
	return status
}

func (c *PerViewerController) Targets(now time.Time) []ViewerTarget {
	status := c.Status(now)
	targets := make([]ViewerTarget, 0, len(status))
	for viewerID, entry := range status {
		targets = append(targets, ViewerTarget{ViewerID: viewerID, Level: entry.Level})
	}
	sort.Slice(targets, func(left, right int) bool { return targets[left].ViewerID < targets[right].ViewerID })
	return targets
}

func (c *PerViewerController) currentProfile(level int) Profile {
	if level < 0 {
		level = 0
	}
	if level >= len(c.ladder) {
		level = len(c.ladder) - 1
	}
	return c.ladder[level]
}

func (c *PerViewerController) nextProfile(level int) Profile {
	if level <= 0 {
		return c.currentProfile(0)
	}
	return c.currentProfile(level - 1)
}

func (c *PerViewerController) prune(now time.Time) {
	for viewerID, state := range c.viewers {
		if !state.lastAt.IsZero() && now.Sub(state.lastAt) > perViewerSampleTTL {
			delete(c.viewers, viewerID)
		}
	}
}

func validPerViewerSample(sample PerViewerSample) bool {
	if sample.EstimateValid && sample.EstimatedBitrateKbps < 0 {
		return false
	}
	if sample.ReceiverValid && (sample.PacketsReceivedDelta < 0 || sample.PacketsLostDelta < 0 || sample.FramesDroppedDelta < 0) {
		return false
	}
	if !sample.EstimateValid && !sample.ReceiverValid {
		return false
	}
	if sample.ReceiverValid && sample.PacketsReceivedDelta+sample.PacketsLostDelta < 20 {
		return false
	}
	return true
}

func perViewerBadSample(sample PerViewerSample, current Profile) bool {
	if sample.EstimateValid && float64(sample.EstimatedBitrateKbps) < float64(current.BitrateKbps)*0.8 {
		return true
	}
	if !sample.ReceiverValid || lossPercentValues(sample.PacketsReceivedDelta, sample.PacketsLostDelta) <= 5 {
		return false
	}
	return sample.IncomingBitrateKbps < float64(current.BitrateKbps)*0.8 ||
		sample.DecodedFPS < float64(current.FPS)*0.85 || sample.JitterMS > 100 || sample.FramesDroppedDelta > 0
}

func perViewerStableSample(sample PerViewerSample, current, next Profile) bool {
	if !sample.EstimateValid || float64(sample.EstimatedBitrateKbps) < float64(next.BitrateKbps)*1.2 {
		return false
	}
	if !sample.ReceiverValid || lossPercentValues(sample.PacketsReceivedDelta, sample.PacketsLostDelta) >= 1 ||
		sample.JitterMS >= 40 || sample.FramesDroppedDelta > 0 {
		return false
	}
	return sample.DecodedFPS >= float64(current.FPS)*0.95
}

func perViewerReason(sample PerViewerSample, current Profile) string {
	if sample.EstimateValid && float64(sample.EstimatedBitrateKbps) < float64(current.BitrateKbps)*0.8 {
		return "bandwidth-estimate"
	}
	return "viewer-network"
}

func lossPercentValues(received, lost int64) float64 {
	total := received + lost
	if total <= 0 {
		return 0
	}
	return float64(lost) * 100 / float64(total)
}

func appendPerViewerWindow(window []bool, value bool, size int) []bool {
	window = append(window, value)
	if len(window) > size {
		window = window[len(window)-size:]
	}
	return window
}

func confirmedPerViewerWindow(window []bool, size, required, trailing int) bool {
	if len(window) != size {
		return false
	}
	count := 0
	for _, value := range window {
		if value {
			count++
		}
	}
	if count < required {
		return false
	}
	for _, value := range window[len(window)-trailing:] {
		if !value {
			return false
		}
	}
	return true
}
