//go:build windows && cgo && partylan_native_media

package video

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/native/windowsmedia"
)

type NativeCapturer struct {
	bridge  windowsmedia.Bridge
	handle  uintptr
	started bool
	config  CaptureConfig
}

func NewNativeCapturer() *NativeCapturer {
	return &NativeCapturer{
		bridge: windowsmedia.NewBridge(),
	}
}

func (c *NativeCapturer) Start(config CaptureConfig) error {
	if c.started {
		return fmt.Errorf("native capturer already running")
	}
	if c.handle == 0 {
		c.handle = c.bridge.CreateCapturer()
	}
	if c.handle == 0 {
		return ErrNativeBackendUnavailable
	}
	status := c.bridge.CapturerStart(c.handle, config.DisplayIndex)
	if status != windowsmedia.StatusOK {
		return fmt.Errorf("native capturer start failed: %w", mapNativeStatusError(status, c.bridge.LastErrorReason()))
	}

	c.config = config
	c.started = true
	return nil
}

func (c *NativeCapturer) Stop() error {
	if c.handle != 0 {
		c.bridge.CapturerStop(c.handle)
		c.bridge.DestroyCapturer(c.handle)
		c.handle = 0
	}
	c.started = false
	return nil
}

func (c *NativeCapturer) Handle() uintptr {
	return c.handle
}

func (c *NativeCapturer) GetFrame() (*Frame, error) {
	if !c.started {
		return nil, fmt.Errorf("native capturer not running")
	}

	timeoutMs := 16
	if c.config.FrameRate > 0 {
		timeoutMs = 1000 / c.config.FrameRate
		if timeoutMs < 5 {
			timeoutMs = 5
		}
	}

	captured, status := c.bridge.CapturerGrabFrame(c.handle, timeoutMs)
	if status != windowsmedia.StatusOK {
		return nil, mapNativeStatusError(status, c.bridge.LastErrorReason())
	}

	return &Frame{
		Data:      captured.Data,
		Timestamp: captured.TimestampMicros,
		Width:     captured.Width,
		Height:    captured.Height,
	}, nil
}

type NativeEncoder struct {
	bridge         windowsmedia.Bridge
	handle         uintptr
	configured     bool
	inputFormat    NativePixelFormat
	capturerHandle uintptr
}

func NewNativeEncoder() *NativeEncoder {
	return &NativeEncoder{
		bridge:      windowsmedia.NewBridge(),
		inputFormat: NativePixelFormatRGBA,
	}
}

func (e *NativeEncoder) SetInputPixelFormat(format NativePixelFormat) {
	e.inputFormat = format
}

func (e *NativeEncoder) SetCapturerHandle(handle uintptr) {
	e.capturerHandle = handle
}

func (e *NativeEncoder) EncodeFromCapturer(capturer *NativeCapturer, timeoutMs int) ([]byte, int64, error) {
	if !e.configured || e.handle == 0 || capturer == nil || capturer.handle == 0 {
		return nil, 0, ErrNativeBackendUnavailable
	}
	packet, status := e.bridge.EncoderEncodeFromCapturer(e.handle, capturer.handle, timeoutMs)
	if status != windowsmedia.StatusOK {
		return nil, 0, mapNativeStatusError(status, e.bridge.LastErrorReason())
	}
	return packet.Data, packet.TimestampMicros, nil
}

func (e *NativeEncoder) Configure(config EncoderConfig) error {
	if e.handle == 0 {
		e.handle = e.bridge.CreateEncoder()
	}
	if e.handle == 0 {
		return ErrNativeBackendUnavailable
	}

	status := e.bridge.EncoderConfigure(e.handle, windowsmedia.EncoderConfig{
		Width:          config.Width,
		Height:         config.Height,
		FPS:            config.FrameRate,
		BitrateKbps:    config.Bitrate,
		PixelFormat:    windowsmedia.PixelFormat(e.inputFormat),
		CapturerHandle: e.capturerHandle,
	})
	if status != windowsmedia.StatusOK {
		return fmt.Errorf("native encoder configure failed: %w", mapNativeStatusError(status, e.bridge.LastErrorReason()))
	}

	e.configured = true
	return nil
}

func (e *NativeEncoder) Encode(frame *Frame) ([]byte, error) {
	if !e.configured || frame == nil {
		return nil, ErrNativeBackendUnavailable
	}

	packet, status := e.bridge.EncoderEncodeRGBA(e.handle, frame.Data, frame.Timestamp)
	if status != windowsmedia.StatusOK {
		return nil, mapNativeStatusError(status, e.bridge.LastErrorReason())
	}
	return packet.Data, nil
}

func (e *NativeEncoder) ForceKeyFrame() error {
	if !e.configured || e.handle == 0 {
		return ErrNativeBackendUnavailable
	}
	status := e.bridge.EncoderRequestKeyFrame(e.handle)
	if status != windowsmedia.StatusOK {
		return mapNativeStatusError(status, e.bridge.LastErrorReason())
	}
	return nil
}

func (e *NativeEncoder) Flush() ([]byte, error) {
	return nil, nil
}

func (e *NativeEncoder) Close() error {
	if e.handle != 0 {
		e.bridge.EncoderClose(e.handle)
		e.bridge.DestroyEncoder(e.handle)
		e.handle = 0
	}
	e.configured = false
	return nil
}

type NativeDecoder struct {
	mu         sync.Mutex
	bridge     windowsmedia.Bridge
	handle     uintptr
	configured bool
	width      int
	height     int
	closed     bool
}

func NewNativeDecoder() *NativeDecoder {
	return newNativeDecoderWithBridge(windowsmedia.NewBridge())
}

func newNativeDecoderWithBridge(bridge windowsmedia.Bridge) *NativeDecoder {
	return &NativeDecoder{bridge: bridge}
}

func (d *NativeDecoder) SetDimensions(width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.width == width && d.height == height {
		return
	}

	d.width = width
	d.height = height
	if d.configured && d.handle != 0 {
		d.bridge.DecoderClose(d.handle)
		d.configured = false
	}
}

func (d *NativeDecoder) Decode(nalUnits []byte) (*Frame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil, fmt.Errorf("%w: native decoder is closed", ErrNativeBackendUnavailable)
	}
	if len(nalUnits) == 0 {
		return nil, fmt.Errorf("empty H264 Annex-B packet")
	}
	if d.width <= 0 || d.height <= 0 {
		return nil, fmt.Errorf("native H264 decoder dimensions are not set")
	}
	if d.bridge == nil {
		return nil, ErrNativeBackendUnavailable
	}
	if d.handle == 0 {
		d.handle = d.bridge.CreateDecoder()
		if d.handle == 0 {
			return nil, ErrNativeBackendUnavailable
		}
	}
	if !d.configured {
		status := d.bridge.DecoderConfigure(d.handle, windowsmedia.DecoderConfig{
			Width:  d.width,
			Height: d.height,
		})
		if status != windowsmedia.StatusOK {
			return nil, fmt.Errorf("native decoder configure failed: %w", mapNativeStatusError(status, d.bridge.LastErrorReason()))
		}
		d.configured = true
	}

	decoded, status := d.bridge.DecoderDecodeAnnexB(d.handle, nalUnits, time.Now().UnixMicro())
	if status != windowsmedia.StatusOK {
		return nil, mapNativeStatusError(status, d.bridge.LastErrorReason())
	}
	if decoded.Width <= 0 || decoded.Height <= 0 || len(decoded.Data) == 0 {
		return nil, fmt.Errorf("%w: native decoder returned an empty frame", ErrNativeBackendUnavailable)
	}

	return &Frame{
		Data:      decoded.Data,
		Timestamp: decoded.TimestampMicros,
		Width:     decoded.Width,
		Height:    decoded.Height,
	}, nil
}

func (d *NativeDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 && d.bridge != nil {
		d.bridge.DecoderClose(d.handle)
		d.bridge.DestroyDecoder(d.handle)
		d.handle = 0
	}
	d.configured = false
	d.closed = true
	return nil
}

func mapNativeStatusError(status windowsmedia.Status, reason string) error {
	reason = strings.TrimSpace(reason)

	switch status {
	case windowsmedia.StatusOK:
		return nil
	case windowsmedia.StatusUnsupportedGPU:
		if reason != "" {
			return fmt.Errorf("%w: %s", ErrUnsupportedHostGPU, reason)
		}
		return ErrUnsupportedHostGPU
	case windowsmedia.StatusNotImplemented:
		if reason != "" {
			return fmt.Errorf("%w: %s", ErrNativeBackendUnavailable, reason)
		}
		return fmt.Errorf("%w: not implemented", ErrNativeBackendUnavailable)
	case windowsmedia.StatusInternalError:
		if reason != "" {
			return fmt.Errorf("%w: %s", ErrNativeBackendUnavailable, reason)
		}
		return fmt.Errorf("%w: internal error", ErrNativeBackendUnavailable)
	default:
		if reason != "" {
			return fmt.Errorf("%w: %s", ErrNativeBackendUnavailable, reason)
		}
		return ErrNativeBackendUnavailable
	}
}
