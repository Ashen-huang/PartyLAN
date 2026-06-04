package video

import "testing"

func TestOpenNativeBridgeReturnsConsistentCapability(t *testing.T) {
	bridge := OpenNativeBridge()
	capability := bridge.QueryCapability()

	if capability.Status == NativeBridgeStatusOK && !capability.SupportsHost {
		t.Fatalf("expected host support when native bridge status is OK")
	}
	if capability.Status == NativeBridgeStatusUnavailable && capability.SupportsHost {
		t.Fatalf("unexpected host support when native bridge is unavailable")
	}
}

func TestPreferredBackendsMatchDetectedCapability(t *testing.T) {
	capability := OpenNativeBridge().QueryCapability()
	host := NewPreferredHostBackend()
	if capability.SupportsHost {
		if host.Name() != "native-runtime" {
			t.Fatalf("expected native-runtime host backend, got %q", host.Name())
		}
	} else if host.Name() != "software-backend" {
		t.Fatalf("expected software-backend host backend, got %q", host.Name())
	}

	viewer := NewPreferredViewerBackend()
	if capability.SupportsView {
		if viewer.Name() != "native-runtime" {
			t.Fatalf("expected native-runtime viewer backend, got %q", viewer.Name())
		}
	} else if viewer.Name() != "software-backend" {
		t.Fatalf("expected software-backend viewer backend, got %q", viewer.Name())
	}
}
