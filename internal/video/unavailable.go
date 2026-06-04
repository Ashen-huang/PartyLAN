package video

type unavailableCapturer struct {
	err error
}

func NewUnavailableCapturer(err error) Capturer {
	return &unavailableCapturer{err: err}
}

func (c *unavailableCapturer) Start(config CaptureConfig) error {
	_ = config
	return c.err
}

func (c *unavailableCapturer) Stop() error {
	return nil
}

func (c *unavailableCapturer) GetFrame() (*Frame, error) {
	return nil, c.err
}

type unavailableEncoder struct {
	err error
}

func NewUnavailableEncoder(err error) Encoder {
	return &unavailableEncoder{err: err}
}

func (e *unavailableEncoder) Configure(config EncoderConfig) error {
	_ = config
	return e.err
}

func (e *unavailableEncoder) Encode(frame *Frame) ([]byte, error) {
	_ = frame
	return nil, e.err
}

func (e *unavailableEncoder) Flush() ([]byte, error) {
	return nil, e.err
}

func (e *unavailableEncoder) Close() error {
	return nil
}

type unavailableDecoder struct {
	err error
}

func NewUnavailableDecoder(err error) Decoder {
	return &unavailableDecoder{err: err}
}

func (d *unavailableDecoder) Decode(nalUnits []byte) (*Frame, error) {
	_ = nalUnits
	return nil, d.err
}

func (d *unavailableDecoder) Close() error {
	return nil
}
