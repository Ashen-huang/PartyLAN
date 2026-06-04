package audio

import (
	"math"
	"sync"
	"time"
)

const (
	MonoChannels = 1
)

type MockCapturer struct {
	mu        sync.Mutex
	running   bool
	source    AudioSource
	startTime time.Time
}

func NewMockCapturer() *MockCapturer {
	return &MockCapturer{}
}

func (c *MockCapturer) Start(source AudioSource, deviceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	c.source = source
	c.running = true
	c.startTime = time.Now()
	return nil
}

func (c *MockCapturer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	return nil
}

func (c *MockCapturer) GetFrame() (*AudioFrame, error) {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()

	if !running {
		return nil, ErrCapturerNotRunning
	}

	samplesPerFrame := SampleRate * FrameSizeMs / 1000
	samples := make([]float32, samplesPerFrame)

	elapsed := time.Since(c.startTime)
	timestamp := elapsed.Microseconds()

	return &AudioFrame{
		Samples:    samples,
		Timestamp:  timestamp,
		Channels:   MonoChannels,
		SampleRate: SampleRate,
	}, nil
}

var ErrCapturerNotRunning = &AudioError{"capturer not running"}

type AudioError struct {
	msg string
}

func (e *AudioError) Error() string {
	return e.msg
}

// SineCapturer generates a sine wave for testing
type SineCapturer struct {
	mu        sync.Mutex
	running   bool
	freq      float64
	phase     float64
	startTime time.Time
}

func NewSineCapturer(freq float64) *SineCapturer {
	return &SineCapturer{freq: freq}
}

func (c *SineCapturer) Start(source AudioSource, deviceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	c.running = true
	c.phase = 0
	c.startTime = time.Now()
	return nil
}

func (c *SineCapturer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	return nil
}

func (c *SineCapturer) GetFrame() (*AudioFrame, error) {
	c.mu.Lock()
	running := c.running
	if !running {
		c.mu.Unlock()
		return nil, ErrCapturerNotRunning
	}

	samplesPerFrame := SampleRate * FrameSizeMs / 1000
	samples := make([]float32, samplesPerFrame)

	phaseInc := 2 * math.Pi * c.freq / float64(SampleRate)
	for i := range samples {
		samples[i] = float32(math.Sin(c.phase) * 0.3)
		c.phase += phaseInc
	}
	if c.phase > 2*math.Pi {
		c.phase -= 2 * math.Pi
	}

	elapsed := time.Since(c.startTime)
	timestamp := elapsed.Microseconds()
	c.mu.Unlock()

	return &AudioFrame{
		Samples:    samples,
		Timestamp:  timestamp,
		Channels:   MonoChannels,
		SampleRate: SampleRate,
	}, nil
}
