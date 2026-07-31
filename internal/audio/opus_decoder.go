package audio

import (
	"fmt"
	"sync"
)

type OpusDecoderConfig struct {
	SampleRate  int
	Channels    int
	FrameMillis int
}

type nativeOpusDecoder interface {
	Decode(packet []byte, frameSize int, decodeFEC bool) ([]int16, error)
	Close()
}

type OpusDecoder struct {
	mu        sync.Mutex
	native    nativeOpusDecoder
	config    OpusDecoderConfig
	frameSize int
}

func NewOpusDecoder(config OpusDecoderConfig) (*OpusDecoder, error) {
	config = normalizeOpusDecoderConfig(config)
	if config.SampleRate <= 0 || config.Channels != 1 || !validOpusFrameMillis(config.FrameMillis) {
		return nil, fmt.Errorf("opus decoder requires mono audio and a valid frame size")
	}
	native, err := newNativeOpusDecoder(config)
	if err != nil {
		return nil, err
	}
	return newOpusDecoderWithNative(config, native), nil
}

func newOpusDecoderWithNative(config OpusDecoderConfig, native nativeOpusDecoder) *OpusDecoder {
	config = normalizeOpusDecoderConfig(config)
	return &OpusDecoder{
		native:    native,
		config:    config,
		frameSize: config.SampleRate * config.FrameMillis / 1000,
	}
}

func (d *OpusDecoder) Decode(packet []byte) ([]int16, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("opus packet is empty")
	}
	return d.decode(packet, false)
}

func (d *OpusDecoder) DecodeFEC(packet []byte) ([]int16, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("opus FEC packet is empty")
	}
	return d.decode(packet, true)
}

func (d *OpusDecoder) ConcealLoss() ([]int16, error) {
	return d.decode(nil, false)
}

func (d *OpusDecoder) decode(packet []byte, fec bool) ([]int16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.native == nil {
		return nil, fmt.Errorf("opus decoder is closed")
	}
	return d.native.Decode(packet, d.frameSize, fec)
}

func (d *OpusDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.native != nil {
		d.native.Close()
		d.native = nil
	}
	return nil
}

func normalizeOpusDecoderConfig(config OpusDecoderConfig) OpusDecoderConfig {
	if config.SampleRate == 0 {
		config.SampleRate = DefaultOpusSampleRate
	}
	if config.Channels == 0 {
		config.Channels = 1
	}
	if config.FrameMillis == 0 {
		config.FrameMillis = DefaultOpusFrameMillis
	}
	return config
}
