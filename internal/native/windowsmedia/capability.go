package windowsmedia

type Status int

const (
	StatusOK Status = iota
	StatusUnavailable
	StatusUnsupportedGPU
	StatusNotImplemented
	StatusInternalError
	StatusRetryable
)

type Capability struct {
	Status       Status
	AdapterName  string
	DriverName   string
	Reason       string
	SupportsHost bool
	SupportsView bool
}

type CapturedFrame struct {
	Data            []byte
	Width           int
	Height          int
	Stride          int
	TimestampMicros int64
}

type PixelFormat int

const (
	PixelFormatRGBA PixelFormat = iota
	PixelFormatBGRA
)

type EncoderConfig struct {
	Width             int
	Height            int
	FPS               int
	BitrateKbps       int
	PixelFormat       PixelFormat
	CapturerHandle    uintptr
	CompatibilityMode bool
}

type DecoderConfig struct {
	Width  int
	Height int
}

type EncodedPacket struct {
	Data            []byte
	TimestampMicros int64
}

type WindowCaptureStatus int

const (
	WindowCaptureStatusOK WindowCaptureStatus = iota
	WindowCaptureStatusUnavailable
	WindowCaptureStatusWindowClosed
	WindowCaptureStatusFrameUnavailable
	WindowCaptureStatusInternalError
)

type WindowCaptureFrame struct {
	Data            []byte
	Width           int
	Height          int
	Stride          int
	TimestampMicros int64
}

type WindowCaptureBridge interface {
	Create() uintptr
	Start(handle uintptr, windowHandle uintptr) WindowCaptureStatus
	Bounds(windowHandle uintptr) (width int, height int, status WindowCaptureStatus)
	GrabFrame(handle uintptr, timeoutMs int) (WindowCaptureFrame, WindowCaptureStatus)
	Stop(handle uintptr)
	Destroy(handle uintptr)
	LastErrorReason(handle uintptr) string
}

type Bridge interface {
	QueryCapability() Capability
	LastErrorReason() string
	CreateCapturer() uintptr
	CreateEncoder() uintptr
	CreateDecoder() uintptr
	CapturerStart(handle uintptr, displayIndex int) Status
	CapturerGrabFrame(handle uintptr, timeoutMs int) (CapturedFrame, Status)
	CapturerStop(handle uintptr)
	EncoderConfigure(handle uintptr, config EncoderConfig) Status
	EncoderEncodeRGBA(handle uintptr, rgba []byte, timestampMicros int64) (EncodedPacket, Status)
	EncoderEncodeFromCapturer(encoderHandle uintptr, capturerHandle uintptr, timeoutMs int) (EncodedPacket, Status)
	EncoderRequestKeyFrame(handle uintptr) Status
	EncoderClose(handle uintptr)
	DecoderConfigure(handle uintptr, config DecoderConfig) Status
	DecoderDecodeAnnexB(handle uintptr, data []byte, timestampMicros int64) (CapturedFrame, Status)
	DecoderReleaseFrame(handle uintptr, frame *CapturedFrame)
	DecoderClose(handle uintptr)
	DestroyCapturer(handle uintptr)
	DestroyEncoder(handle uintptr)
	DestroyDecoder(handle uintptr)
}
