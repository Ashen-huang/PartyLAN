package voice

import (
	"PartyLAN/internal/audio"
	"PartyLAN/internal/network"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const voicePortOffset = 2

type voiceChannel struct {
	mu sync.Mutex

	network  network.Network
	peerID   string
	listener net.Listener
	conn     net.Conn

	capturer audio.Capturer
	encoder  audio.Encoder
	decoder  audio.Decoder
	mixer    audio.Mixer
	vad      *VAD

	muted       bool
	volume      float32
	state       VoiceState
	stateChange func(peerID string, state VoiceState)

	cancel context.CancelFunc
	done   chan struct{}
}

func NewVoiceChannel(net network.Network) VoiceChannel {
	return &voiceChannel{
		network:  net,
		volume:   1.0,
		state:    VoiceStateIdle,
		capturer: audio.NewMockCapturer(),
		encoder:  audio.NewPCMEncoder(),
		decoder:  audio.NewPCMDecoder(),
		mixer:    audio.NewSimpleMixer(),
		vad:      NewVAD(0),
		done:     make(chan struct{}),
	}
}

func NewVoiceChannelWithCapturer(net network.Network, capturer audio.Capturer) VoiceChannel {
	return &voiceChannel{
		network:  net,
		volume:   1.0,
		state:    VoiceStateIdle,
		capturer: capturer,
		encoder:  audio.NewPCMEncoder(),
		decoder:  audio.NewPCMDecoder(),
		mixer:    audio.NewSimpleMixer(),
		vad:      NewVAD(0),
		done:     make(chan struct{}),
	}
}

func (vc *voiceChannel) Start(ctx context.Context, peerID string) error {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	if vc.cancel != nil {
		return fmt.Errorf("voice channel already started")
	}

	ctx, vc.cancel = context.WithCancel(ctx)
	vc.peerID = peerID

	port := vc.getVoicePort(peerID)

	listener, err := vc.network.Listen(port)
	if err != nil {
		vc.cancel = nil
		return fmt.Errorf("listen on port %d: %w", port, err)
	}
	vc.listener = listener

	if err := vc.capturer.Start(audio.SourceMicrophone, ""); err != nil {
		vc.listener.Close()
		vc.listener = nil
		vc.cancel = nil
		return fmt.Errorf("start capturer: %w", err)
	}

	go vc.acceptLoop(ctx)
	go vc.sendLoop(ctx)

	return nil
}

func (vc *voiceChannel) Stop() error {
	vc.mu.Lock()
	cancel := vc.cancel
	vc.cancel = nil
	vc.mu.Unlock()

	if cancel == nil {
		return nil
	}

	cancel()

	vc.mu.Lock()
	if vc.conn != nil {
		vc.conn.Close()
		vc.conn = nil
	}
	if vc.listener != nil {
		vc.listener.Close()
		vc.listener = nil
	}
	vc.capturer.Stop()
	vc.mu.Unlock()

	vc.setState(VoiceStateIdle)
	return nil
}

func (vc *voiceChannel) Mute(muted bool) {
	vc.mu.Lock()
	vc.muted = muted
	vc.mu.Unlock()

	if muted {
		vc.setState(VoiceStateMuted)
	} else {
		vc.setState(VoiceStateIdle)
	}
}

func (vc *voiceChannel) GetState() VoiceState {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.state
}

func (vc *voiceChannel) SetVolume(volume float32) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	if volume < 0 {
		volume = 0
	}
	if volume > 2.0 {
		volume = 2.0
	}
	vc.volume = volume
}

func (vc *voiceChannel) OnStateChange(fn func(peerID string, state VoiceState)) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.stateChange = fn
}

func (vc *voiceChannel) getVoicePort(peerID string) int {
	// Use a hash of peerID to generate a unique port offset
	hash := uint16(0)
	for _, b := range peerID {
		hash = hash*31 + uint16(b)
	}
	return voicePortOffset + int(hash%1000)
}

func (vc *voiceChannel) setState(state VoiceState) {
	vc.mu.Lock()
	if vc.state == state {
		vc.mu.Unlock()
		return
	}
	vc.state = state
	fn := vc.stateChange
	peerID := vc.peerID
	vc.mu.Unlock()

	if fn != nil {
		go fn(peerID, state)
	}
}

func (vc *voiceChannel) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if tl, ok := vc.listener.(*net.TCPListener); ok {
			tl.SetDeadline(time.Now().Add(1 * time.Second))
		}
		conn, err := vc.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		vc.mu.Lock()
		if vc.conn != nil {
			vc.conn.Close()
		}
		vc.conn = conn
		vc.mu.Unlock()

		go vc.receiveLoop(ctx, conn)
	}
}

func (vc *voiceChannel) sendLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			vc.mu.Lock()
			muted := vc.muted
			conn := vc.conn
			vc.mu.Unlock()

			if conn == nil || muted {
				continue
			}

			frame, err := vc.capturer.GetFrame()
			if err != nil {
				continue
			}

			if vc.vad.Process(frame.Samples) {
				vc.setState(VoiceStateTalking)
			} else {
				vc.setState(VoiceStateIdle)
			}

			if !vc.vad.IsActive() {
				continue
			}

			data, err := vc.encoder.Encode(frame)
			if err != nil {
				continue
			}

			vc.sendPacket(conn, data)
		}
	}
}

func (vc *voiceChannel) receiveLoop(ctx context.Context, conn net.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, err := vc.readPacket(conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}

		frame, err := vc.decoder.Decode(data)
		if err != nil {
			continue
		}

		if sm, ok := vc.mixer.(*audio.SimpleMixer); ok {
			sm.PushFrame(vc.peerID, frame)
		}
	}
}

func (vc *voiceChannel) sendPacket(conn net.Conn, data []byte) error {
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(data)))
	_, err := conn.Write(append(lenBuf, data...))
	return err
}

func (vc *voiceChannel) readPacket(conn net.Conn) ([]byte, error) {
	lenBuf := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := io.ReadFull(conn, lenBuf)
	if err != nil {
		return nil, err
	}

	length := binary.LittleEndian.Uint32(lenBuf)
	if length > 1024*1024 {
		return nil, fmt.Errorf("packet too large: %d bytes", length)
	}

	data := make([]byte, length)
	_, err = io.ReadFull(conn, data)
	if err != nil {
		return nil, err
	}

	return data, nil
}
