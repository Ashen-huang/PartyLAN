package windowsaudio

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAudioBridge struct {
	mu         sync.Mutex
	reason     string
	frames     []PCMFrame
	configured SessionConfig
	started    bool
	stopped    bool
	destroyed  bool
}

func (b *fakeAudioBridge) QueryCapability() Capability {
	return Capability{SupportsProcessCapture: true, Reason: "fake ready"}
}

func (b *fakeAudioBridge) LastErrorReason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reason
}

func (b *fakeAudioBridge) CreateSession() uintptr {
	return 1
}

func (b *fakeAudioBridge) ConfigureSession(handle uintptr, config SessionConfig) Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	if handle == 0 || config.TargetProcessID == 0 {
		b.reason = "target process id is required"
		return StatusInternalError
	}
	b.configured = config
	b.reason = "configured"
	return StatusOK
}

func (b *fakeAudioBridge) StartSession(handle uintptr) Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	if handle == 0 {
		b.reason = "missing handle"
		return StatusInternalError
	}
	b.started = true
	b.reason = "started"
	return StatusOK
}

func (b *fakeAudioBridge) ReadFrame(handle uintptr, timeoutMs int) (PCMFrame, Status) {
	b.mu.Lock()
	if len(b.frames) > 0 {
		frame := b.frames[0]
		b.frames = b.frames[1:]
		b.reason = "frame captured"
		b.mu.Unlock()
		return frame, StatusOK
	}
	b.reason = "audio capture timeout"
	b.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	return PCMFrame{}, StatusUnavailable
}

func (b *fakeAudioBridge) StopSession(handle uintptr) {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
}

func (b *fakeAudioBridge) DestroySession(handle uintptr) {
	b.mu.Lock()
	b.destroyed = true
	b.mu.Unlock()
}

func TestProcessCapturerStreamsFramesOnWorkerThread(t *testing.T) {
	bridge := &fakeAudioBridge{
		frames: []PCMFrame{
			{
				Data:            []byte{1, 2, 3, 4},
				Channels:        2,
				SampleRate:      48000,
				TimestampMicros: 123,
			},
		},
	}
	capturer := NewProcessCapturerWithBridge(bridge)

	if err := capturer.Start(context.Background(), ProcessCaptureConfig{TargetProcessID: 42}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !capturer.Running() {
		t.Fatal("expected capturer to be running")
	}

	select {
	case frame := <-capturer.Frames():
		if got, want := len(frame.Data), 4; got != want {
			t.Fatalf("frame data length = %d, want %d", got, want)
		}
		if frame.Channels != 2 || frame.SampleRate != 48000 {
			t.Fatalf("unexpected frame format: channels=%d sampleRate=%d", frame.Channels, frame.SampleRate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for captured frame")
	}

	capturer.Stop()
	if capturer.Running() {
		t.Fatal("expected capturer to stop")
	}

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if !bridge.started || !bridge.stopped || !bridge.destroyed {
		t.Fatalf("expected bridge lifecycle start/stop/destroy, got started=%t stopped=%t destroyed=%t", bridge.started, bridge.stopped, bridge.destroyed)
	}
	if bridge.configured.TargetProcessID != 42 {
		t.Fatalf("configured target process id = %d, want 42", bridge.configured.TargetProcessID)
	}
}

func TestProcessCapturerRejectsMissingTargetPID(t *testing.T) {
	capturer := NewProcessCapturerWithBridge(&fakeAudioBridge{})
	err := capturer.Start(context.Background(), ProcessCaptureConfig{})
	if err == nil {
		t.Fatal("expected missing target PID error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "target process id") {
		t.Fatalf("expected target PID error, got %v", err)
	}
}
