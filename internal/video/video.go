package video

import "image"

type CodecType int

const (
	CodecH264 CodecType = iota
	CodecH265
)

type QualityPreset int

const (
	QualitySmooth QualityPreset = iota // 720p30
	QualityBalanced                    // 1080p30
	QualityHigh                        // 1080p60
	QualityOriginal                    // native 60fps
)

type CaptureConfig struct {
	DisplayIndex int
	Region       *image.Rectangle // nil = full screen
	FrameRate    int
}

type EncoderConfig struct {
	Codec    CodecType
	Bitrate  int // kbps
	Width    int
	Height   int
	FrameRate int
}

type Frame struct {
	Data      []byte
	Timestamp int64 // microseconds
	Width     int
	Height    int
}

type Capturer interface {
	Start(config CaptureConfig) error
	Stop() error
	GetFrame() (*Frame, error)
}

type Encoder interface {
	Configure(config EncoderConfig) error
	Encode(frame *Frame) ([]byte, error) // returns NAL units
	Flush() ([]byte, error)
	Close() error
}

type Decoder interface {
	Decode(nalUnits []byte) (*Frame, error)
	Close() error
}
