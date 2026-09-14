package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"PartyLAN/internal/gamecast/adaptive"
	"PartyLAN/internal/gamecast/screegop2p"
	"PartyLAN/internal/video"
)

func (a *App) runGameCastP2PLayerVideoLoop(
	ctx context.Context,
	host gameCastNativePublisher,
	getFrame func() (*video.Frame, error),
	stopCapture func() error,
	encoders []gameCastVideoEncoder,
	session *shareSession,
	mode string,
	shouldRetry func(error) bool,
	onFatalCaptureError func(error),
	done chan<- struct{},
) {
	defer close(done)
	layeredHost, ok := host.(gameCastP2PLayerPublisher)
	if !ok || len(encoders) == 0 || encoders[0].encoder == nil {
		log.Printf("[gamecast/%s] P2P layer loop has no initial encoder", mode)
		return
	}

	captureCtx, cancelCapture := context.WithCancel(ctx)
	defer cancelCapture()
	latestFrames := make(chan *video.Frame, 1)
	captureErrors := make(chan error, 1)
	captureUnavailable := make(chan struct{}, 1)
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		lastTransientLog := time.Time{}
		for {
			if captureCtx.Err() != nil {
				return
			}
			frame, err := getFrame()
			if err != nil {
				if shouldRetry != nil && shouldRetry(err) {
					select {
					case captureUnavailable <- struct{}{}:
					default:
					}
					if time.Since(lastTransientLog) >= 5*time.Second {
						log.Printf("[gamecast/%s] frame temporarily unavailable; keeping send clock active: %v", mode, err)
						lastTransientLog = time.Now()
					}
					continue
				}
				select {
				case captureErrors <- err:
				default:
				}
				return
			}
			offerLatestGameCastVideoFrame(latestFrames, frame)
		}
	}()

	pool := newGameCastP2PLayerPool(p2pAdaptiveLadder(session), encoders[0], nil)
	for _, extra := range encoders[1:] {
		if extra.encoder != nil {
			_ = extra.encoder.Close()
		}
	}
	defer func() {
		cancelCapture()
		select {
		case <-captureDone:
		case <-time.After(500 * time.Millisecond):
			log.Printf("[gamecast/%s] capture worker did not stop within 500ms", mode)
		}
		if stopCapture != nil {
			_ = stopCapture()
		}
		pool.Close()
	}()

	qualityUpdates := make(chan gameCastQualityUpdate, 1)
	a.mu.Lock()
	participantID := a.gamecastParticipantID
	a.mu.Unlock()
	go a.runGameCastQualityPoller(ctx, session.roomID, participantID, qualityUpdates)

	controller := adaptive.NewPerViewerController(pool.ladder, time.Now())
	qualityUpdate := gameCastQualityUpdate{}
	lastFrame := (*video.Frame)(nil)
	lastControlAt := time.Time{}
	nextFrameAt := time.Now()
	servedKeyFrameSeq := a.mediaKeyFrameSeq.Load()
	keyFrameGate := newGameCastKeyFrameGate(time.Now())
	primed := false
	sessionLayers := make(map[string]string)
	loadWindowStarted := time.Now()
	loadDuration := time.Duration(0)
	loadBadWindow := make([]bool, 0, 3)
	lastOverloadLog := time.Time{}
	forceControl := false
	pendingLayers := make(map[int]<-chan gameCastP2PLayerPreparation)

	for {
		frameDuration := time.Second / time.Duration(maxInt(session.fps, 1))
		if !waitForWebRTCFrameSlot(ctx, &nextFrameAt, frameDuration) {
			return
		}
		select {
		case err := <-captureErrors:
			if err != nil && ctx.Err() == nil && onFatalCaptureError != nil {
				go onFatalCaptureError(err)
			}
			return
		default:
		}
		for {
			select {
			case update := <-qualityUpdates:
				qualityUpdate = update
			default:
				goto qualityDrained
			}
		}
	qualityDrained:
		for {
			select {
			case <-captureUnavailable:
			default:
				goto captureUnavailableDrained
			}
		}
	captureUnavailableDrained:

		var latestFrame *video.Frame
	drainLatest:
		for {
			select {
			case latestFrame = <-latestFrames:
			default:
				break drainLatest
			}
		}
		if latestFrame != nil {
			lastFrame = latestFrame
		}
		if lastFrame == nil {
			continue
		}

		now := time.Now()
		for level, pending := range pendingLayers {
			select {
			case prepared := <-pending:
				delete(pendingLayers, level)
				if err := pool.CompletePreparation(prepared, now); err != nil {
					log.Printf("[gamecast/%s] prepare P2P layer=%d failed: %v", mode, level, err)
				} else {
					log.Printf("[gamecast/%s] prepared P2P layer=%d off media loop", mode, level)
				}
				forceControl = true
			default:
			}
		}
		if !primed {
			if err := pool.Prime(0, lastFrame, now); err != nil {
				log.Printf("[gamecast/%s] prime primary P2P layer failed: %v", mode, err)
				if onFatalCaptureError != nil {
					go onFatalCaptureError(err)
				}
				return
			}
			if idr := pool.LastIDR(0); len(idr) > 0 {
				if err := host.WriteH264LayerSample("0", idr, layerDuration(pool.slots[0].entry.layer)); err != nil {
					log.Printf("[gamecast/%s] write primary IDR failed: %v", mode, err)
				}
			}
			primed = true
		}

		entries := pool.Entries()
		a.applyGameCastVideoKeyFrameRequest(entries, &servedKeyFrameSeq, keyFrameGate)
		for _, result := range pool.EncodeDue(lastFrame, now) {
			loadDuration += result.EncodeTime
			if result.Quarantined {
				pool.Remove(result.Level, now)
				forceControl = true
				log.Printf("[gamecast/%s] quarantined P2P layer=%d after runtime failures", mode, result.Level)
				continue
			}
			if result.Err != nil {
				log.Printf("[gamecast/%s] encode P2P layer=%d failed: %v", mode, result.Level, result.Err)
				continue
			}
			if err := host.WriteH264LayerSample(strconv.Itoa(result.Level), result.Data, result.Duration); err != nil {
				log.Printf("[gamecast/%s] write P2P layer=%d failed: %v", mode, result.Level, err)
			}
		}

		if forceControl || lastControlAt.IsZero() || now.Sub(lastControlAt) >= 2*time.Second {
			forceControl = false
			lastControlAt = now
			loadRatio := p2pRecordLoadWindow(&loadWindowStarted, &loadDuration, now)
			pool.ObserveLoad(now, loadRatio)
			loadBad := loadRatio > 0.85
			loadBadWindow = append(loadBadWindow, loadBad)
			if len(loadBadWindow) > 3 {
				loadBadWindow = loadBadWindow[len(loadBadWindow)-3:]
			}
			snapshots := layeredHost.ViewerTransportSnapshots()
			if p2pConfirmedWindow(loadBadWindow, 3, 2, 2) && pool.LayerCount() > 1 {
				if level, ok := p2pDropHighestCostLayer(now, layeredHost, pool, snapshots, sessionLayers); ok {
					loadBadWindow = loadBadWindow[:0]
					if time.Since(lastOverloadLog) >= 5*time.Second {
						log.Printf("[gamecast/%s] removed highest-cost P2P layer=%d after aggregate encoder overload", mode, level)
						lastOverloadLog = now
					}
				}
			}
			samples := p2pViewerSamples(snapshots, qualityUpdate, now)
			controller.Observe(now, samples)
			ensureLayer := func(level int, frame *video.Frame, at time.Time) error {
				if _, ok := pendingLayers[level]; ok {
					return nil
				}
				pending, err := pool.PrepareAsync(ctx, level, frame, at, len(pendingLayers))
				if err != nil {
					return err
				}
				pendingLayers[level] = pending
				return nil
			}
			if err := a.reconcileP2PViewerLayersWithEnsure(now, layeredHost, pool, controller, snapshots, sessionLayers, lastFrame, ensureLayer); err != nil {
				log.Printf("[gamecast/%s] reconcile P2P layers failed: %v", mode, err)
			}
			if pool.LayerCount() == 0 && len(pendingLayers) == 0 {
				err := fmt.Errorf("all native P2P video layers are unavailable")
				if onFatalCaptureError != nil {
					go onFatalCaptureError(err)
				}
				return
			}
			p2pLogLayerStatus(mode, pool, controller, snapshots, loadBad)
			a.emitGameCastP2PLayerStats(pool, controller, snapshots, loadBad)
		}
	}
}

func p2pAdaptiveLadder(session *shareSession) []adaptive.Profile {
	if session == nil {
		return adaptive.BuildLadder(adaptive.Profile{})
	}
	return adaptive.BuildLadder(adaptive.Profile{Width: session.width, Height: session.height, FPS: session.fps, BitrateKbps: 50000})
}

func p2pViewerSamples(snapshots []screegop2p.ViewerTransportSnapshot, update gameCastQualityUpdate, now time.Time) []adaptive.PerViewerSample {
	quality := make(map[string]adaptive.ViewerSample)
	for _, sample := range qualityViewerSamples(update) {
		quality[sample.ViewerID] = sample
	}
	samples := make([]adaptive.PerViewerSample, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.PeerID == "" {
			continue
		}
		viewer := quality[snapshot.PeerID]
		receiverValid := viewer.Valid
		sampleID := viewer.SampleID
		generation := viewer.Generation
		pairID := viewer.PairID
		if generation == "" {
			generation = snapshot.SessionID
		}
		if pairID == "" {
			pairID = snapshot.SessionID
		}
		if snapshot.EstimateValid {
			sampleID = fmt.Sprintf("bwe-%d-%s", now.UnixNano(), snapshot.SessionID)
		}
		if sampleID == "" && !snapshot.EstimateValid {
			continue
		}
		samples = append(samples, adaptive.PerViewerSample{
			At: now, ViewerID: snapshot.PeerID, SampleID: sampleID,
			Generation: generation, PairID: pairID, ReceiverValid: receiverValid,
			PacketsReceivedDelta: viewer.PacketsReceivedDelta, PacketsLostDelta: viewer.PacketsLostDelta,
			FramesDroppedDelta: viewer.FramesDroppedDelta, DecodedFPS: viewer.DecodedFPS,
			IncomingBitrateKbps: viewer.IncomingBitrateKbps, JitterMS: viewer.JitterMS,
			EstimateValid: snapshot.EstimateValid, EstimatedBitrateKbps: snapshot.EstimatedBitrateKbps,
		})
	}
	return samples
}

func (a *App) reconcileP2PViewerLayers(
	now time.Time,
	host gameCastP2PLayerPublisher,
	pool *gameCastP2PLayerPool,
	controller *adaptive.PerViewerController,
	snapshots []screegop2p.ViewerTransportSnapshot,
	sessionLayers map[string]string,
	frame *video.Frame,
) error {
	return a.reconcileP2PViewerLayersWithEnsure(now, host, pool, controller, snapshots, sessionLayers, frame, pool.Ensure)
}

func (a *App) reconcileP2PViewerLayersWithEnsure(
	now time.Time,
	host gameCastP2PLayerPublisher,
	pool *gameCastP2PLayerPool,
	controller *adaptive.PerViewerController,
	snapshots []screegop2p.ViewerTransportSnapshot,
	sessionLayers map[string]string,
	frame *video.Frame,
	ensureLayer func(int, *video.Frame, time.Time) error,
) error {
	liveSessions := make(map[string]bool, len(snapshots))
	for _, snapshot := range snapshots {
		liveSessions[snapshot.SessionID] = true
	}
	for sessionID := range sessionLayers {
		if !liveSessions[sessionID] {
			delete(sessionLayers, sessionID)
		}
	}
	targets := controller.Targets(now)
	targetByViewer := make(map[string]adaptive.ViewerTarget, len(targets))
	for _, target := range targets {
		targetByViewer[target.ViewerID] = adaptive.ViewerTarget{ViewerID: target.ViewerID, Level: p2pResolveAvailableTarget(pool, target.Level, now)}
	}
	for _, snapshot := range snapshots {
		if _, ok := targetByViewer[snapshot.PeerID]; !ok {
			targetByViewer[snapshot.PeerID] = adaptive.ViewerTarget{ViewerID: snapshot.PeerID, Level: p2pResolveAvailableTarget(pool, 0, now)}
		}
		if _, ok := sessionLayers[snapshot.SessionID]; !ok {
			layerID := snapshot.LayerID
			if layerID == "" {
				layerID = "0"
			}
			sessionLayers[snapshot.SessionID] = layerID
		}
	}
	allTargets := make([]adaptive.ViewerTarget, 0, len(targetByViewer))
	for _, target := range targetByViewer {
		allTargets = append(allTargets, target)
	}
	sort.Slice(allTargets, func(left, right int) bool { return allTargets[left].ViewerID < allTargets[right].ViewerID })
	selected := adaptive.SelectActiveLevels(allTargets, gameCastP2PMaxLayers)
	if len(selected) == 0 && pool.LayerCount() > 0 {
		selected = []int{0}
	}
	var firstErr error
	for round := 0; round < gameCastP2PMaxLayers; round++ {
		ready := pool.ReadyLevels()
		activeForAssignment := p2pIntersectLevels(selected, ready)
		if len(activeForAssignment) == 0 {
			activeForAssignment = ready
		}
		assignments := adaptive.AssignViewerLevels(allTargets, activeForAssignment)
		assignmentByViewer := make(map[string]adaptive.ViewerAssignment, len(assignments))
		for _, assignment := range assignments {
			assignmentByViewer[assignment.ViewerID] = assignment
		}
		for _, snapshot := range snapshots {
			assignment, ok := assignmentByViewer[snapshot.PeerID]
			if !ok || pool.LastIDR(assignment.AssignedLevel) == nil {
				continue
			}
			assignedLayer := strconv.Itoa(assignment.AssignedLevel)
			if sessionLayers[snapshot.SessionID] == assignedLayer {
				pool.MarkUsed(assignment.AssignedLevel, now)
				continue
			}
			if err := host.SwitchViewerLayer(snapshot.SessionID, assignedLayer, pool.LastIDR(assignment.AssignedLevel), layerDuration(pool.slots[assignment.AssignedLevel].entry.layer)); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			sessionLayers[snapshot.SessionID] = assignedLayer
			pool.MarkUsed(assignment.AssignedLevel, now)
		}

		referenced := make(map[int]bool)
		for _, layerID := range sessionLayers {
			if level, err := strconv.Atoi(layerID); err == nil {
				referenced[level] = true
			}
		}
		missing := make([]int, 0)
		for _, level := range selected {
			if _, ok := pool.Layer(level); !ok && pool.IsAvailable(level, now) {
				missing = append(missing, level)
			}
		}
		capacityNeeded := pool.LayerCount()+len(missing) > gameCastP2PMaxLayers
		for level, slot := range pool.slots {
			if containsInt(selected, level) || referenced[level] {
				continue
			}
			if capacityNeeded || now.Sub(slot.lastUsedAt) >= 12*time.Second {
				pool.Remove(level, now)
				capacityNeeded = pool.LayerCount()+len(missing) > gameCastP2PMaxLayers
			}
		}
		if len(missing) == 0 {
			return firstErr
		}
		for _, level := range missing {
			if pool.LayerCount() >= gameCastP2PMaxLayers {
				if evictLevel, ok := p2pHighestCostUnreferencedLayer(pool, sessionLayers); ok {
					pool.Remove(evictLevel, now)
				}
			}
			if err := ensureLayer(level, frame, now); err != nil {
				log.Printf("[gamecast/p2p] quality layer %d unavailable: %v", level, err)
			}
		}
	}
	return firstErr
}

func p2pResolveAvailableTarget(pool *gameCastP2PLayerPool, target int, now time.Time) int {
	if target < 0 {
		target = 0
	}
	if target >= len(pool.ladder) {
		target = len(pool.ladder) - 1
	}
	for level := target; level < len(pool.ladder); level++ {
		if pool.IsAvailable(level, now) {
			return level
		}
	}
	for level := target - 1; level >= 0; level-- {
		if pool.IsAvailable(level, now) {
			return level
		}
	}
	return target
}

func p2pIntersectLevels(selected, ready []int) []int {
	result := make([]int, 0, len(selected))
	for _, level := range selected {
		if containsInt(ready, level) {
			result = append(result, level)
		}
	}
	return result
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func p2pHighestCostUnreferencedLayer(pool *gameCastP2PLayerPool, sessionLayers map[string]string) (int, bool) {
	referenced := make(map[int]bool)
	for _, layerID := range sessionLayers {
		if level, err := strconv.Atoi(layerID); err == nil {
			referenced[level] = true
		}
	}
	levels := pool.ReadyLevels()
	for _, level := range levels {
		if !referenced[level] {
			return level, true
		}
	}
	return 0, false
}

func p2pDropHighestCostLayer(now time.Time, host gameCastP2PLayerPublisher, pool *gameCastP2PLayerPool, snapshots []screegop2p.ViewerTransportSnapshot, sessionLayers map[string]string) (int, bool) {
	levels := pool.ReadyLevels()
	if len(levels) < 2 {
		return 0, false
	}
	highestCost := levels[0]
	fallback := levels[1]
	idr := pool.LastIDR(fallback)
	fallbackSlot := pool.slots[fallback]
	if len(idr) == 0 || fallbackSlot == nil {
		return 0, false
	}
	for _, snapshot := range snapshots {
		currentLayer := sessionLayers[snapshot.SessionID]
		if currentLayer == "" {
			currentLayer = snapshot.LayerID
		}
		current, err := strconv.Atoi(currentLayer)
		if err != nil || current != highestCost {
			continue
		}
		if err := host.SwitchViewerLayer(snapshot.SessionID, strconv.Itoa(fallback), idr, layerDuration(fallbackSlot.entry.layer)); err != nil {
			log.Printf("[gamecast/p2p] overload migration session=%s layer=%d->%d failed: %v", snapshot.SessionID, highestCost, fallback, err)
			return 0, false
		}
		sessionLayers[snapshot.SessionID] = strconv.Itoa(fallback)
		pool.MarkUsed(fallback, now)
	}
	pool.Quarantine(highestCost, now)
	return highestCost, true
}

func p2pRecordLoadWindow(started *time.Time, duration *time.Duration, now time.Time) float64 {
	if started == nil || duration == nil || started.IsZero() {
		return 0
	}
	elapsed := now.Sub(*started)
	if elapsed <= 0 {
		return 0
	}
	ratio := float64(*duration) / float64(elapsed)
	*started = now
	*duration = 0
	return ratio
}

func p2pConfirmedWindow(window []bool, size, required, trailing int) bool {
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

func p2pLogLayerStatus(mode string, pool *gameCastP2PLayerPool, controller *adaptive.PerViewerController, snapshots []screegop2p.ViewerTransportSnapshot, overloaded bool) {
	if controller == nil {
		return
	}
	status := controller.Status(time.Now())
	weak := 0
	for _, entry := range status {
		if entry.Weak {
			weak++
		}
	}
	log.Printf("[gamecast/%s] p2p-layer-pool policy=%s layers=%v viewers=%d weak=%d overloaded=%t", mode, adaptive.PerViewerPolicyVersion, pool.ReadyLevels(), len(snapshots), weak, overloaded)
}
