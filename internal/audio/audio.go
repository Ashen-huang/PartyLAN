package audio

const (
	SampleRate  = 48000
	Channels    = 2
	FrameSizeMs = 20 // milliseconds per frame
)

type AudioSource int

const (
	SourceMicrophone AudioSource = iota
	SourceSystemLoopback
	SourceVirtualDevice
)

type AudioFrame struct {
	Samples   []float32
	Timestamp int64 // microseconds
	Channels  int
	SampleRate int
}

type Capturer interface {
	Start(source AudioSource, deviceID string) error
	Stop() error
	GetFrame() (*AudioFrame, error)
}

type Encoder interface {
	Encode(frame *AudioFrame) ([]byte, error)
	Close() error
}

type Decoder interface {
	Decode(data []byte) (*AudioFrame, error)
	Close() error
}

type Mixer interface {
	AddStream(streamID string, decoder Decoder)
	RemoveStream(streamID string)
	Mix() (*AudioFrame, error)
	SetVolume(streamID string, volume float32)
}
