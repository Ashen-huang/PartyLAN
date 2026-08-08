//go:build windows && cgo

package windowsmedia

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -ld3d11 -ldxgi -ldxguid -lwindowsapp -lruntimeobject -lole32 -luuid
#include "bridge.h"
*/
import "C"

import "unsafe"

type nativeWindowCaptureBridge struct{}

func NewWindowCaptureBridge() WindowCaptureBridge {
	return nativeWindowCaptureBridge{}
}

func (nativeWindowCaptureBridge) Create() uintptr {
	return uintptr(unsafe.Pointer(C.PartyLAN_CreateWindowCapturer()))
}

func (nativeWindowCaptureBridge) Start(handle uintptr, windowHandle uintptr) WindowCaptureStatus {
	return mapWindowCaptureStatus(C.PartyLAN_WindowCapturerStart(
		unsafe.Pointer(handle),
		C.uint64_t(windowHandle),
	))
}

func (nativeWindowCaptureBridge) Bounds(windowHandle uintptr) (int, int, WindowCaptureStatus) {
	var width C.uint32_t
	var height C.uint32_t
	status := mapWindowCaptureStatus(C.PartyLAN_WindowCapturerBounds(
		C.uint64_t(windowHandle),
		&width,
		&height,
	))
	return int(width), int(height), status
}

func (nativeWindowCaptureBridge) PrepareBounds(windowHandle uintptr, timeoutMs int) (int, int, WindowCaptureStatus) {
	var width C.uint32_t
	var height C.uint32_t
	status := mapWindowCaptureStatus(C.PartyLAN_WindowCapturerPrepareBounds(
		C.uint64_t(windowHandle),
		C.uint32_t(timeoutMs),
		&width,
		&height,
	))
	return int(width), int(height), status
}

func (nativeWindowCaptureBridge) GrabFrame(handle uintptr, timeoutMs int) (WindowCaptureFrame, WindowCaptureStatus) {
	var frame C.PartyLANWindowCaptureFrame
	status := mapWindowCaptureStatus(C.PartyLAN_WindowCapturerGrabFrame(
		unsafe.Pointer(handle),
		C.int(timeoutMs),
		&frame,
	))
	if status != WindowCaptureStatusOK {
		return WindowCaptureFrame{}, status
	}

	out := WindowCaptureFrame{
		Data:            C.GoBytes(unsafe.Pointer(frame.data), C.int(frame.data_len)),
		Width:           int(frame.width),
		Height:          int(frame.height),
		Stride:          int(frame.stride),
		TimestampMicros: int64(frame.timestamp_us),
	}
	C.PartyLAN_WindowCapturerReleaseFrame(unsafe.Pointer(handle), &frame)
	return out, WindowCaptureStatusOK
}

func (nativeWindowCaptureBridge) Stop(handle uintptr) {
	C.PartyLAN_WindowCapturerStop(unsafe.Pointer(handle))
}

func (nativeWindowCaptureBridge) Destroy(handle uintptr) {
	C.PartyLAN_DestroyWindowCapturer(unsafe.Pointer(handle))
}

func (nativeWindowCaptureBridge) LastErrorReason(handle uintptr) string {
	return cString(C.PartyLAN_GetWindowCaptureReason(unsafe.Pointer(handle)))
}

func mapWindowCaptureStatus(status C.PartyLANWindowCaptureStatus) WindowCaptureStatus {
	switch status {
	case C.PARTYLAN_WINDOW_CAPTURE_STATUS_OK:
		return WindowCaptureStatusOK
	case C.PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED:
		return WindowCaptureStatusWindowClosed
	case C.PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE:
		return WindowCaptureStatusFrameUnavailable
	case C.PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR:
		return WindowCaptureStatusInternalError
	default:
		return WindowCaptureStatusUnavailable
	}
}
