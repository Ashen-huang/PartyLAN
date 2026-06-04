package audio

import "sync"

type streamEntry struct {
	volume float32
	frames chan *AudioFrame
}

const streamBufferSize = 5

type SimpleMixer struct {
	mu      sync.Mutex
	streams map[string]*streamEntry
}

func NewSimpleMixer() *SimpleMixer {
	return &SimpleMixer{
		streams: make(map[string]*streamEntry),
	}
}

func (m *SimpleMixer) AddStream(streamID string, decoder Decoder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[streamID] = &streamEntry{
		volume: 1.0,
		frames: make(chan *AudioFrame, streamBufferSize),
	}
}

func (m *SimpleMixer) RemoveStream(streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, streamID)
}

func (m *SimpleMixer) PushFrame(streamID string, frame *AudioFrame) {
	m.mu.Lock()
	s, ok := m.streams[streamID]
	m.mu.Unlock()
	if !ok {
		return
	}
	select {
	case s.frames <- frame:
	default:
		<-s.frames
		s.frames <- frame
	}
}

func (m *SimpleMixer) Mix() (*AudioFrame, error) {
	m.mu.Lock()
	streams := make([]*streamEntry, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	m.mu.Unlock()

	samplesPerFrame := SampleRate * FrameSizeMs / 1000
	mixed := make([]float32, samplesPerFrame)

	if len(streams) == 0 {
		return &AudioFrame{
			Samples:    mixed,
			Channels:   MonoChannels,
			SampleRate: SampleRate,
		}, nil
	}

	for _, s := range streams {
		select {
		case frame := <-s.frames:
			for i := 0; i < samplesPerFrame && i < len(frame.Samples); i++ {
				mixed[i] += frame.Samples[i] * s.volume
			}
		default:
		}
	}

	for i := range mixed {
		if mixed[i] > 1.0 {
			mixed[i] = 1.0
		} else if mixed[i] < -1.0 {
			mixed[i] = -1.0
		}
	}

	return &AudioFrame{
		Samples:    mixed,
		Channels:   MonoChannels,
		SampleRate: SampleRate,
	}, nil
}

func (m *SimpleMixer) SetVolume(streamID string, volume float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.streams[streamID]; ok {
		if volume < 0 {
			volume = 0
		}
		if volume > 2.0 {
			volume = 2.0
		}
		s.volume = volume
	}
}
