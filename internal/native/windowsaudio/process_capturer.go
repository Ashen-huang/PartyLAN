package windowsaudio

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultProcessCaptureSampleRate  = 48000
	defaultProcessCaptureChannels    = 2
	defaultProcessCaptureFrameMillis = 20
	defaultProcessCaptureReadTimeout = 50 * time.Millisecond
	defaultProcessCaptureFrameBuffer = 16
)

type ProcessCaptureConfig struct {
	TargetProcessID uint32
	Mode            CaptureMode
	SampleRate      uint32
	Channels        uint32
	FrameMillis     uint32
	ReadTimeout     time.Duration
}

type ProcessCapturer struct {
	bridge Bridge

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	frames chan PCMFrame
	errors chan error
}

func NewProcessCapturer() *ProcessCapturer {
	return NewProcessCapturerWithBridge(NewBridge())
}

func NewProcessCapturerWithBridge(bridge Bridge) *ProcessCapturer {
	if bridge == nil {
		bridge = NewBridge()
	}
	return &ProcessCapturer{bridge: bridge}
}

func (c *ProcessCapturer) Start(ctx context.Context, config ProcessCaptureConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	config = normalizeProcessCaptureConfig(config)
	if config.Mode != CaptureSystemMix && config.TargetProcessID == 0 {
		return fmt.Errorf("target process id is required for process audio capture")
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	frames := make(chan PCMFrame, defaultProcessCaptureFrameBuffer)
	errors := make(chan error, 1)
	ready := make(chan error, 1)

	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		cancel()
		return fmt.Errorf("process audio capture is already running")
	}
	c.cancel = cancel
	c.done = done
	c.frames = frames
	c.errors = errors
	c.mu.Unlock()

	go c.run(runCtx, config, frames, errors, ready, done)

	select {
	case err := <-ready:
		if err != nil {
			cancel()
			<-done
			return err
		}
		return nil
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	}
}

func (c *ProcessCapturer) Frames() <-chan PCMFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames
}

func (c *ProcessCapturer) Errors() <-chan error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errors
}

func (c *ProcessCapturer) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
}

func (c *ProcessCapturer) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil
}

func (c *ProcessCapturer) run(
	ctx context.Context,
	config ProcessCaptureConfig,
	frames chan<- PCMFrame,
	errors chan<- error,
	ready chan<- error,
	done chan struct{},
) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer c.clear(done)
	defer close(done)
	defer close(errors)
	defer close(frames)

	handle := c.bridge.CreateSession()
	if handle == 0 {
		ready <- c.statusError("create process audio session", StatusUnavailable)
		return
	}
	defer c.bridge.DestroySession(handle)

	status := c.bridge.ConfigureSession(handle, SessionConfig{
		TargetProcessID: config.TargetProcessID,
		Mode:            config.Mode,
		SampleRate:      config.SampleRate,
		Channels:        config.Channels,
		FrameMillis:     config.FrameMillis,
	})
	if status != StatusOK {
		ready <- c.statusError("configure process audio session", status)
		return
	}

	status = c.bridge.StartSession(handle)
	if status != StatusOK {
		ready <- c.statusError("start process audio session", status)
		return
	}
	defer c.bridge.StopSession(handle)

	ready <- nil

	timeoutMs := durationMillis(config.ReadTimeout, int(defaultProcessCaptureReadTimeout/time.Millisecond))
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, status := c.bridge.ReadFrame(handle, timeoutMs)
		if status == StatusOK {
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			default:
			}
			continue
		}

		reason := c.bridge.LastErrorReason()
		if transientAudioReadStatus(status, reason) {
			time.Sleep(5 * time.Millisecond)
			continue
		}

		err := c.statusError("read process audio frame", status)
		select {
		case errors <- err:
		default:
		}
		return
	}
}

func (c *ProcessCapturer) clear(done chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done != done {
		return
	}
	c.cancel = nil
	c.done = nil
	c.frames = nil
	c.errors = nil
}

func (c *ProcessCapturer) statusError(operation string, status Status) error {
	reason := strings.TrimSpace(c.bridge.LastErrorReason())
	if reason == "" {
		reason = string(status)
	}
	return fmt.Errorf("%s: %s", operation, reason)
}

func normalizeProcessCaptureConfig(config ProcessCaptureConfig) ProcessCaptureConfig {
	if config.SampleRate == 0 {
		config.SampleRate = defaultProcessCaptureSampleRate
	}
	if config.Channels == 0 {
		config.Channels = defaultProcessCaptureChannels
	}
	if config.FrameMillis == 0 {
		config.FrameMillis = defaultProcessCaptureFrameMillis
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = defaultProcessCaptureReadTimeout
	}
	return config
}

func durationMillis(value time.Duration, fallback int) int {
	if value <= 0 {
		return fallback
	}
	ms := int(value / time.Millisecond)
	if ms <= 0 {
		return 1
	}
	return ms
}

func transientAudioReadStatus(status Status, reason string) bool {
	if status != StatusUnavailable {
		return false
	}
	normalized := strings.ToLower(reason)
	return strings.Contains(normalized, "timeout") || strings.Contains(normalized, "buffer empty")
}
