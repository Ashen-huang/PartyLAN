package windowcapture

import "errors"

var (
	ErrWindowClosed     = errors.New("shared window is closed")
	ErrFrameUnavailable = errors.New("window frame is temporarily unavailable")
)
