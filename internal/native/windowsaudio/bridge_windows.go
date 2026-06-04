//go:build windows && cgo

package windowsaudio

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -lole32 -luuid -lshell32 -lgdi32 -luser32
#include "bridge.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

type nativeBridge struct{}

func NewBridge() Bridge { return nativeBridge{} }

func (nativeBridge) QueryCapability() Capability {
	var cap C.PartyLANAudioCapability
	_ = C.PartyLAN_QueryAudioCapability(&cap)
	return Capability{
		SupportsProcessCapture: cap.supports_process_capture != 0,
		Reason:                 cString(cap.reason),
	}
}

func (nativeBridge) LastErrorReason() string {
	return cString(C.PartyLAN_GetLastAudioReason())
}

func (nativeBridge) CreateSession() uintptr {
	return uintptr(unsafe.Pointer(C.PartyLAN_CreateAudioSession()))
}

func (nativeBridge) ConfigureSession(handle uintptr, config SessionConfig) Status {
	c := C.PartyLANAudioSessionConfig{
		target_process_id: C.uint32_t(config.TargetProcessID),
		capture_mode:      C.uint32_t(config.Mode),
		sample_rate:       C.uint32_t(config.SampleRate),
		channels:          C.uint32_t(config.Channels),
		frame_ms:          C.uint32_t(config.FrameMillis),
	}
	return mapStatus(C.PartyLAN_ConfigureAudioSession(unsafe.Pointer(handle), &c))
}

func (nativeBridge) StartSession(handle uintptr) Status {
	return mapStatus(C.PartyLAN_StartAudioSession(unsafe.Pointer(handle)))
}

func (nativeBridge) ReadFrame(handle uintptr, timeoutMs int) (PCMFrame, Status) {
	var frame C.PartyLANAudioFrame
	status := mapStatus(C.PartyLAN_ReadAudioFrame(unsafe.Pointer(handle), C.int(timeoutMs), &frame))
	if status != StatusOK {
		return PCMFrame{}, status
	}
	out := PCMFrame{
		Data:            C.GoBytes(unsafe.Pointer(frame.data), C.int(frame.data_len)),
		Channels:        uint32(frame.channels),
		SampleRate:      uint32(frame.sample_rate),
		TimestampMicros: int64(frame.timestamp_us),
	}
	C.PartyLAN_ReleaseAudioFrame(unsafe.Pointer(handle), &frame)
	return out, StatusOK
}

func (nativeBridge) StopSession(handle uintptr) {
	C.PartyLAN_StopAudioSession(unsafe.Pointer(handle))
}

func (nativeBridge) DestroySession(handle uintptr) {
	C.PartyLAN_DestroyAudioSession(unsafe.Pointer(handle))
}

func ListActiveAudioSources() ([]AudioSource, error) {
	var list C.PartyLANAudioSourceList
	status := mapStatus(C.PartyLAN_ListAudioSources(&list))
	defer C.PartyLAN_FreeAudioSourceList(&list)

	if status != StatusOK {
		reason := cString(list.reason)
		if reason == "" {
			reason = "active audio session enumeration failed"
		}
		return nil, errors.New(reason)
	}

	count := int(list.count)
	if count == 0 || list.items == nil {
		return []AudioSource{}, nil
	}

	items := unsafe.Slice(list.items, count)
	out := make([]AudioSource, 0, count)
	for _, item := range items {
		out = append(out, AudioSource{
			ProcessID:   uint32(item.process_id),
			Name:        cString(item.name),
			DisplayName: cString(item.display_name),
			IconDataURL: cString(item.icon_data_url),
			Active:      item.active != 0,
		})
	}
	return out, nil
}

func mapStatus(status C.PartyLANAudioStatus) Status {
	switch status {
	case C.PARTYLAN_AUDIO_STATUS_OK:
		return StatusOK
	case C.PARTYLAN_AUDIO_STATUS_NOT_IMPLEMENTED:
		return StatusNotImplemented
	case C.PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR:
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
