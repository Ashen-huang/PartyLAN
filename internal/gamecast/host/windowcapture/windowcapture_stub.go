//go:build !windows

package windowcapture

import (
	"fmt"
	"image"

	"PartyLAN/internal/video"
)

type Capturer struct{}

const BackendWindowsGraphicsCapture = "windows-graphics-capture"

func New(handle uintptr, frameRate int) *Capturer {
	_ = handle
	_ = frameRate
	return &Capturer{}
}

func (c *Capturer) Backend() string {
	return "unavailable"
}

func (c *Capturer) Start() error {
	return fmt.Errorf("window capture is only supported on Windows")
}

func (c *Capturer) Bounds() (image.Rectangle, error) {
	return image.Rectangle{}, fmt.Errorf("window capture is only supported on Windows")
}

func (c *Capturer) Stop() error {
	return nil
}

func (c *Capturer) GetFrame() (*video.Frame, error) {
	return nil, fmt.Errorf("window capture is only supported on Windows")
}
