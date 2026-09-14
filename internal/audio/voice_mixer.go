package audio

import (
	"errors"
	"fmt"
	"sync"
)

const (
	MaxVoiceRemoteStreams = 19
	VoiceFrameSamples     = DefaultOpusSampleRate * DefaultOpusFrameMillis / 1000
	voiceStreamBufferSize = 5
)

var (
	ErrVoiceMixerFull    = errors.New("voice mixer is full")
	ErrVoiceMixerClosed  = errors.New("voice mixer is closed")
	ErrVoiceFrameInvalid = errors.New("voice frame is invalid")
)

type voiceMixStream struct {
	frames chan []int16
}

type VoiceMixer struct {
	mu      sync.Mutex
	closed  bool
	muted   bool
	streams map[string]*voiceMixStream
}

func NewVoiceMixer() *VoiceMixer {
	return &VoiceMixer{streams: make(map[string]*voiceMixStream)}
}

func (m *VoiceMixer) AddStream(streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrVoiceMixerClosed
	}
	if streamID == "" {
		return fmt.Errorf("%w: stream id is required", ErrVoiceFrameInvalid)
	}
	if m.streams[streamID] != nil {
		return nil
	}
	if len(m.streams) >= MaxVoiceRemoteStreams {
		return ErrVoiceMixerFull
	}
	m.streams[streamID] = &voiceMixStream{frames: make(chan []int16, voiceStreamBufferSize)}
	return nil
}

func (m *VoiceMixer) RemoveStream(streamID string) {
	m.mu.Lock()
	delete(m.streams, streamID)
	m.mu.Unlock()
}

func (m *VoiceMixer) PushFrame(streamID string, samples []int16) error {
	if len(samples) != VoiceFrameSamples {
		return fmt.Errorf("%w: got %d samples, want %d", ErrVoiceFrameInvalid, len(samples), VoiceFrameSamples)
	}
	frame := append([]int16(nil), samples...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrVoiceMixerClosed
	}
	stream := m.streams[streamID]
	if stream == nil {
		return fmt.Errorf("%w: stream is not registered", ErrVoiceFrameInvalid)
	}
	select {
	case stream.frames <- frame:
	default:
		<-stream.frames
		stream.frames <- frame
	}
	return nil
}

func (m *VoiceMixer) Mix() ([]int16, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrVoiceMixerClosed
	}
	muted := m.muted
	streams := make([]*voiceMixStream, 0, len(m.streams))
	for _, stream := range m.streams {
		streams = append(streams, stream)
	}
	m.mu.Unlock()

	sums := make([]int64, VoiceFrameSamples)
	for _, stream := range streams {
		select {
		case frame := <-stream.frames:
			for index, sample := range frame {
				sums[index] += int64(sample)
			}
		default:
		}
	}
	mixed := make([]int16, VoiceFrameSamples)
	if muted {
		return mixed, nil
	}
	for index, sample := range sums {
		switch {
		case sample > 32767:
			mixed[index] = 32767
		case sample < -32768:
			mixed[index] = -32768
		default:
			mixed[index] = int16(sample)
		}
	}
	return mixed, nil
}

func (m *VoiceMixer) SetMuted(muted bool) {
	m.mu.Lock()
	m.muted = muted
	m.mu.Unlock()
}

func (m *VoiceMixer) Close() {
	m.mu.Lock()
	m.closed = true
	m.streams = nil
	m.mu.Unlock()
}
