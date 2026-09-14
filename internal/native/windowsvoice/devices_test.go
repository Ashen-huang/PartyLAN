package windowsvoice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSpeaker struct {
	startErr error
	errors   chan error
	writes   chan PCMFrame
	mu       sync.Mutex
	started  bool
	stopped  bool
}

func newFakeSpeaker(startErr error) *fakeSpeaker {
	return &fakeSpeaker{
		startErr: startErr,
		errors:   make(chan error, 1),
		writes:   make(chan PCMFrame, 4),
	}
}

func (s *fakeSpeaker) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return s.startErr
	}
	s.started = true
	return nil
}

func (s *fakeSpeaker) Write(frame PCMFrame) error {
	s.mu.Lock()
	started := s.started && !s.stopped
	s.mu.Unlock()
	if !started {
		return ErrNotRunning
	}
	s.writes <- frame
	return nil
}

func (s *fakeSpeaker) Errors() <-chan error { return s.errors }

func (s *fakeSpeaker) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

func TestRecoveringSpeakerRetriesStartupAndReopensAfterDeviceFailure(t *testing.T) {
	first := newFakeSpeaker(ErrUnavailable)
	second := newFakeSpeaker(nil)
	third := newFakeSpeaker(nil)
	devices := []Speaker{first, second, third}
	var factoryMu sync.Mutex
	factoryCalls := 0
	factory := func() Speaker {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		device := devices[factoryCalls]
		factoryCalls++
		return device
	}
	retryCount := 0
	speaker := NewRecoveringSpeaker(factory, RetryPolicy{
		Delays: []time.Duration{time.Second, 2 * time.Second},
		Wait: func(context.Context, time.Duration) bool {
			retryCount++
			return true
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := speaker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	frame := PCMFrame{Samples: make([]int16, FrameSamples)}
	if err := speaker.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-second.writes:
	case <-time.After(2 * time.Second):
		t.Fatal("frame was not written after startup retry")
	}
	second.errors <- errors.New("device unplugged")
	if err := speaker.Write(frame); err != nil {
		t.Fatalf("Write during reopen: %v", err)
	}
	select {
	case <-third.writes:
	case <-time.After(2 * time.Second):
		t.Fatal("frame was not written after device reopen")
	}
	speaker.Stop()
	third.mu.Lock()
	thirdStopped := third.stopped
	third.mu.Unlock()
	if factoryCalls != 3 || retryCount < 2 || !thirdStopped {
		t.Fatalf("factoryCalls=%d retryCount=%d thirdStopped=%v", factoryCalls, retryCount, thirdStopped)
	}
}

func TestPCMFrameRequiresMonoTwentyMillisecondSamples(t *testing.T) {
	if err := ValidatePCMFrame(PCMFrame{Samples: make([]int16, FrameSamples)}); err != nil {
		t.Fatalf("valid frame: %v", err)
	}
	if err := ValidatePCMFrame(PCMFrame{Samples: make([]int16, FrameSamples-1)}); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("invalid frame error = %v", err)
	}
}
