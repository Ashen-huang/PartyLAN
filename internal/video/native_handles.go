//go:build !windows || !cgo || !partylan_native_media

package video

type NativeCapturer struct {
	err error
}

func NewNativeCapturer() *NativeCapturer {
	return &NativeCapturer{err: ErrNativeBackendUnavailable}
}

func (c *NativeCapturer) Start(config CaptureConfig) error {
	_ = config
	return c.err
}

func (c *NativeCapturer) Stop() error {
	return nil
}

func (c *NativeCapturer) GetFrame() (*Frame, error) {
	return nil, c.err
}

func (c *NativeCapturer) Handle() uintptr {
	return 0
}

type NativeEncoder struct {
	err            error
	inputFormat    NativePixelFormat
	capturerHandle uintptr
}

func NewNativeEncoder() *NativeEncoder {
	return &NativeEncoder{
		err:         ErrNativeBackendUnavailable,
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
	_ = capturer
	_ = timeoutMs
	return nil, 0, e.err
}

func (e *NativeEncoder) Configure(config EncoderConfig) error {
	_ = config
	return e.err
}

func (e *NativeEncoder) Encode(frame *Frame) ([]byte, error) {
	_ = frame
	return nil, e.err
}

func (e *NativeEncoder) ForceKeyFrame() error {
	return e.err
}

func (e *NativeEncoder) Flush() ([]byte, error) {
	return nil, e.err
}

func (e *NativeEncoder) Close() error {
	return nil
}

type NativeDecoder struct {
	err error
}

func NewNativeDecoder() *NativeDecoder {
	return &NativeDecoder{err: ErrNativeBackendUnavailable}
}

func (d *NativeDecoder) Decode(nalUnits []byte) (*Frame, error) {
	_ = nalUnits
	return nil, d.err
}

func (d *NativeDecoder) SetDimensions(width, height int) {
	_ = width
	_ = height
}

func (d *NativeDecoder) Close() error {
	return nil
}
