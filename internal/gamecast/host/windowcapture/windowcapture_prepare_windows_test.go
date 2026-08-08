//go:build windows

package windowcapture

import (
	"errors"
	"testing"

	"PartyLAN/internal/native/windowsmedia"
)

type prepareBoundsBridge struct {
	width      int
	height     int
	status     windowsmedia.WindowCaptureStatus
	window     uintptr
	timeoutMs  int
	prepareHit int
}

func (b *prepareBoundsBridge) Create() uintptr { return 1 }

func (b *prepareBoundsBridge) Start(handle uintptr, windowHandle uintptr) windowsmedia.WindowCaptureStatus {
	return windowsmedia.WindowCaptureStatusOK
}

func (b *prepareBoundsBridge) Bounds(windowHandle uintptr) (int, int, windowsmedia.WindowCaptureStatus) {
	return b.width, b.height, b.status
}

func (b *prepareBoundsBridge) PrepareBounds(windowHandle uintptr, timeoutMs int) (int, int, windowsmedia.WindowCaptureStatus) {
	b.window = windowHandle
	b.timeoutMs = timeoutMs
	b.prepareHit++
	return b.width, b.height, b.status
}

func (b *prepareBoundsBridge) GrabFrame(handle uintptr, timeoutMs int) (windowsmedia.WindowCaptureFrame, windowsmedia.WindowCaptureStatus) {
	return windowsmedia.WindowCaptureFrame{}, windowsmedia.WindowCaptureStatusUnavailable
}

func (b *prepareBoundsBridge) Stop(handle uintptr)    {}
func (b *prepareBoundsBridge) Destroy(handle uintptr) {}

func (b *prepareBoundsBridge) LastErrorReason(handle uintptr) string {
	return "test bridge"
}

func TestPrepareForShareUsesPreparedBounds(t *testing.T) {
	bridge := &prepareBoundsBridge{
		width:  1280,
		height: 720,
		status: windowsmedia.WindowCaptureStatusOK,
	}
	capturer := newWithBridge(bridge, 42, 60)

	bounds, err := capturer.PrepareForShare()
	if err != nil {
		t.Fatalf("PrepareForShare() error = %v", err)
	}
	if got := bounds.Dx(); got != 1280 {
		t.Fatalf("prepared width = %d, want 1280", got)
	}
	if got := bounds.Dy(); got != 720 {
		t.Fatalf("prepared height = %d, want 720", got)
	}
	if bridge.prepareHit != 1 || bridge.window != 42 || bridge.timeoutMs != prepareForShareTimeout {
		t.Fatalf("PrepareBounds call = count %d, hwnd %d, timeout %d; want 1, 42, %d", bridge.prepareHit, bridge.window, bridge.timeoutMs, prepareForShareTimeout)
	}
}

func TestPrepareForShareMapsRestoreFailure(t *testing.T) {
	bridge := &prepareBoundsBridge{status: windowsmedia.WindowCaptureStatusUnavailable}
	capturer := newWithBridge(bridge, 42, 60)

	_, err := capturer.PrepareForShare()
	if err == nil || err.Error() != "所选窗口无法恢复，请手动还原后重试" {
		t.Fatalf("PrepareForShare() error = %v, want restore guidance", err)
	}

	bridge.status = windowsmedia.WindowCaptureStatusWindowClosed
	_, err = capturer.PrepareForShare()
	if !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("closed window error = %v, want ErrWindowClosed", err)
	}
}
