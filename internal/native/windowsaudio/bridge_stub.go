//go:build !windows || !cgo

package windowsaudio

import "fmt"

type stubBridge struct{}

func NewBridge() Bridge {
	return stubBridge{}
}

func (stubBridge) QueryCapability() Capability {
	return Capability{
		SupportsProcessCapture: false,
		Reason:                 "audio bridge unavailable in this build",
	}
}

func (stubBridge) LastErrorReason() string {
	return "audio bridge unavailable in this build"
}

func (stubBridge) CreateSession() uintptr { return 0 }

func (stubBridge) ConfigureSession(handle uintptr, config SessionConfig) Status {
	_ = handle
	_ = config
	return StatusUnavailable
}

func (stubBridge) StartSession(handle uintptr) Status {
	_ = handle
	return StatusUnavailable
}

func (stubBridge) ReadFrame(handle uintptr, timeoutMs int) (PCMFrame, Status) {
	_ = handle
	_ = timeoutMs
	return PCMFrame{}, StatusUnavailable
}

func (stubBridge) StopSession(handle uintptr) {
	_ = handle
}

func (stubBridge) DestroySession(handle uintptr) {
	_ = handle
}

func ListActiveAudioSources() ([]AudioSource, error) {
	return nil, fmt.Errorf("audio session enumeration unavailable in this build")
}
