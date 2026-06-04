package audio

import (
	"encoding/binary"
	"math"
)

type PCMEncoder struct{}

func NewPCMEncoder() *PCMEncoder {
	return &PCMEncoder{}
}

func (e *PCMEncoder) Encode(frame *AudioFrame) ([]byte, error) {
	buf := make([]byte, 8+len(frame.Samples)*4)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(frame.Samples)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(frame.SampleRate))
	for i, s := range frame.Samples {
		binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(s))
	}
	return buf, nil
}

func (e *PCMEncoder) Close() error {
	return nil
}

type PCMDecoder struct{}

func NewPCMDecoder() *PCMDecoder {
	return &PCMDecoder{}
}

func (d *PCMDecoder) Decode(data []byte) (*AudioFrame, error) {
	if len(data) < 8 {
		return nil, ErrInvalidData
	}
	numSamples := binary.LittleEndian.Uint32(data[0:4])
	sampleRate := binary.LittleEndian.Uint32(data[4:8])

	expectedLen := 8 + int(numSamples)*4
	if len(data) < expectedLen {
		return nil, ErrInvalidData
	}

	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[8+i*4:]))
	}

	return &AudioFrame{
		Samples:    samples,
		Timestamp:  0,
		Channels:   MonoChannels,
		SampleRate: int(sampleRate),
	}, nil
}

func (d *PCMDecoder) Close() error {
	return nil
}

var ErrInvalidData = &AudioError{"invalid audio data"}
