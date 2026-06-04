package video

import (
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
)

type ScreenCapturer struct {
	mu       sync.Mutex
	config   CaptureConfig
	bounds   image.Rectangle
	running  bool
	stopCh   chan struct{}
	lastTime time.Time
}

func NewScreenCapturer() *ScreenCapturer {
	return &ScreenCapturer{
		stopCh: make(chan struct{}),
	}
}

func (c *ScreenCapturer) Start(config CaptureConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("capturer already running")
	}

	n := screenshot.NumActiveDisplays()
	if config.DisplayIndex < 0 || config.DisplayIndex >= n {
		return fmt.Errorf("display index %d out of range (0-%d)", config.DisplayIndex, n-1)
	}

	bounds := screenshot.GetDisplayBounds(config.DisplayIndex)
	c.bounds = bounds
	if config.Region != nil {
		c.bounds = config.Region.Intersect(bounds)
		if c.bounds.Empty() {
			return fmt.Errorf("capture region is outside display bounds")
		}
	}

	if config.FrameRate <= 0 {
		config.FrameRate = 30
	}
	c.config = config
	c.running = true
	c.stopCh = make(chan struct{})
	c.lastTime = time.Time{}

	return nil
}

func (c *ScreenCapturer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false
	close(c.stopCh)
	return nil
}

func (c *ScreenCapturer) GetFrame() (*Frame, error) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil, fmt.Errorf("capturer not running")
	}
	c.mu.Unlock()

	if c.config.FrameRate > 0 {
		frameInterval := time.Second / time.Duration(c.config.FrameRate)
		now := time.Now()
		elapsed := now.Sub(c.lastTime)
		if elapsed < frameInterval {
			time.Sleep(frameInterval - elapsed)
		}
		c.lastTime = time.Now()
	}

	img, err := screenshot.CaptureRect(c.bounds)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	raw := img.Pix

	frame := &Frame{
		Data:      make([]byte, len(raw)),
		Timestamp: time.Now().UnixMicro(),
		Width:     w,
		Height:    h,
	}
	copy(frame.Data, raw)

	return frame, nil
}

func ListDisplays() []image.Rectangle {
	n := screenshot.NumActiveDisplays()
	displays := make([]image.Rectangle, n)
	for i := 0; i < n; i++ {
		displays[i] = screenshot.GetDisplayBounds(i)
	}
	return displays
}
