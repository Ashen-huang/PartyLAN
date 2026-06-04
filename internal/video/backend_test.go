package video

import (
	"errors"
	"testing"
)

func TestNativeBackendStubReportsUnavailableCapability(t *testing.T) {
	backend := NewNativeBackendStub()
	capability := backend.Capability()

	if capability.Backend != BackendNative {
		t.Fatalf("expected native backend, got %q", capability.Backend)
	}
	if capability.Status != CapabilityUnavailable {
		t.Fatalf("expected unavailable status, got %q", capability.Status)
	}
	if capability.SupportsHost {
		t.Fatal("expected host support to be false")
	}
}

func TestUnavailableImplementationsReturnConfiguredError(t *testing.T) {
	expected := ErrNativeBackendUnavailable

	capturer := NewUnavailableCapturer(expected)
	if err := capturer.Start(CaptureConfig{}); !errors.Is(err, expected) {
		t.Fatalf("capturer returned %v, want %v", err, expected)
	}

	encoder := NewUnavailableEncoder(expected)
	if err := encoder.Configure(EncoderConfig{}); !errors.Is(err, expected) {
		t.Fatalf("encoder returned %v, want %v", err, expected)
	}

	decoder := NewUnavailableDecoder(expected)
	if _, err := decoder.Decode(nil); !errors.Is(err, expected) {
		t.Fatalf("decoder returned %v, want %v", err, expected)
	}
}
