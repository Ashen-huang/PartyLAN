package video

import "errors"

var (
	ErrNativeBackendUnavailable = errors.New("native video backend unavailable")
	ErrCaptureFrameUnavailable  = errors.New("video capture frame temporarily unavailable")
	ErrUnsupportedHostGPU       = errors.New("unsupported host GPU for native video backend")
	ErrCapabilityCheckFailed    = errors.New("video capability check failed")
)
