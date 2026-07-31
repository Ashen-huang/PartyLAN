package windowsvoice

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	SampleRate   = 48000
	Channels     = 1
	FrameMillis  = 20
	FrameSamples = SampleRate * FrameMillis / 1000
)

var (
	ErrUnavailable  = errors.New("voice device unavailable")
	ErrNotRunning   = errors.New("voice device is not running")
	ErrInvalidFrame = errors.New("voice PCM frame is invalid")
)

type PCMFrame struct {
	Samples []int16
}

type Microphone interface {
	Start(context.Context) error
	Frames() <-chan PCMFrame
	Errors() <-chan error
	Stop()
}

type Speaker interface {
	Start(context.Context) error
	Write(PCMFrame) error
	Errors() <-chan error
	Stop()
}

type SpeakerFactory func() Speaker

type RetryPolicy struct {
	Delays []time.Duration
	Wait   func(context.Context, time.Duration) bool
}

type RecoveringSpeaker struct {
	mu      sync.Mutex
	factory SpeakerFactory
	policy  RetryPolicy
	frames  chan PCMFrame
	errors  chan error
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewRecoveringSpeaker(factory SpeakerFactory, policy RetryPolicy) *RecoveringSpeaker {
	if len(policy.Delays) == 0 {
		policy.Delays = []time.Duration{
			time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			15 * time.Second,
		}
	}
	if policy.Wait == nil {
		policy.Wait = waitRetryDelay
	}
	return &RecoveringSpeaker{
		factory: factory,
		policy:  policy,
		frames:  make(chan PCMFrame, 5),
		errors:  make(chan error, 8),
	}
}

func (s *RecoveringSpeaker) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	if s.factory == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithCancel(parent)
	s.running = true
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
	return nil
}

func (s *RecoveringSpeaker) Write(frame PCMFrame) error {
	if err := ValidatePCMFrame(frame); err != nil {
		return err
	}
	frame.Samples = append([]int16(nil), frame.Samples...)
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return ErrNotRunning
	}
	select {
	case s.frames <- frame:
	default:
		<-s.frames
		s.frames <- frame
	}
	return nil
}

func (s *RecoveringSpeaker) Errors() <-chan error { return s.errors }

func (s *RecoveringSpeaker) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	cancel()
	<-done
}

func (s *RecoveringSpeaker) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		device := s.factory()
		if device == nil {
			s.emitError(ErrUnavailable)
		} else if err := device.Start(ctx); err == nil {
			attempt = 0
			err = s.serveDevice(ctx, device)
			device.Stop()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				s.emitError(err)
			}
		} else {
			device.Stop()
			s.emitError(err)
		}
		delay := s.policy.Delays[min(attempt, len(s.policy.Delays)-1)]
		attempt++
		if !s.policy.Wait(ctx, delay) {
			return
		}
	}
}

func (s *RecoveringSpeaker) serveDevice(ctx context.Context, device Speaker) error {
	for {
		select {
		case err := <-device.Errors():
			if err == nil {
				return ErrUnavailable
			}
			return err
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-device.Errors():
			if err == nil {
				return ErrUnavailable
			}
			return err
		case frame := <-s.frames:
			if err := device.Write(frame); err != nil {
				return err
			}
		}
	}
}

func (s *RecoveringSpeaker) emitError(err error) {
	if err == nil {
		return
	}
	select {
	case s.errors <- err:
	default:
	}
}

func waitRetryDelay(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func ValidatePCMFrame(frame PCMFrame) error {
	if len(frame.Samples) != FrameSamples {
		return ErrInvalidFrame
	}
	return nil
}

func NewRecoveringDefaultSpeaker() Speaker {
	return NewRecoveringSpeaker(NewDefaultSpeaker, RetryPolicy{})
}
