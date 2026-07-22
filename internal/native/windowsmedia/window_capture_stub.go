//go:build !windows || !cgo

package windowsmedia

type unavailableWindowCaptureBridge struct{}

func NewWindowCaptureBridge() WindowCaptureBridge {
	return unavailableWindowCaptureBridge{}
}

func (unavailableWindowCaptureBridge) Create() uintptr { return 0 }

func (unavailableWindowCaptureBridge) Start(handle uintptr, windowHandle uintptr) WindowCaptureStatus {
	return WindowCaptureStatusUnavailable
}

func (unavailableWindowCaptureBridge) Bounds(windowHandle uintptr) (int, int, WindowCaptureStatus) {
	return 0, 0, WindowCaptureStatusUnavailable
}

func (unavailableWindowCaptureBridge) GrabFrame(handle uintptr, timeoutMs int) (WindowCaptureFrame, WindowCaptureStatus) {
	return WindowCaptureFrame{}, WindowCaptureStatusUnavailable
}

func (unavailableWindowCaptureBridge) Stop(handle uintptr)    {}
func (unavailableWindowCaptureBridge) Destroy(handle uintptr) {}

func (unavailableWindowCaptureBridge) LastErrorReason(handle uintptr) string {
	return "Windows Graphics Capture requires Windows and cgo"
}
