package room

import (
	"testing"

	"PartyLAN/internal/video"
)

func TestShouldEnableNativeEncoder(t *testing.T) {
	t.Run("disabled when not requested", func(t *testing.T) {
		if shouldEnableNativeEncoder(false, video.BackendCapability{Backend: video.BackendNative, SupportsView: true}) {
			t.Fatal("native encoder should stay disabled when not requested")
		}
	})

	t.Run("disabled when viewer decode is unavailable", func(t *testing.T) {
		if shouldEnableNativeEncoder(true, video.BackendCapability{Backend: video.BackendNative, SupportsView: false}) {
			t.Fatal("native encoder should be disabled when viewer decode is unavailable")
		}
	})

	t.Run("disabled for software viewer backend", func(t *testing.T) {
		if shouldEnableNativeEncoder(true, video.BackendCapability{Backend: video.BackendSoftware, SupportsView: true}) {
			t.Fatal("native encoder should be disabled for software viewer backend")
		}
	})

	t.Run("enabled when requested and native viewer decode is available", func(t *testing.T) {
		if !shouldEnableNativeEncoder(true, video.BackendCapability{Backend: video.BackendNative, SupportsView: true}) {
			t.Fatal("native encoder should be enabled when viewer decode is available")
		}
	})
}

func TestFrameForReuseUpdatesTimestampWithoutCopyingPixels(t *testing.T) {
	pixels := []byte{1, 2, 3, 4}
	frame := &video.Frame{
		Data:      pixels,
		Timestamp: 100,
		Width:     1,
		Height:    1,
	}

	reused := frameForReuse(frame, 200)
	if reused == nil {
		t.Fatal("expected reusable frame")
	}
	if reused.Timestamp != 200 {
		t.Fatalf("expected timestamp 200, got %d", reused.Timestamp)
	}
	if reused.Width != frame.Width || reused.Height != frame.Height {
		t.Fatalf("unexpected dimensions: %dx%d", reused.Width, reused.Height)
	}
	if &reused.Data[0] != &pixels[0] {
		t.Fatal("expected reused frame to reference the same pixel buffer")
	}
	if frame.Timestamp != 100 {
		t.Fatalf("original timestamp changed: %d", frame.Timestamp)
	}
}

func TestRecordSenderStatsTracksReuseTimeoutAndDrop(t *testing.T) {
	sender := &StreamSender{}

	sender.recordSenderStats(0, 12, 0, 0, true, false, true)
	sender.recordSenderStats(100, 8, 0, 2, true, true, false)

	if sender.statsWindow.frames != 1 {
		t.Fatalf("expected one sent frame, got %d", sender.statsWindow.frames)
	}
	if sender.statsWindow.bytes != 100 {
		t.Fatalf("expected 100 bytes, got %d", sender.statsWindow.bytes)
	}
	if sender.statsWindow.captureSamples != 2 {
		t.Fatalf("expected two capture samples, got %d", sender.statsWindow.captureSamples)
	}
	if sender.statsWindow.encodeSamples != 1 {
		t.Fatalf("expected one encode sample, got %d", sender.statsWindow.encodeSamples)
	}
	if sender.statsWindow.captureTimeouts != 2 {
		t.Fatalf("expected two capture timeouts, got %d", sender.statsWindow.captureTimeouts)
	}
	if sender.statsWindow.reusedFrames != 1 {
		t.Fatalf("expected one reused frame, got %d", sender.statsWindow.reusedFrames)
	}
	if sender.statsWindow.droppedFrames != 1 {
		t.Fatalf("expected one dropped frame, got %d", sender.statsWindow.droppedFrames)
	}
}
