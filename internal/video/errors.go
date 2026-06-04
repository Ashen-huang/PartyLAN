package video

import "errors"

var (
	ErrNativeBackendUnavailable = errors.New("native video backend unavailable")
	ErrUnsupportedHostGPU       = errors.New("unsupported host GPU for native video backend")
	ErrCapabilityCheckFailed    = errors.New("video capability check failed")
)
