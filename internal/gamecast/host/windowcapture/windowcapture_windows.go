//go:build windows

package windowcapture

import (
	"fmt"
	"image"
	"strings"

	"PartyLAN/internal/native/windowsmedia"
	"PartyLAN/internal/video"
)

const BackendWindowsGraphicsCapture = "windows-graphics-capture"

type Capturer struct {
	bridge    windowsmedia.WindowCaptureBridge
	handle    uintptr
	hwnd      uintptr
	frameRate int
	running   bool
}

func New(handle uintptr, frameRate int) *Capturer {
	return &Capturer{
		bridge:    windowsmedia.NewWindowCaptureBridge(),
		hwnd:      handle,
		frameRate: frameRate,
	}
}

func (c *Capturer) Backend() string {
	return BackendWindowsGraphicsCapture
}

func (c *Capturer) Start() error {
	if c.hwnd == 0 {
		return fmt.Errorf("window handle is required")
	}
	if _, _, status := c.bridge.Bounds(c.hwnd); status != windowsmedia.WindowCaptureStatusOK {
		return c.statusError(status, "validate shared window")
	}
	if c.running || c.handle != 0 {
		_ = c.Stop()
	}
	c.handle = c.bridge.Create()
	if c.handle == 0 {
		return fmt.Errorf("Windows Graphics Capture is unavailable")
	}
	if status := c.bridge.Start(c.handle, c.hwnd); status != windowsmedia.WindowCaptureStatusOK {
		err := c.statusError(status, "start Windows Graphics Capture")
		c.bridge.Destroy(c.handle)
		c.handle = 0
		return err
	}
	if c.frameRate <= 0 {
		c.frameRate = 30
	}
	c.running = true
	return nil
}

func (c *Capturer) Bounds() (image.Rectangle, error) {
	if c.hwnd == 0 {
		return image.Rectangle{}, fmt.Errorf("window handle is required")
	}
	width, height, status := c.bridge.Bounds(c.hwnd)
	if status != windowsmedia.WindowCaptureStatusOK {
		return image.Rectangle{}, c.statusError(status, "read shared window bounds")
	}
	if width <= 0 || height <= 0 {
		return image.Rectangle{}, fmt.Errorf("%w: selected window has invalid bounds", ErrFrameUnavailable)
	}
	return image.Rect(0, 0, width, height), nil
}

func (c *Capturer) Stop() error {
	c.running = false
	if c.handle != 0 {
		c.bridge.Stop(c.handle)
		c.bridge.Destroy(c.handle)
		c.handle = 0
	}
	return nil
}

func (c *Capturer) GetFrame() (*video.Frame, error) {
	if !c.running || c.handle == 0 {
		return nil, fmt.Errorf("window capturer not running")
	}
	timeoutMs := 33
	if c.frameRate > 0 {
		timeoutMs = 1000 / c.frameRate
		if timeoutMs < 5 {
			timeoutMs = 5
		}
		if timeoutMs > 50 {
			timeoutMs = 50
		}
	}

	frame, status := c.bridge.GrabFrame(c.handle, timeoutMs)
	if status != windowsmedia.WindowCaptureStatusOK {
		return nil, c.statusError(status, "capture shared window frame")
	}
	if frame.Width <= 0 || frame.Height <= 0 || len(frame.Data) < frame.Width*frame.Height*4 {
		return nil, fmt.Errorf("%w: Windows Graphics Capture returned an invalid BGRA frame", ErrFrameUnavailable)
	}

	out := &video.Frame{
		Data:      frame.Data,
		Timestamp: frame.TimestampMicros,
		Width:     frame.Width,
		Height:    frame.Height,
	}
	return out, nil
}

func (c *Capturer) statusError(status windowsmedia.WindowCaptureStatus, operation string) error {
	reason := ""
	if c.bridge != nil {
		reason = strings.TrimSpace(c.bridge.LastErrorReason(c.handle))
	}
	if reason == "" || c.handle == 0 {
		reason = operation
	}
	switch status {
	case windowsmedia.WindowCaptureStatusWindowClosed:
		return fmt.Errorf("%w: %s", ErrWindowClosed, reason)
	case windowsmedia.WindowCaptureStatusFrameUnavailable:
		return fmt.Errorf("%w: %s", ErrFrameUnavailable, reason)
	case windowsmedia.WindowCaptureStatusInternalError:
		return fmt.Errorf("Windows Graphics Capture internal error: %s", reason)
	default:
		return fmt.Errorf("Windows Graphics Capture unavailable: %s", reason)
	}
}
