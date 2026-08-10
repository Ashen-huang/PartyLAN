package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"PartyLAN/internal/gamecast/adaptive"
	"PartyLAN/internal/gamecast/nativehost"
	"PartyLAN/internal/video"
)

const (
	gameCastP2PMaxLayers       = 3
	gameCastP2PLayerQuarantine = 30 * time.Second
)

type gameCastP2PLayerEncoderFactory func(nativehost.VideoLayer) (gameCastVideoEncoder, error)

type gameCastP2PLayerSlot struct {
	level       int
	entry       gameCastVideoEncoder
	nextFrameAt time.Time
	lastIDR     []byte
	lastUsedAt  time.Time
	lastEncode  time.Duration
}

type gameCastP2PLayerHealth struct {
	quarantinedUntil time.Time
	recoveryFailures int
	disabled         bool
	windowStarted    time.Time
	windowAttempts   int
	windowFailures   int
	consecutive      int
}

type gameCastP2PLayerPool struct {
	ladder          []adaptive.Profile
	slots           map[int]*gameCastP2PLayerSlot
	health          map[int]*gameCastP2PLayerHealth
	factory         gameCastP2PLayerEncoderFactory
	maxLayer        int
	loadStableSince time.Time
}

type gameCastP2PLayerEncodeResult struct {
	Level       int
	Data        []byte
	Duration    time.Duration
	EncodeTime  time.Duration
	Err         error
	Quarantined bool
}

type gameCastP2PLayerPreparation struct {
	Level      int
	Entry      gameCastVideoEncoder
	IDR        []byte
	EncodeTime time.Duration
	Err        error
}

func newGameCastP2PLayerPool(ladder []adaptive.Profile, initial gameCastVideoEncoder, factory gameCastP2PLayerEncoderFactory) *gameCastP2PLayerPool {
	if factory == nil {
		factory = func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
			return newGameCastVideoEncoder(layer)
		}
	}
	pool := &gameCastP2PLayerPool{
		ladder:  append([]adaptive.Profile(nil), ladder...),
		slots:   make(map[int]*gameCastP2PLayerSlot),
		health:  make(map[int]*gameCastP2PLayerHealth),
		factory: factory, maxLayer: gameCastP2PMaxLayers,
	}
	if initial.encoder != nil && len(pool.ladder) > 0 {
		pool.slots[0] = &gameCastP2PLayerSlot{level: 0, entry: initial}
	}
	return pool
}

func (p *gameCastP2PLayerPool) LayerCount() int {
	return len(p.slots)
}

func (p *gameCastP2PLayerPool) ReadyLevels() []int {
	levels := make([]int, 0, len(p.slots))
	for level := range p.slots {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	return levels
}

func (p *gameCastP2PLayerPool) Layer(level int) (*gameCastP2PLayerSlot, bool) {
	slot, ok := p.slots[level]
	return slot, ok
}

func (p *gameCastP2PLayerPool) Entries() []gameCastVideoEncoder {
	levels := p.ReadyLevels()
	entries := make([]gameCastVideoEncoder, 0, len(levels))
	for _, level := range levels {
		if slot := p.slots[level]; slot != nil {
			entries = append(entries, slot.entry)
		}
	}
	return entries
}

func (p *gameCastP2PLayerPool) Prime(level int, frame *video.Frame, now time.Time) error {
	slot := p.slots[level]
	if slot == nil {
		return fmt.Errorf("quality layer %d is not active", level)
	}
	if len(slot.lastIDR) > 0 {
		return nil
	}
	return p.prime(slot, frame, now)
}

func (p *gameCastP2PLayerPool) Ensure(level int, frame *video.Frame, now time.Time) error {
	if level < 0 || level >= len(p.ladder) {
		return fmt.Errorf("quality layer %d is outside ladder", level)
	}
	if _, ok := p.slots[level]; ok {
		return nil
	}
	health := p.layerHealth(level)
	if health.disabled || now.Before(health.quarantinedUntil) {
		return fmt.Errorf("quality layer %d is quarantined", level)
	}
	if len(p.slots) >= p.maxLayer {
		return fmt.Errorf("quality layer pool is full at %d layers", p.maxLayer)
	}
	profile := p.ladder[level]
	entry, err := p.factory(gameCastP2PVideoLayer(profile, level))
	if err != nil {
		p.recordPreparationFailure(level, now)
		return err
	}
	slot := &gameCastP2PLayerSlot{level: level, entry: entry, lastUsedAt: now}
	p.slots[level] = slot
	if frame != nil {
		if err := p.prime(slot, frame, now); err != nil {
			p.remove(level, now, true)
			return err
		}
	}
	health.quarantinedUntil = time.Time{}
	health.windowStarted = time.Time{}
	health.windowAttempts = 0
	health.windowFailures = 0
	health.consecutive = 0
	return nil
}

func (p *gameCastP2PLayerPool) PrepareAsync(
	ctx context.Context,
	level int,
	frame *video.Frame,
	now time.Time,
	reserved int,
) (<-chan gameCastP2PLayerPreparation, error) {
	if level < 0 || level >= len(p.ladder) {
		return nil, fmt.Errorf("quality layer %d is outside ladder", level)
	}
	if frame == nil {
		return nil, fmt.Errorf("quality layer %d preparation requires a frame", level)
	}
	if _, ok := p.slots[level]; ok {
		return nil, fmt.Errorf("quality layer %d is already active", level)
	}
	if !p.IsAvailable(level, now) {
		return nil, fmt.Errorf("quality layer %d is quarantined", level)
	}
	if len(p.slots)+maxInt(reserved, 0) >= p.maxLayer {
		return nil, fmt.Errorf("quality layer pool is full at %d active or preparing layers", p.maxLayer)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	profile := p.ladder[level]
	layer := gameCastP2PVideoLayer(profile, level)
	factory := p.factory
	cloned := cloneVideoFrame(frame)
	result := make(chan gameCastP2PLayerPreparation, 1)
	go func() {
		prepared := gameCastP2PLayerPreparation{Level: level}
		entry, err := factory(layer)
		if err != nil {
			prepared.Err = err
		} else {
			prepared.Entry = entry
			if entry.encoder == nil {
				prepared.Err = fmt.Errorf("quality layer %d factory returned an empty encoder", level)
			} else if err := entry.encoder.ForceKeyFrame(); err != nil {
				prepared.Err = fmt.Errorf("force layer %d keyframe: %w", level, err)
			} else {
				started := time.Now()
				prepared.IDR, prepared.Err = entry.encoder.Encode(
					prepareWebRTCFrameForEncode(cloned, layer.Width, layer.Height),
				)
				prepared.EncodeTime = time.Since(started)
				if prepared.Err == nil && !h264PreparedAccessUnit(prepared.IDR) {
					prepared.Err = fmt.Errorf("prime layer %d omitted SPS/PPS or IDR", level)
				}
			}
		}
		if prepared.Err != nil && prepared.Entry.encoder != nil {
			_ = prepared.Entry.encoder.Close()
			prepared.Entry.encoder = nil
		}
		select {
		case result <- prepared:
		case <-ctx.Done():
			if prepared.Entry.encoder != nil {
				_ = prepared.Entry.encoder.Close()
			}
		}
	}()
	return result, nil
}

func (p *gameCastP2PLayerPool) CompletePreparation(prepared gameCastP2PLayerPreparation, now time.Time) error {
	if prepared.Err != nil {
		p.recordPreparationFailure(prepared.Level, now)
		return prepared.Err
	}
	if prepared.Entry.encoder == nil || !h264PreparedAccessUnit(prepared.IDR) {
		p.recordPreparationFailure(prepared.Level, now)
		return fmt.Errorf("quality layer %d preparation is incomplete", prepared.Level)
	}
	if _, ok := p.slots[prepared.Level]; ok {
		_ = prepared.Entry.encoder.Close()
		return nil
	}
	if len(p.slots) >= p.maxLayer {
		_ = prepared.Entry.encoder.Close()
		return fmt.Errorf("quality layer pool is full at %d layers", p.maxLayer)
	}
	slot := &gameCastP2PLayerSlot{
		level: prepared.Level, entry: prepared.Entry, lastUsedAt: now,
		lastIDR: append([]byte(nil), prepared.IDR...), lastEncode: prepared.EncodeTime,
	}
	slot.nextFrameAt = now.Add(layerDuration(slot.entry.layer))
	p.slots[prepared.Level] = slot
	health := p.layerHealth(prepared.Level)
	health.quarantinedUntil = time.Time{}
	health.windowStarted = time.Time{}
	health.windowAttempts = 0
	health.windowFailures = 0
	health.consecutive = 0
	return nil
}

func (p *gameCastP2PLayerPool) prime(slot *gameCastP2PLayerSlot, frame *video.Frame, now time.Time) error {
	if err := slot.entry.encoder.ForceKeyFrame(); err != nil {
		p.recordPreparationFailure(slot.level, now)
		return fmt.Errorf("force layer %d keyframe: %w", slot.level, err)
	}
	started := time.Now()
	data, err := slot.entry.encoder.Encode(prepareWebRTCFrameForEncode(frame, slot.entry.layer.Width, slot.entry.layer.Height))
	slot.lastEncode = time.Since(started)
	if err != nil {
		p.recordPreparationFailure(slot.level, now)
		return fmt.Errorf("prime layer %d: %w", slot.level, err)
	}
	if !h264PreparedAccessUnit(data) {
		p.recordPreparationFailure(slot.level, now)
		return fmt.Errorf("prime layer %d omitted SPS/PPS or IDR", slot.level)
	}
	slot.lastIDR = append(slot.lastIDR[:0], data...)
	slot.nextFrameAt = now.Add(layerDuration(slot.entry.layer))
	return nil
}

func (p *gameCastP2PLayerPool) EncodeDue(frame *video.Frame, now time.Time) []gameCastP2PLayerEncodeResult {
	levels := p.ReadyLevels()
	results := make([]gameCastP2PLayerEncodeResult, 0, len(levels))
	for _, level := range levels {
		slot := p.slots[level]
		if slot == nil || frame == nil || (!slot.nextFrameAt.IsZero() && now.Before(slot.nextFrameAt)) {
			continue
		}
		duration := layerDuration(slot.entry.layer)
		started := time.Now()
		data, err := slot.entry.encoder.Encode(prepareWebRTCFrameForEncode(frame, slot.entry.layer.Width, slot.entry.layer.Height))
		encodeTime := time.Since(started)
		slot.lastEncode = encodeTime
		slot.nextFrameAt = now.Add(duration)
		result := gameCastP2PLayerEncodeResult{Level: level, Data: data, Duration: duration, EncodeTime: encodeTime, Err: err}
		if err == nil && len(data) > 0 && h264PreparedAccessUnit(data) {
			slot.lastIDR = append(slot.lastIDR[:0], data...)
		}
		result.Quarantined = p.recordRuntimeResult(level, now, err != nil)
		results = append(results, result)
	}
	return results
}

func (p *gameCastP2PLayerPool) LastIDR(level int) []byte {
	slot := p.slots[level]
	if slot == nil || len(slot.lastIDR) == 0 {
		return nil
	}
	return append([]byte(nil), slot.lastIDR...)
}

func (p *gameCastP2PLayerPool) MarkUsed(level int, now time.Time) {
	if slot := p.slots[level]; slot != nil {
		slot.lastUsedAt = now
	}
}

func (p *gameCastP2PLayerPool) Remove(level int, now time.Time) {
	p.remove(level, now, false)
}

func (p *gameCastP2PLayerPool) Quarantine(level int, now time.Time) {
	p.remove(level, now, true)
}

func (p *gameCastP2PLayerPool) ObserveLoad(now time.Time, ratio float64) {
	if ratio < 0.7 {
		if p.loadStableSince.IsZero() {
			p.loadStableSince = now
		}
		return
	}
	p.loadStableSince = time.Time{}
}

func (p *gameCastP2PLayerPool) IsAvailable(level int, now time.Time) bool {
	health := p.layerHealth(level)
	if health.disabled || now.Before(health.quarantinedUntil) {
		return false
	}
	if !health.quarantinedUntil.IsZero() {
		return !p.loadStableSince.IsZero() && now.Sub(p.loadStableSince) >= 10*time.Second
	}
	return true
}

func (p *gameCastP2PLayerPool) Close() {
	for level := range p.slots {
		p.remove(level, time.Now(), false)
	}
}

func (p *gameCastP2PLayerPool) layerHealth(level int) *gameCastP2PLayerHealth {
	health := p.health[level]
	if health == nil {
		health = &gameCastP2PLayerHealth{}
		p.health[level] = health
	}
	return health
}

func (p *gameCastP2PLayerPool) recordPreparationFailure(level int, now time.Time) {
	health := p.layerHealth(level)
	health.recoveryFailures++
	health.quarantinedUntil = now.Add(gameCastP2PLayerQuarantine)
	if health.recoveryFailures >= 2 {
		health.disabled = true
	}
}

func (p *gameCastP2PLayerPool) recordRuntimeResult(level int, now time.Time, failed bool) bool {
	health := p.layerHealth(level)
	if health.windowStarted.IsZero() {
		health.windowStarted = now
	}
	health.windowAttempts++
	if failed {
		health.windowFailures++
		health.consecutive++
	} else {
		health.consecutive = 0
	}
	windowExceeded := false
	if now.Sub(health.windowStarted) >= 2*time.Second {
		windowExceeded = health.windowAttempts >= 5 && float64(health.windowFailures)/float64(health.windowAttempts) > 0.2
		health.windowStarted = now
		health.windowAttempts = 0
		health.windowFailures = 0
	}
	if health.consecutive >= 3 || windowExceeded {
		health.quarantinedUntil = now.Add(gameCastP2PLayerQuarantine)
		health.recoveryFailures++
		if health.recoveryFailures >= 2 {
			health.disabled = true
		}
		return true
	}
	return false
}

func (p *gameCastP2PLayerPool) remove(level int, now time.Time, quarantine bool) {
	slot := p.slots[level]
	delete(p.slots, level)
	if slot != nil && slot.entry.encoder != nil {
		_ = slot.entry.encoder.Close()
	}
	if quarantine {
		health := p.layerHealth(level)
		health.quarantinedUntil = now.Add(gameCastP2PLayerQuarantine)
	}
}

func gameCastP2PVideoLayer(profile adaptive.Profile, level int) nativehost.VideoLayer {
	return nativehost.VideoLayer{
		RID: strconv.Itoa(level), Width: profile.Width, Height: profile.Height,
		FPS: profile.FPS, BitrateKbps: profile.BitrateKbps,
	}
}

func layerDuration(layer nativehost.VideoLayer) time.Duration {
	if layer.FPS <= 0 {
		return time.Second / 30
	}
	return time.Second / time.Duration(layer.FPS)
}

func initialGameCastVideoLayers(host gameCastNativePublisher, session *shareSession) []nativehost.VideoLayer {
	if session == nil {
		return nil
	}
	if _, ok := host.(gameCastP2PLayerPublisher); ok {
		ladder := adaptive.BuildLadder(adaptive.Profile{
			Width: session.width, Height: session.height, FPS: session.fps, BitrateKbps: 20000,
		})
		if len(ladder) > 0 {
			return []nativehost.VideoLayer{gameCastP2PVideoLayer(ladder[0], 0)}
		}
	}
	return nativehost.SingleVideoLayer(session.width, session.height, session.fps)
}
