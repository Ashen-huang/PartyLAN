//go:build !windows || !cgo

package windowsmedia

type stubBridge struct{}

func NewBridge() Bridge {
	return stubBridge{}
}

func (stubBridge) QueryCapability() Capability {
	return Capability{
		Status:       StatusUnavailable,
		AdapterName:  "",
		DriverName:   "",
		Reason:       "native bridge unavailable in this build",
		SupportsHost: false,
		SupportsView: false,
	}
}

func (stubBridge) LastErrorReason() string {
	return "native bridge unavailable in this build"
}

func (stubBridge) CreateCapturer() uintptr { return 0 }
func (stubBridge) CreateEncoder() uintptr  { return 0 }
func (stubBridge) CreateDecoder() uintptr  { return 0 }
func (stubBridge) CapturerStart(handle uintptr, displayIndex int) Status {
	_ = handle
	_ = displayIndex
	return StatusUnavailable
}
func (stubBridge) CapturerGrabFrame(handle uintptr, timeoutMs int) (CapturedFrame, Status) {
	_ = handle
	_ = timeoutMs
	return CapturedFrame{}, StatusUnavailable
}
func (stubBridge) CapturerStop(handle uintptr) {
	_ = handle
}
func (stubBridge) EncoderConfigure(handle uintptr, config EncoderConfig) Status {
	_ = handle
	_ = config
	return StatusUnavailable
}
func (stubBridge) EncoderEncodeRGBA(handle uintptr, rgba []byte, timestampMicros int64) (EncodedPacket, Status) {
	_ = handle
	_ = rgba
	_ = timestampMicros
	return EncodedPacket{}, StatusUnavailable
}
func (stubBridge) EncoderEncodeFromCapturer(encoderHandle uintptr, capturerHandle uintptr, timeoutMs int) (EncodedPacket, Status) {
	_ = encoderHandle
	_ = capturerHandle
	_ = timeoutMs
	return EncodedPacket{}, StatusUnavailable
}
func (stubBridge) EncoderRequestKeyFrame(handle uintptr) Status {
	_ = handle
	return StatusUnavailable
}
func (stubBridge) EncoderClose(handle uintptr) {
	_ = handle
}
func (stubBridge) DecoderConfigure(handle uintptr, config DecoderConfig) Status {
	_ = handle
	_ = config
	return StatusUnavailable
}
func (stubBridge) DecoderDecodeAnnexB(handle uintptr, data []byte, timestampMicros int64) (CapturedFrame, Status) {
	_ = handle
	_ = data
	_ = timestampMicros
	return CapturedFrame{}, StatusUnavailable
}
func (stubBridge) DecoderReleaseFrame(handle uintptr, frame *CapturedFrame) {
	_ = handle
	_ = frame
}
func (stubBridge) DecoderClose(handle uintptr) {
	_ = handle
}
func (stubBridge) DestroyCapturer(handle uintptr) {
	_ = handle
}
func (stubBridge) DestroyEncoder(handle uintptr) {
	_ = handle
}
func (stubBridge) DestroyDecoder(handle uintptr) {
	_ = handle
}
