//go:build windows && cgo

package windowsmedia

import "testing"

func TestWindowCaptureBoundsClassifiesDestroyedWindow(t *testing.T) {
	bridge := NewWindowCaptureBridge()
	_, _, status := bridge.Bounds(^uintptr(0))
	if status != WindowCaptureStatusWindowClosed {
		t.Fatalf("Bounds(invalid hwnd) status = %d, want %d", status, WindowCaptureStatusWindowClosed)
	}
}
