//go:build windows && cgo

package windowsmedia

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -ld3d11 -ldxgi -ldxguid -lmfplat -lmfuuid -lwmcodecdspuuid -lole32
#include "bridge.h"
*/
import "C"

import "unsafe"

type nativeBridge struct{}

func NewBridge() Bridge {
	return nativeBridge{}
}

func (nativeBridge) QueryCapability() Capability {
	var cap C.PartyLANMediaCapability
	status := C.PartyLAN_QueryMediaCapability(&cap)

	return Capability{
		Status:       mapStatus(status),
		AdapterName:  cString(cap.adapter_name),
		DriverName:   cString(cap.driver_name),
		Reason:       cString(cap.reason),
		SupportsHost: cap.supports_host != 0,
		SupportsView: cap.supports_view != 0,
	}
}

func (nativeBridge) LastErrorReason() string {
	return cString(C.PartyLAN_GetLastMediaReason())
}

func (nativeBridge) CreateCapturer() uintptr {
	return uintptr(unsafe.Pointer(C.PartyLAN_CreateCapturer()))
}

func (nativeBridge) CreateEncoder() uintptr {
	return uintptr(unsafe.Pointer(C.PartyLAN_CreateEncoder()))
}

func (nativeBridge) CreateDecoder() uintptr {
	return uintptr(unsafe.Pointer(C.PartyLAN_CreateDecoder()))
}

func (nativeBridge) CapturerStart(handle uintptr, displayIndex int) Status {
	status := C.PartyLAN_CapturerStart(unsafe.Pointer(handle), C.int(displayIndex))
	return mapStatus(status)
}

func (nativeBridge) CapturerGrabFrame(handle uintptr, timeoutMs int) (CapturedFrame, Status) {
	var frame C.PartyLANMediaFrame
	status := C.PartyLAN_CapturerGrabFrame(unsafe.Pointer(handle), C.int(timeoutMs), &frame)
	goStatus := mapStatus(status)
	if goStatus != StatusOK {
		return CapturedFrame{}, goStatus
	}

	data := C.GoBytes(unsafe.Pointer(frame.data), C.int(frame.data_len))
	out := CapturedFrame{
		Data:            data,
		Width:           int(frame.width),
		Height:          int(frame.height),
		Stride:          int(frame.stride),
		TimestampMicros: int64(frame.timestamp_us),
	}

	C.PartyLAN_CapturerReleaseFrame(unsafe.Pointer(handle), &frame)
	return out, StatusOK
}

func (nativeBridge) CapturerStop(handle uintptr) {
	C.PartyLAN_CapturerStop(unsafe.Pointer(handle))
}

func (nativeBridge) EncoderConfigure(handle uintptr, config EncoderConfig) Status {
	c := C.PartyLANMediaEncoderConfig{
		width:           C.uint32_t(config.Width),
		height:          C.uint32_t(config.Height),
		fps:             C.uint32_t(config.FPS),
		bitrate_kbps:    C.uint32_t(config.BitrateKbps),
		pixel_format:    C.uint32_t(config.PixelFormat),
		capturer_handle: C.uint64_t(config.CapturerHandle),
	}
	status := C.PartyLAN_EncoderConfigure(unsafe.Pointer(handle), &c)
	return mapStatus(status)
}

func (nativeBridge) EncoderEncodeRGBA(handle uintptr, rgba []byte, timestampMicros int64) (EncodedPacket, Status) {
	if len(rgba) == 0 {
		return EncodedPacket{}, StatusInternalError
	}

	var packet C.PartyLANMediaPacket
	status := C.PartyLAN_EncoderEncodeRGBA(
		unsafe.Pointer(handle),
		(*C.uint8_t)(unsafe.Pointer(&rgba[0])),
		C.uint32_t(len(rgba)),
		C.int64_t(timestampMicros),
		&packet,
	)
	goStatus := mapStatus(status)
	if goStatus != StatusOK {
		return EncodedPacket{}, goStatus
	}

	data := C.GoBytes(unsafe.Pointer(packet.data), C.int(packet.data_len))
	out := EncodedPacket{
		Data:            data,
		TimestampMicros: int64(packet.timestamp_us),
	}
	C.PartyLAN_EncoderReleasePacket(unsafe.Pointer(handle), &packet)
	return out, StatusOK
}

func (nativeBridge) EncoderEncodeFromCapturer(encoderHandle uintptr, capturerHandle uintptr, timeoutMs int) (EncodedPacket, Status) {
	var packet C.PartyLANMediaPacket
	status := C.PartyLAN_EncoderEncodeFromCapturer(
		unsafe.Pointer(encoderHandle),
		unsafe.Pointer(capturerHandle),
		C.int(timeoutMs),
		&packet,
	)
	goStatus := mapStatus(status)
	if goStatus != StatusOK {
		return EncodedPacket{}, goStatus
	}

	data := C.GoBytes(unsafe.Pointer(packet.data), C.int(packet.data_len))
	out := EncodedPacket{
		Data:            data,
		TimestampMicros: int64(packet.timestamp_us),
	}
	C.PartyLAN_EncoderReleasePacket(unsafe.Pointer(encoderHandle), &packet)
	return out, StatusOK
}

func (nativeBridge) EncoderRequestKeyFrame(handle uintptr) Status {
	status := C.PartyLAN_EncoderRequestKeyFrame(unsafe.Pointer(handle))
	return mapStatus(status)
}

func (nativeBridge) EncoderClose(handle uintptr) {
	C.PartyLAN_EncoderClose(unsafe.Pointer(handle))
}

func (nativeBridge) DecoderConfigure(handle uintptr, config DecoderConfig) Status {
	c := C.PartyLANMediaDecoderConfig{
		width:  C.uint32_t(config.Width),
		height: C.uint32_t(config.Height),
	}
	status := C.PartyLAN_DecoderConfigure(unsafe.Pointer(handle), &c)
	return mapStatus(status)
}

func (nativeBridge) DecoderDecodeAnnexB(handle uintptr, data []byte, timestampMicros int64) (CapturedFrame, Status) {
	if len(data) == 0 {
		return CapturedFrame{}, StatusInternalError
	}

	var frame C.PartyLANMediaFrame
	status := C.PartyLAN_DecoderDecodeAnnexB(
		unsafe.Pointer(handle),
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.uint32_t(len(data)),
		C.int64_t(timestampMicros),
		&frame,
	)
	goStatus := mapStatus(status)
	if goStatus != StatusOK {
		return CapturedFrame{}, goStatus
	}

	out := CapturedFrame{
		Data:            C.GoBytes(unsafe.Pointer(frame.data), C.int(frame.data_len)),
		Width:           int(frame.width),
		Height:          int(frame.height),
		Stride:          int(frame.stride),
		TimestampMicros: int64(frame.timestamp_us),
	}
	C.PartyLAN_DecoderReleaseFrame(unsafe.Pointer(handle), &frame)
	return out, StatusOK
}

func (nativeBridge) DecoderReleaseFrame(handle uintptr, frame *CapturedFrame) {
	_ = handle
	_ = frame
}

func (nativeBridge) DecoderClose(handle uintptr) {
	C.PartyLAN_DecoderClose(unsafe.Pointer(handle))
}

func (nativeBridge) DestroyCapturer(handle uintptr) {
	C.PartyLAN_DestroyCapturer(unsafe.Pointer(handle))
}

func (nativeBridge) DestroyEncoder(handle uintptr) {
	C.PartyLAN_DestroyEncoder(unsafe.Pointer(handle))
}

func (nativeBridge) DestroyDecoder(handle uintptr) {
	C.PartyLAN_DestroyDecoder(unsafe.Pointer(handle))
}

func mapStatus(status C.PartyLANMediaStatus) Status {
	switch status {
	case C.PARTYLAN_MEDIA_STATUS_OK:
		return StatusOK
	case C.PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU:
		return StatusUnsupportedGPU
	case C.PARTYLAN_MEDIA_STATUS_NOT_IMPLEMENTED:
		return StatusNotImplemented
	case C.PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR:
		return StatusInternalError
	default:
		return StatusUnavailable
	}
}

func cString(ptr *C.char) string {
	if ptr == nil {
		return ""
	}
	return C.GoString(ptr)
}
