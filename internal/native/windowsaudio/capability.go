package windowsaudio

type Status string

const (
	StatusOK             Status = "ok"
	StatusUnavailable    Status = "unavailable"
	StatusNotImplemented Status = "not_implemented"
	StatusInternalError  Status = "internal_error"
)

type Capability struct {
	SupportsProcessCapture bool
	Reason                 string
}

type AudioSource struct {
	ProcessID   uint32
	Name        string
	DisplayName string
	IconDataURL string
	Active      bool
}

type CaptureMode int

const (
	CaptureIncludeProcessTree CaptureMode = iota
	CaptureExcludeProcessTree
	CaptureSystemMix
)

type SessionConfig struct {
	TargetProcessID uint32
	Mode            CaptureMode
	SampleRate      uint32
	Channels        uint32
	FrameMillis     uint32
}

type PCMFrame struct {
	Data            []byte
	Channels        uint32
	SampleRate      uint32
	TimestampMicros int64
}

type Bridge interface {
	QueryCapability() Capability
	LastErrorReason() string
	CreateSession() uintptr
	ConfigureSession(handle uintptr, config SessionConfig) Status
	StartSession(handle uintptr) Status
	ReadFrame(handle uintptr, timeoutMs int) (PCMFrame, Status)
	StopSession(handle uintptr)
	DestroySession(handle uintptr)
}
