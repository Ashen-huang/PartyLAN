package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"PartyLAN/internal/gamecast/adaptive"
	"PartyLAN/internal/gamecast/nativehost"
	"PartyLAN/internal/gamecast/screegop2p"
	"PartyLAN/internal/video"
)

var preparedH264AccessUnit = []byte{
	0, 0, 0, 1, 0x67, 0x42,
	0, 0, 0, 1, 0x68, 0xce,
	0, 0, 0, 1, 0x65, 0x88,
}

type fakeGameCastLayerEncoder struct {
	failEncode int
	failAt     map[int]bool
	calls      int
	closed     bool
}

type fakeP2PLayerPublisher struct {
	snapshots []screegop2p.ViewerTransportSnapshot
	switches  []string
}

func (p *fakeP2PLayerPublisher) Close() error                                             { return nil }
func (p *fakeP2PLayerPublisher) WriteH264Sample([]byte, time.Duration) error              { return nil }
func (p *fakeP2PLayerPublisher) WriteH264LayerSample(string, []byte, time.Duration) error { return nil }
func (p *fakeP2PLayerPublisher) WriteOpusSample([]byte, time.Duration) error              { return nil }
func (p *fakeP2PLayerPublisher) SessionCount() int                                        { return len(p.snapshots) }
func (p *fakeP2PLayerPublisher) ActiveVideoLayers() []nativehost.VideoLayer               { return nil }
func (p *fakeP2PLayerPublisher) SetViewerLayer(sessionID, layerID string) error {
	for index := range p.snapshots {
		if p.snapshots[index].SessionID == sessionID {
			p.snapshots[index].LayerID = layerID
			return nil
		}
	}
	return errors.New("unknown session")
}
func (p *fakeP2PLayerPublisher) SwitchViewerLayer(sessionID, layerID string, _ []byte, _ time.Duration) error {
	p.switches = append(p.switches, sessionID+":"+layerID)
	return p.SetViewerLayer(sessionID, layerID)
}
func (p *fakeP2PLayerPublisher) ViewerTransportSnapshots() []screegop2p.ViewerTransportSnapshot {
	return append([]screegop2p.ViewerTransportSnapshot(nil), p.snapshots...)
}

func (e *fakeGameCastLayerEncoder) Encode(*video.Frame) ([]byte, error) {
	e.calls++
	if e.failAt[e.calls] {
		return nil, errors.New("encode failed")
	}
	if e.failEncode > 0 {
		e.failEncode--
		return nil, errors.New("encode failed")
	}
	return append([]byte(nil), preparedH264AccessUnit...), nil
}

func (e *fakeGameCastLayerEncoder) ForceKeyFrame() error { return nil }
func (e *fakeGameCastLayerEncoder) Close() error {
	e.closed = true
	return nil
}

func TestGameCastP2PLayerPoolNeverCreatesMoreThanThreeEncoders(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	created := 0
	pool := newGameCastP2PLayerPool(ladder, gameCastVideoEncoder{}, func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
		created++
		return gameCastVideoEncoder{layer: layer, encoder: &fakeGameCastLayerEncoder{}}, nil
	})
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	now := time.Unix(20000, 0)
	for _, level := range []int{0, 2, 6} {
		if err := pool.Ensure(level, frame, now); err != nil {
			t.Fatalf("ensure layer %d: %v", level, err)
		}
	}
	if err := pool.Ensure(3, frame, now); err == nil {
		t.Fatal("fourth layer unexpectedly succeeded")
	}
	if pool.LayerCount() != 3 || created != 3 {
		t.Fatalf("pool count=%d created=%d, want 3", pool.LayerCount(), created)
	}
}

func TestGameCastP2PLayerPoolQuarantinesAfterThreeConsecutiveFailures(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	encoder := &fakeGameCastLayerEncoder{failEncode: 3}
	initial := gameCastVideoEncoder{layer: gameCastP2PVideoLayer(ladder[0], 0), encoder: encoder}
	pool := newGameCastP2PLayerPool(ladder, initial, nil)
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	now := time.Unix(21000, 0)
	for attempt := 0; attempt < 3; attempt++ {
		results := pool.EncodeDue(frame, now.Add(time.Duration(attempt)*time.Second))
		if len(results) != 1 || results[0].Err == nil {
			t.Fatalf("attempt %d results = %#v, want encode failure", attempt+1, results)
		}
		if attempt < 2 && results[0].Quarantined {
			t.Fatalf("layer quarantined too early on attempt %d", attempt+1)
		}
		if attempt == 2 && !results[0].Quarantined {
			t.Fatalf("third failure did not quarantine layer: %#v", results[0])
		}
	}
	pool.Remove(0, now.Add(2*time.Second))
	if pool.IsAvailable(0, now.Add(3*time.Second)) {
		t.Fatal("quarantined layer became immediately available")
	}
}

func TestGameCastP2PLayerPoolQuarantinesWhenTwoSecondFailureRateExceedsTwentyPercent(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	encoder := &fakeGameCastLayerEncoder{failAt: map[int]bool{1: true, 4: true}}
	initial := gameCastVideoEncoder{layer: gameCastP2PVideoLayer(ladder[0], 0), encoder: encoder}
	pool := newGameCastP2PLayerPool(ladder, initial, nil)
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	start := time.Unix(21500, 0)
	for attempt := 0; attempt < 5; attempt++ {
		results := pool.EncodeDue(frame, start.Add(time.Duration(attempt)*500*time.Millisecond))
		if len(results) != 1 {
			t.Fatalf("attempt %d results = %#v, want one result", attempt+1, results)
		}
		if attempt < 4 && results[0].Quarantined {
			t.Fatalf("layer quarantined before the two-second window closed on attempt %d", attempt+1)
		}
		if attempt == 4 && !results[0].Quarantined {
			t.Fatalf("40%% failure rate did not quarantine layer: %#v", results[0])
		}
	}
}

func TestGameCastP2PLayerPoolRequiresTenSecondsLowLoadAfterQuarantine(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	initial := gameCastVideoEncoder{layer: gameCastP2PVideoLayer(ladder[0], 0), encoder: &fakeGameCastLayerEncoder{}}
	pool := newGameCastP2PLayerPool(ladder, initial, nil)
	defer pool.Close()
	start := time.Unix(21800, 0)
	pool.Quarantine(0, start)
	if pool.IsAvailable(0, start.Add(31*time.Second)) {
		t.Fatal("layer recovered without a stable low-load window")
	}
	pool.ObserveLoad(start.Add(20*time.Second), 0.5)
	if pool.IsAvailable(0, start.Add(29*time.Second)) {
		t.Fatal("layer recovered before quarantine expired")
	}
	if !pool.IsAvailable(0, start.Add(31*time.Second)) {
		t.Fatal("layer did not recover after quarantine and 10 seconds of low load")
	}
}

func TestGameCastP2PLayerPoolKeepsOtherLayerWhenOptionalInitializationFails(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	initialEncoder := &fakeGameCastLayerEncoder{}
	initial := gameCastVideoEncoder{layer: gameCastP2PVideoLayer(ladder[0], 0), encoder: initialEncoder}
	pool := newGameCastP2PLayerPool(ladder, initial, func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
		return gameCastVideoEncoder{}, errors.New("NVENC session unavailable")
	})
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	if err := pool.Ensure(1, frame, time.Unix(22000, 0)); err == nil {
		t.Fatal("optional layer initialization unexpectedly succeeded")
	}
	if pool.LayerCount() != 1 || initialEncoder.closed {
		t.Fatalf("primary layer was not preserved: count=%d closed=%t", pool.LayerCount(), initialEncoder.closed)
	}
}

func TestGameCastP2PLayerPoolPreparesOptionalEncoderOffMediaLoop(t *testing.T) {
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	started := make(chan struct{})
	release := make(chan struct{})
	initial := gameCastVideoEncoder{layer: gameCastP2PVideoLayer(ladder[0], 0), encoder: &fakeGameCastLayerEncoder{}}
	pool := newGameCastP2PLayerPool(ladder, initial, func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
		close(started)
		<-release
		return gameCastVideoEncoder{layer: layer, encoder: &fakeGameCastLayerEncoder{}}, nil
	})
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	now := time.Unix(22500, 0)
	pending, err := pool.PrepareAsync(context.Background(), 1, frame, now, 0)
	if err != nil {
		t.Fatalf("start preparation: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preparation worker did not start")
	}
	if pool.LayerCount() != 1 {
		t.Fatalf("preparing layer became visible before IDR was ready: %d", pool.LayerCount())
	}
	close(release)
	select {
	case prepared := <-pending:
		if err := pool.CompletePreparation(prepared, now); err != nil {
			t.Fatalf("complete preparation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("preparation did not finish")
	}
	if pool.LayerCount() != 2 || pool.LastIDR(1) == nil {
		t.Fatalf("prepared layer was not atomically installed: count=%d idr=%d", pool.LayerCount(), len(pool.LastIDR(1)))
	}
}

func TestReconcileP2PLayersMovesOnlyWeakViewer(t *testing.T) {
	start := time.Unix(23000, 0)
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	factory := func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
		return gameCastVideoEncoder{layer: layer, encoder: &fakeGameCastLayerEncoder{}}, nil
	}
	initial, _ := factory(gameCastP2PVideoLayer(ladder[0], 0))
	pool := newGameCastP2PLayerPool(ladder, initial, factory)
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	if err := pool.Prime(0, frame, start); err != nil {
		t.Fatalf("prime high layer: %v", err)
	}
	if err := pool.Ensure(1, frame, start); err != nil {
		t.Fatalf("ensure weak layer: %v", err)
	}
	controller := adaptive.NewPerViewerController(ladder, start)
	for index, seconds := range []int{2, 4, 6} {
		at := start.Add(time.Duration(seconds) * time.Second)
		weakEstimate := 12000
		if index > 0 {
			weakEstimate = 7000
		}
		controller.Observe(at, []adaptive.PerViewerSample{
			p2pControllerSample("strong", at, 15000, index),
			p2pControllerSample("weak", at, weakEstimate, index),
		})
	}
	host := &fakeP2PLayerPublisher{snapshots: []screegop2p.ViewerTransportSnapshot{
		{SessionID: "session-strong", PeerID: "strong", LayerID: "0"},
		{SessionID: "session-weak", PeerID: "weak", LayerID: "0"},
	}}
	sessionLayers := make(map[string]string)
	if err := (&App{}).reconcileP2PViewerLayers(start.Add(6*time.Second), host, pool, controller, host.ViewerTransportSnapshots(), sessionLayers, frame); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(host.switches) != 1 || host.switches[0] != "session-weak:1" {
		t.Fatalf("switches = %v, want only weak viewer to layer 1", host.switches)
	}
}

func TestP2POverloadMigratesHighViewerBeforeQuarantiningLayer(t *testing.T) {
	now := time.Unix(24000, 0)
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 20000})
	factory := func(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
		return gameCastVideoEncoder{layer: layer, encoder: &fakeGameCastLayerEncoder{}}, nil
	}
	initial, _ := factory(gameCastP2PVideoLayer(ladder[0], 0))
	pool := newGameCastP2PLayerPool(ladder, initial, factory)
	defer pool.Close()
	frame := &video.Frame{Width: 1920, Height: 1080, Data: make([]byte, 1920*1080*4)}
	if err := pool.Prime(0, frame, now); err != nil {
		t.Fatalf("prime high layer: %v", err)
	}
	if err := pool.Ensure(1, frame, now); err != nil {
		t.Fatalf("ensure fallback layer: %v", err)
	}
	host := &fakeP2PLayerPublisher{snapshots: []screegop2p.ViewerTransportSnapshot{
		{SessionID: "session-high", PeerID: "high", LayerID: "0"},
		{SessionID: "session-low", PeerID: "low", LayerID: "1"},
	}}
	sessionLayers := map[string]string{"session-high": "0", "session-low": "1"}
	level, ok := p2pDropHighestCostLayer(now, host, pool, host.ViewerTransportSnapshots(), sessionLayers)
	if !ok || level != 0 {
		t.Fatalf("drop result = level:%d ok:%t, want layer 0", level, ok)
	}
	if len(host.switches) != 1 || host.switches[0] != "session-high:1" {
		t.Fatalf("switches = %v, want high viewer migrated to layer 1", host.switches)
	}
	if _, ok := pool.Layer(0); ok || pool.IsAvailable(0, now.Add(time.Second)) {
		t.Fatal("overloaded high layer was not quarantined")
	}
}

func p2pControllerSample(viewerID string, at time.Time, estimateKbps, sequence int) adaptive.PerViewerSample {
	return adaptive.PerViewerSample{
		At: at, ViewerID: viewerID, SampleID: time.Duration(sequence).String(),
		Generation: "g1", PairID: "p1", ReceiverValid: true,
		PacketsReceivedDelta: 200, PacketsLostDelta: 0, DecodedFPS: 60,
		IncomingBitrateKbps: 10000, JitterMS: 10,
		EstimateValid: true, EstimatedBitrateKbps: estimateKbps,
	}
}
