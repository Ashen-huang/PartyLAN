package adaptive

import (
	"sort"
	"time"
)

const (
	PolicyVersion = "native-global-adaptive-v1"

	localObservation       = 6 * time.Second
	networkObservation     = 15 * time.Second
	downshiftCooldown      = 15 * time.Second
	recoveryStableDuration = 30 * time.Second
	recoveryCooldown       = 30 * time.Second
	viewerSampleTTL        = 8 * time.Second
)

type Profile struct {
	Width       int
	Height      int
	FPS         int
	BitrateKbps int
}

var canonicalProfiles = []Profile{
	{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000},
	{Width: 1920, Height: 1080, FPS: 45, BitrateKbps: 7000},
	{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 6000},
	{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500},
	{Width: 1280, Height: 720, FPS: 24, BitrateKbps: 3000},
	{Width: 960, Height: 540, FPS: 24, BitrateKbps: 1700},
	{Width: 640, Height: 360, FPS: 24, BitrateKbps: 900},
	{Width: 640, Height: 360, FPS: 18, BitrateKbps: 600},
}

func BuildLadder(requested Profile) []Profile {
	requested = normalizeProfile(requested)
	ladder := []Profile{requested}
	for _, candidate := range canonicalProfiles {
		if candidate.Width > requested.Width || candidate.Height > requested.Height || candidate.FPS > requested.FPS {
			continue
		}
		candidate.BitrateKbps = minPositive(candidate.BitrateKbps, requested.BitrateKbps)
		if sameProfile(ladder[len(ladder)-1], candidate) {
			continue
		}
		ladder = append(ladder, candidate)
	}
	return ladder
}

func normalizeProfile(profile Profile) Profile {
	if profile.Width <= 0 {
		profile.Width = 1280
	}
	if profile.Height <= 0 {
		profile.Height = 720
	}
	if profile.FPS <= 0 {
		profile.FPS = 30
	}
	if profile.BitrateKbps <= 0 {
		profile.BitrateKbps = bitrateFor(profile.Width, profile.Height, profile.FPS)
	}
	profile.Width -= profile.Width % 2
	profile.Height -= profile.Height % 2
	return profile
}

func bitrateFor(width, height, fps int) int {
	for _, profile := range canonicalProfiles {
		if profile.Width <= width && profile.Height <= height && profile.FPS <= fps {
			return profile.BitrateKbps
		}
	}
	return canonicalProfiles[len(canonicalProfiles)-1].BitrateKbps
}

func sameProfile(left, right Profile) bool {
	return left.Width == right.Width && left.Height == right.Height && left.FPS == right.FPS && left.BitrateKbps == right.BitrateKbps
}

func minPositive(left, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

type LocalSample struct {
	At                time.Time
	Valid             bool
	FreshFPS          float64
	FreshFPSValid     bool
	EncodedFPS        float64
	EncodeBudgetRatio float64
	RepeatedRatio     float64
	BitrateKbps       float64
	WriteFailures     int
}

type ViewerSample struct {
	At                   time.Time
	ViewerID             string
	SampleID             string
	Generation           string
	PairID               string
	Valid                bool
	PacketsReceivedDelta int64
	PacketsLostDelta     int64
	FramesDroppedDelta   int64
	DecodedFPS           float64
	IncomingBitrateKbps  float64
	JitterMS             float64
}

type DecisionType string

const (
	DecisionDownshift DecisionType = "downshift"
	DecisionRecover   DecisionType = "recover"
)

type Decision struct {
	Type   DecisionType
	Reason string
	From   Profile
	To     Profile
}

type Status struct {
	PolicyVersion string
	State         string
	Requested     Profile
	Current       Profile
	Level         int
	Levels        int
	Reason        string
	CooldownUntil time.Time
	StableSince   time.Time
	ViewerCount   int
	WeakViewers   int
}

type viewerState struct {
	generation   string
	pairID       string
	connectedAt  time.Time
	lastAt       time.Time
	window       []bool
	latest       ViewerSample
	lastSampleID string
	confirmed    bool
}

type Controller struct {
	requested     Profile
	ladder        []Profile
	current       int
	startedAt     time.Time
	localWindow   []bool
	latestLocal   *LocalSample
	viewers       map[string]*viewerState
	cooldownUntil time.Time
	stableSince   time.Time
	state         string
	reason        string
	decisionFrom  int
	failures      int
}

func NewController(requested Profile, startedAt time.Time) *Controller {
	requested = normalizeProfile(requested)
	return &Controller{
		requested: requested,
		ladder:    BuildLadder(requested),
		startedAt: startedAt,
		viewers:   make(map[string]*viewerState),
		state:     "observing",
	}
}

func (c *Controller) Current() Profile {
	return c.ladder[c.current]
}

func (c *Controller) Status(now time.Time) Status {
	viewerCount := 0
	weakViewers := 0
	for _, viewer := range c.viewers {
		if !viewerFresh(now, viewer) {
			continue
		}
		viewerCount++
		if viewer.confirmed {
			weakViewers++
		}
	}
	return Status{
		PolicyVersion: PolicyVersion,
		State:         c.state,
		Requested:     c.requested,
		Current:       c.Current(),
		Level:         c.current,
		Levels:        len(c.ladder),
		Reason:        c.reason,
		CooldownUntil: c.cooldownUntil,
		StableSince:   c.stableSince,
		ViewerCount:   viewerCount,
		WeakViewers:   weakViewers,
	}
}

func (c *Controller) Observe(now time.Time, local *LocalSample, viewers []ViewerSample) *Decision {
	localBad := false
	if local != nil && local.Valid {
		copy := *local
		copy.At = now
		c.latestLocal = &copy
		localBad = isLocalBad(copy, c.Current())
		c.localWindow = appendWindow(c.localWindow, localBad, 5)
	}

	for _, sample := range viewers {
		c.observeViewer(now, sample)
	}
	c.pruneViewers(now)

	networkBad := false
	for _, viewer := range c.viewers {
		if viewerFresh(now, viewer) && viewer.confirmed {
			networkBad = true
			break
		}
	}

	if (localBad || networkBad) && !c.stableSince.IsZero() {
		c.stableSince = time.Time{}
	}

	if now.Before(c.cooldownUntil) {
		c.state = "cooldown"
		return nil
	}

	localConfirmed := now.Sub(c.startedAt) >= localObservation && confirmedWindow(c.localWindow, 5, 4, 3)
	if c.current < len(c.ladder)-1 && (localConfirmed || networkBad) {
		reason := "network"
		if localConfirmed {
			reason = "local-performance"
		}
		return c.move(now, c.current+1, DecisionDownshift, reason, downshiftCooldown)
	}

	if c.current > 0 && c.recoveryStable(now) {
		if c.stableSince.IsZero() {
			c.stableSince = now
		}
		if now.Sub(c.stableSince) >= recoveryStableDuration {
			return c.move(now, c.current-1, DecisionRecover, "stable-headroom", recoveryCooldown)
		}
		c.state = "recovering"
		c.reason = "stable-headroom"
		return nil
	}

	if c.current > 0 {
		c.stableSince = time.Time{}
	}
	if localBad || networkBad {
		c.state = "degraded-suspected"
	} else if now.Sub(c.startedAt) < localObservation {
		c.state = "observing"
	} else {
		c.state = "normal"
		c.reason = ""
	}
	return nil
}

func (c *Controller) move(now time.Time, level int, decisionType DecisionType, reason string, cooldown time.Duration) *Decision {
	from := c.Current()
	c.decisionFrom = c.current
	c.current = level
	to := c.Current()
	c.localWindow = nil
	for _, viewer := range c.viewers {
		viewer.window = nil
		viewer.confirmed = false
		viewer.connectedAt = now
	}
	c.stableSince = time.Time{}
	c.cooldownUntil = now.Add(cooldown)
	c.state = "cooldown"
	c.reason = reason
	return &Decision{Type: decisionType, Reason: reason, From: from, To: to}
}

func (c *Controller) Commit(_ Decision) {
	c.failures = 0
}

func (c *Controller) Reject(decision Decision, now time.Time, reason string) {
	if decision.To == c.Current() && c.decisionFrom >= 0 && c.decisionFrom < len(c.ladder) {
		c.current = c.decisionFrom
	}
	c.localWindow = nil
	for _, viewer := range c.viewers {
		viewer.window = nil
		viewer.confirmed = false
		viewer.connectedAt = now
	}
	c.stableSince = time.Time{}
	c.failures++
	c.reason = reason
	if c.failures >= 2 {
		c.state = "frozen"
		c.cooldownUntil = now.Add(30 * time.Second)
		return
	}
	c.state = "cooldown"
	c.cooldownUntil = now.Add(10 * time.Second)
}

func (c *Controller) observeViewer(now time.Time, sample ViewerSample) {
	if sample.ViewerID == "" || !sample.Valid || sample.PacketsReceivedDelta < 0 || sample.PacketsLostDelta < 0 || sample.FramesDroppedDelta < 0 {
		return
	}
	if sample.PacketsReceivedDelta+sample.PacketsLostDelta < 20 {
		return
	}
	sample.At = now
	viewer := c.viewers[sample.ViewerID]
	if viewer == nil || viewer.generation != sample.Generation || viewer.pairID != sample.PairID {
		viewer = &viewerState{
			generation:  sample.Generation,
			pairID:      sample.PairID,
			connectedAt: now,
		}
		c.viewers[sample.ViewerID] = viewer
	}
	if sample.SampleID != "" && viewer.lastSampleID == sample.SampleID {
		return
	}
	bad := isViewerBad(sample, c.Current())
	viewer.window = appendWindow(viewer.window, bad, 7)
	viewer.lastAt = now
	viewer.latest = sample
	viewer.lastSampleID = sample.SampleID
	viewer.confirmed = now.Sub(viewer.connectedAt) >= networkObservation && confirmedWindow(viewer.window, 7, 5, 3)
}

func (c *Controller) pruneViewers(now time.Time) {
	for id, viewer := range c.viewers {
		if !viewerFresh(now, viewer) {
			delete(c.viewers, id)
		}
	}
}

func (c *Controller) recoveryStable(now time.Time) bool {
	if c.latestLocal == nil || !c.latestLocal.Valid || now.Sub(c.latestLocal.At) > viewerSampleTTL {
		return false
	}
	profile := c.Current()
	if (c.latestLocal.FreshFPSValid && c.latestLocal.FreshFPS < float64(profile.FPS)*0.95) ||
		c.latestLocal.EncodedFPS < float64(profile.FPS)*0.95 ||
		c.latestLocal.EncodeBudgetRatio >= 0.65 {
		return false
	}
	for _, viewer := range c.viewers {
		if !viewerFresh(now, viewer) {
			continue
		}
		sample := viewer.latest
		if lossPercent(sample) >= 1 || sample.JitterMS >= 40 || sample.DecodedFPS < float64(profile.FPS)*0.9 {
			return false
		}
	}
	return true
}

func isLocalBad(sample LocalSample, profile Profile) bool {
	return (sample.FreshFPSValid && sample.FreshFPS < float64(profile.FPS)*0.85) ||
		sample.EncodedFPS < float64(profile.FPS)*0.85 ||
		sample.EncodeBudgetRatio >= 0.85
}

func isViewerBad(sample ViewerSample, profile Profile) bool {
	if lossPercent(sample) <= 5 {
		return false
	}
	return sample.DecodedFPS < float64(profile.FPS)*0.85 ||
		sample.IncomingBitrateKbps < float64(profile.BitrateKbps)*0.8 ||
		sample.JitterMS > 100 ||
		sample.FramesDroppedDelta > 0
}

func lossPercent(sample ViewerSample) float64 {
	total := sample.PacketsReceivedDelta + sample.PacketsLostDelta
	if total <= 0 {
		return 0
	}
	return float64(sample.PacketsLostDelta) * 100 / float64(total)
}

func viewerFresh(now time.Time, viewer *viewerState) bool {
	return viewer != nil && !viewer.lastAt.IsZero() && now.Sub(viewer.lastAt) <= viewerSampleTTL
}

func appendWindow(window []bool, value bool, size int) []bool {
	window = append(window, value)
	if len(window) > size {
		window = window[len(window)-size:]
	}
	return window
}

func confirmedWindow(window []bool, size, required, trailing int) bool {
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

type LocalMetrics struct {
	windowStarted  time.Time
	freshFrames    int
	lastCaptureID  int64
	encodedFrames  int
	encodeAttempts int
	encodedBytes   int
	repeated       int
	writeFailures  int
	encodeTimes    []time.Duration
}

func NewLocalMetrics(startedAt time.Time) *LocalMetrics {
	return &LocalMetrics{windowStarted: startedAt}
}

func (m *LocalMetrics) RecordCapture(captureID int64, _ time.Time) {
	if captureID == 0 || captureID == m.lastCaptureID {
		return
	}
	m.lastCaptureID = captureID
	m.freshFrames++
}

func (m *LocalMetrics) RecordEncode(duration time.Duration, encodedBytes int, repeated bool, writeOK bool) {
	m.encodeAttempts++
	if duration >= 0 {
		m.encodeTimes = append(m.encodeTimes, duration)
	}
	if encodedBytes > 0 {
		m.encodedFrames++
		m.encodedBytes += encodedBytes
	}
	if repeated {
		m.repeated++
	}
	if !writeOK {
		m.writeFailures++
	}
}

func (m *LocalMetrics) Snapshot(now time.Time, profile Profile) (LocalSample, bool) {
	elapsed := now.Sub(m.windowStarted).Seconds()
	if elapsed < 2 || m.encodeAttempts == 0 {
		return LocalSample{}, false
	}
	profile = normalizeProfile(profile)
	encodeTimes := append([]time.Duration(nil), m.encodeTimes...)
	sort.Slice(encodeTimes, func(left, right int) bool { return encodeTimes[left] < encodeTimes[right] })
	p95 := time.Duration(0)
	if len(encodeTimes) > 0 {
		index := (len(encodeTimes)*95 + 99) / 100
		if index < 1 {
			index = 1
		}
		p95 = encodeTimes[index-1]
	}
	frameBudget := time.Second / time.Duration(profile.FPS)
	freshFPS := float64(m.freshFrames) / elapsed
	freshThreshold := float64(profile.FPS) * 0.2
	if freshThreshold < 5 {
		freshThreshold = 5
	}
	sample := LocalSample{
		At:                now,
		Valid:             true,
		FreshFPS:          freshFPS,
		FreshFPSValid:     freshFPS >= freshThreshold,
		EncodedFPS:        float64(m.encodedFrames) / elapsed,
		EncodeBudgetRatio: float64(p95) / float64(frameBudget),
		RepeatedRatio:     float64(m.repeated) / float64(m.encodeAttempts),
		BitrateKbps:       float64(m.encodedBytes*8) / elapsed / 1000,
		WriteFailures:     m.writeFailures,
	}
	m.windowStarted = now
	m.freshFrames = 0
	m.encodedFrames = 0
	m.encodeAttempts = 0
	m.encodedBytes = 0
	m.repeated = 0
	m.writeFailures = 0
	m.encodeTimes = nil
	return sample, true
}
