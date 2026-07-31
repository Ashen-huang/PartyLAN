//go:build windows && cgo

package windowsvoice

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

const (
	deviceFrameBuffer  = 8
	playbackFrameLimit = 10
)

type malgoResources struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
}

func (r *malgoResources) close() {
	if r == nil {
		return
	}
	if r.device != nil {
		_ = r.device.Stop()
		r.device.Uninit()
	}
	if r.context != nil {
		_ = r.context.Uninit()
		r.context.Free()
	}
}

type defaultMicrophone struct {
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	resources   *malgoResources
	frames      chan PCMFrame
	errors      chan error
	pending     []int16
	stopping    atomic.Bool
}

func NewDefaultMicrophone() Microphone {
	return &defaultMicrophone{
		frames: make(chan PCMFrame, deviceFrameBuffer),
		errors: make(chan error, 4),
	}
}

func (m *defaultMicrophone) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	if m.resources != nil {
		m.mu.Unlock()
		return nil
	}
	m.pending = nil
	m.mu.Unlock()

	allocated, err := malgo.InitContext([]malgo.Backend{malgo.BackendWasapi}, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("%w: initialize WASAPI capture: %v", ErrUnavailable, err)
	}
	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = Channels
	config.SampleRate = SampleRate
	config.PeriodSizeInFrames = FrameSamples
	device, err := malgo.InitDevice(allocated.Context, config, malgo.DeviceCallbacks{
		Data: func(_ []byte, input []byte, _ uint32) { m.capture(input) },
		Stop: m.deviceStopped,
	})
	if err != nil {
		_ = allocated.Uninit()
		allocated.Free()
		return fmt.Errorf("%w: open default microphone: %v", ErrUnavailable, err)
	}
	resources := &malgoResources{context: allocated, device: device}
	m.stopping.Store(false)
	if err := device.Start(); err != nil {
		m.stopping.Store(true)
		resources.close()
		return fmt.Errorf("%w: start default microphone: %v", ErrUnavailable, err)
	}
	m.mu.Lock()
	m.resources = resources
	m.mu.Unlock()
	go func(current *malgoResources) {
		<-ctx.Done()
		m.stopIfCurrent(current)
	}(resources)
	return nil
}

func (m *defaultMicrophone) Frames() <-chan PCMFrame { return m.frames }
func (m *defaultMicrophone) Errors() <-chan error    { return m.errors }

func (m *defaultMicrophone) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked(nil)
}

func (m *defaultMicrophone) stopIfCurrent(resources *malgoResources) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked(resources)
}

func (m *defaultMicrophone) stopLocked(expected *malgoResources) {
	m.mu.Lock()
	resources := m.resources
	if resources == nil || (expected != nil && resources != expected) {
		m.mu.Unlock()
		return
	}
	m.resources = nil
	m.pending = nil
	m.stopping.Store(true)
	m.mu.Unlock()
	resources.close()
}

func (m *defaultMicrophone) capture(input []byte) {
	if len(input) < 2 {
		return
	}
	samples := make([]int16, len(input)/2)
	for index := range samples {
		samples[index] = int16(binary.LittleEndian.Uint16(input[index*2:]))
	}
	m.mu.Lock()
	m.pending = append(m.pending, samples...)
	frames := make([]PCMFrame, 0)
	for len(m.pending) >= FrameSamples {
		frames = append(frames, PCMFrame{Samples: append([]int16(nil), m.pending[:FrameSamples]...)})
		m.pending = m.pending[FrameSamples:]
	}
	m.mu.Unlock()
	for _, frame := range frames {
		select {
		case m.frames <- frame:
		default:
			<-m.frames
			m.frames <- frame
		}
	}
}

func (m *defaultMicrophone) deviceStopped() {
	if m.stopping.Load() {
		return
	}
	select {
	case m.errors <- fmt.Errorf("%w: microphone stopped", ErrUnavailable):
	default:
	}
}

type defaultSpeaker struct {
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	resources   *malgoResources
	pending     []byte
	errors      chan error
	stopping    atomic.Bool
}

func NewDefaultSpeaker() Speaker {
	return &defaultSpeaker{errors: make(chan error, 4)}
}

func (s *defaultSpeaker) Start(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if s.resources != nil {
		s.mu.Unlock()
		return nil
	}
	s.pending = nil
	s.mu.Unlock()

	allocated, err := malgo.InitContext([]malgo.Backend{malgo.BackendWasapi}, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("%w: initialize WASAPI playback: %v", ErrUnavailable, err)
	}
	config := malgo.DefaultDeviceConfig(malgo.Playback)
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = Channels
	config.SampleRate = SampleRate
	config.PeriodSizeInFrames = FrameSamples
	device, err := malgo.InitDevice(allocated.Context, config, malgo.DeviceCallbacks{
		Data: func(output []byte, _ []byte, _ uint32) { s.play(output) },
		Stop: s.deviceStopped,
	})
	if err != nil {
		_ = allocated.Uninit()
		allocated.Free()
		return fmt.Errorf("%w: open default speaker: %v", ErrUnavailable, err)
	}
	resources := &malgoResources{context: allocated, device: device}
	s.stopping.Store(false)
	if err := device.Start(); err != nil {
		s.stopping.Store(true)
		resources.close()
		return fmt.Errorf("%w: start default speaker: %v", ErrUnavailable, err)
	}
	s.mu.Lock()
	s.resources = resources
	s.mu.Unlock()
	go func(current *malgoResources) {
		<-ctx.Done()
		s.stopIfCurrent(current)
	}(resources)
	return nil
}

func (s *defaultSpeaker) Write(frame PCMFrame) error {
	if err := ValidatePCMFrame(frame); err != nil {
		return err
	}
	data := make([]byte, len(frame.Samples)*2)
	for index, sample := range frame.Samples {
		binary.LittleEndian.PutUint16(data[index*2:], uint16(sample))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resources == nil {
		return ErrNotRunning
	}
	limit := FrameSamples * 2 * playbackFrameLimit
	if overflow := len(s.pending) + len(data) - limit; overflow > 0 {
		s.pending = append([]byte(nil), s.pending[overflow:]...)
	}
	s.pending = append(s.pending, data...)
	return nil
}

func (s *defaultSpeaker) Errors() <-chan error { return s.errors }

func (s *defaultSpeaker) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopLocked(nil)
}

func (s *defaultSpeaker) stopIfCurrent(resources *malgoResources) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopLocked(resources)
}

func (s *defaultSpeaker) stopLocked(expected *malgoResources) {
	s.mu.Lock()
	resources := s.resources
	if resources == nil || (expected != nil && resources != expected) {
		s.mu.Unlock()
		return
	}
	s.resources = nil
	s.pending = nil
	s.stopping.Store(true)
	s.mu.Unlock()
	resources.close()
}

func (s *defaultSpeaker) play(output []byte) {
	clear(output)
	s.mu.Lock()
	written := copy(output, s.pending)
	if written == len(s.pending) {
		s.pending = nil
	} else if written > 0 {
		s.pending = append([]byte(nil), s.pending[written:]...)
	}
	s.mu.Unlock()
}

func (s *defaultSpeaker) deviceStopped() {
	if s.stopping.Load() {
		return
	}
	select {
	case s.errors <- fmt.Errorf("%w: speaker stopped", ErrUnavailable):
	default:
	}
}
