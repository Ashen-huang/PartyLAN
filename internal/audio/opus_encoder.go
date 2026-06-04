package audio

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	DefaultOpusSampleRate     = 48000
	DefaultOpusChannels       = 2
	DefaultOpusFrameMillis    = 20
	DefaultOpusMaxPacketBytes = 4000
)

type OpusCapability struct {
	Available bool
	Reason    string
}

type OpusEncoderConfig struct {
	SampleRate     int
	Channels       int
	FrameMillis    int
	MaxPacketBytes int
}

type nativeOpusEncoder interface {
	EncodePCM16(samples []int16) ([]byte, error)
	Close()
}

type OpusEncoder struct {
	native      nativeOpusEncoder
	config      OpusEncoderConfig
	frameValues int
	pending     []int16
}

func NewOpusEncoder(config OpusEncoderConfig) (*OpusEncoder, error) {
	config = normalizeOpusEncoderConfig(config)
	if config.SampleRate <= 0 {
		return nil, fmt.Errorf("opus sample rate must be positive")
	}
	if config.Channels != 1 && config.Channels != 2 {
		return nil, fmt.Errorf("opus channels must be 1 or 2")
	}
	if !validOpusFrameMillis(config.FrameMillis) {
		return nil, fmt.Errorf("opus frame size must be 2.5, 5, 10, 20, 40, or 60 ms; got %d ms", config.FrameMillis)
	}

	native, err := newNativeOpusEncoder(config)
	if err != nil {
		return nil, err
	}

	frameValues := config.SampleRate * config.FrameMillis / 1000 * config.Channels
	return &OpusEncoder{
		native:      native,
		config:      config,
		frameValues: frameValues,
	}, nil
}

func (e *OpusEncoder) EncodePCM16(samples []int16) ([][]byte, error) {
	if e == nil || e.native == nil {
		return nil, fmt.Errorf("opus encoder is closed")
	}
	if len(samples) == 0 {
		return nil, nil
	}

	e.pending = append(e.pending, samples...)
	packets := make([][]byte, 0)
	for len(e.pending) >= e.frameValues {
		frame := e.pending[:e.frameValues]
		packet, err := e.native.EncodePCM16(frame)
		if err != nil {
			return nil, err
		}
		packets = append(packets, packet)
		e.pending = e.pending[e.frameValues:]
	}
	return packets, nil
}

func (e *OpusEncoder) FrameDuration() time.Duration {
	if e == nil || e.config.FrameMillis <= 0 {
		return DefaultOpusFrameMillis * time.Millisecond
	}
	return time.Duration(e.config.FrameMillis) * time.Millisecond
}

func (e *OpusEncoder) Close() error {
	if e == nil || e.native == nil {
		return nil
	}
	e.native.Close()
	e.native = nil
	e.pending = nil
	return nil
}

func PCM16BytesToSamples(data []byte) ([]int16, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("PCM16 data length must be even")
	}
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples, nil
}

func normalizeOpusEncoderConfig(config OpusEncoderConfig) OpusEncoderConfig {
	if config.SampleRate == 0 {
		config.SampleRate = DefaultOpusSampleRate
	}
	if config.Channels == 0 {
		config.Channels = DefaultOpusChannels
	}
	if config.FrameMillis == 0 {
		config.FrameMillis = DefaultOpusFrameMillis
	}
	if config.MaxPacketBytes == 0 {
		config.MaxPacketBytes = DefaultOpusMaxPacketBytes
	}
	return config
}

func validOpusFrameMillis(ms int) bool {
	switch ms {
	case 5, 10, 20, 40, 60:
		return true
	default:
		return false
	}
}
