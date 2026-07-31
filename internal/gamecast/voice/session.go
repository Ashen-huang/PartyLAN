package voice

import (
	"context"
	"errors"
	"sync"
	"time"

	"PartyLAN/internal/audio"
	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/native/windowsvoice"
)

type ConnectionState string

const (
	ConnectionIdle         ConnectionState = "idle"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionFull         ConnectionState = "full"
	ConnectionUnauthorized ConnectionState = "unauthorized"
	ConnectionError        ConnectionState = "error"
)

type MicrophoneState string

const (
	MicrophoneStarting    MicrophoneState = "starting"
	MicrophoneLive        MicrophoneState = "live"
	MicrophoneMuted       MicrophoneState = "muted"
	MicrophoneUnavailable MicrophoneState = "unavailable"
)

type State struct {
	Connection       ConnectionState `json:"connection"`
	Microphone       MicrophoneState `json:"microphone"`
	PlaybackMuted    bool            `json:"playbackMuted"`
	ParticipantCount int             `json:"participantCount"`
	ErrorCode        string          `json:"errorCode,omitempty"`
}

type TokenIssuer interface {
	IssueVoiceToken(context.Context, gamecast.VoiceTokenRequest) (gamecast.VoiceTokenResponse, error)
	Profile() gamecast.Profile
}

type ConnectionEvent struct {
	State            ConnectionState
	ParticipantCount int
	ErrorCode        string
	Err              error
}

type RemotePacket struct {
	TrackID        string
	SequenceNumber uint16
	Payload        []byte
	End            bool
}

type Connection interface {
	Connect(context.Context, string) error
	Events() <-chan ConnectionEvent
	RemotePackets() <-chan RemotePacket
	Done() <-chan struct{}
	WriteOpus([]byte, time.Duration) error
	RefreshAuth(context.Context, string) error
	Close()
}

type ConnectionFactory func(gamecast.Profile) Connection
type MicrophoneFactory func() windowsvoice.Microphone

type PacketEncoder interface {
	EncodePCM16([]int16) ([][]byte, error)
	FrameDuration() time.Duration
	Close() error
}

type PacketDecoder interface {
	Decode([]byte) ([]int16, error)
	DecodeFEC([]byte) ([]int16, error)
	ConcealLoss() ([]int16, error)
	Close() error
}

type Config struct {
	RoomID            string
	ParticipantID     string
	TokenIssuer       TokenIssuer
	ConnectionFactory ConnectionFactory
	MicrophoneFactory MicrophoneFactory
	Speaker           windowsvoice.Speaker
	EncoderFactory    func() (PacketEncoder, error)
	DecoderFactory    func() (PacketDecoder, error)
	RefreshInterval   time.Duration
	BackoffDelays     []time.Duration
	Wait              func(context.Context, time.Duration) bool
	OnState           func(State)
}

type ConnectError struct {
	Code string
	Err  error
}

func (e *ConnectError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *ConnectError) Unwrap() error { return e.Err }

type Session struct {
	mu                sync.Mutex
	config            Config
	state             State
	generation        uint64
	cancel            context.CancelFunc
	runContext        context.Context
	connection        Connection
	microphone        windowsvoice.Microphone
	encoder           PacketEncoder
	microphoneCancel  context.CancelFunc
	microphoneDone    chan struct{}
	microphoneEnabled bool
	microphoneRunning bool
	mixer             *audio.VoiceMixer
}

func New(config Config) *Session {
	if config.MicrophoneFactory == nil {
		config.MicrophoneFactory = windowsvoice.NewDefaultMicrophone
	}
	if config.Speaker == nil {
		config.Speaker = windowsvoice.NewRecoveringDefaultSpeaker()
	}
	if config.EncoderFactory == nil {
		config.EncoderFactory = func() (PacketEncoder, error) {
			return audio.NewOpusEncoder(audio.VoiceOpusEncoderConfig())
		}
	}
	if config.DecoderFactory == nil {
		config.DecoderFactory = func() (PacketDecoder, error) {
			return audio.NewOpusDecoder(audio.OpusDecoderConfig{})
		}
	}
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = 5 * time.Second
	}
	if len(config.BackoffDelays) == 0 {
		config.BackoffDelays = []time.Duration{
			time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			15 * time.Second,
		}
	}
	if config.Wait == nil {
		config.Wait = waitDelay
	}
	return &Session{
		config: config,
		state: State{
			Connection: ConnectionIdle,
			Microphone: MicrophoneMuted,
		},
	}
}

func (s *Session) Start(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	if s.config.TokenIssuer == nil || s.config.ConnectionFactory == nil {
		return errors.New("voice token issuer and connection factory are required")
	}
	if s.config.RoomID == "" || s.config.ParticipantID == "" {
		return errors.New("voice room and participant id are required")
	}
	s.stopCurrent(false)
	ctx, cancel := context.WithCancel(parent)
	mixer := audio.NewVoiceMixer()
	s.mu.Lock()
	s.generation++
	generation := s.generation
	s.cancel = cancel
	s.runContext = ctx
	s.mixer = mixer
	s.microphoneEnabled = true
	s.microphoneRunning = false
	s.state = State{
		Connection:    ConnectionConnecting,
		Microphone:    MicrophoneStarting,
		PlaybackMuted: false,
	}
	state := s.state
	s.mu.Unlock()
	s.emit(state)

	if err := s.config.Speaker.Start(ctx); err != nil {
		s.setErrorIfCurrent(generation, "voice_speaker_unavailable")
	}
	s.startMicrophone(ctx, generation)
	go s.runPlayback(ctx, generation, mixer)
	go s.runNetwork(ctx, generation)
	return nil
}

func (s *Session) Stop() {
	s.stopCurrent(true)
}

func (s *Session) stopCurrent(emit bool) {
	s.mu.Lock()
	if s.cancel == nil && s.connection == nil && s.microphone == nil && s.mixer == nil {
		s.mu.Unlock()
		return
	}
	s.generation++
	cancel := s.cancel
	connection := s.connection
	microphoneCancel := s.microphoneCancel
	microphoneDone := s.microphoneDone
	mixer := s.mixer
	s.cancel = nil
	s.runContext = nil
	s.connection = nil
	s.mixer = nil
	s.microphoneEnabled = false
	s.microphoneRunning = false
	s.state = State{Connection: ConnectionIdle, Microphone: MicrophoneMuted}
	state := s.state
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if connection != nil {
		connection.Close()
	}
	if microphoneCancel != nil {
		microphoneCancel()
	}
	if microphoneDone != nil {
		<-microphoneDone
	}
	s.mu.Lock()
	s.microphone = nil
	s.encoder = nil
	s.microphoneCancel = nil
	s.microphoneDone = nil
	s.mu.Unlock()
	if mixer != nil {
		mixer.Close()
	}
	s.config.Speaker.Stop()
	if emit {
		s.emit(state)
	}
}

func (s *Session) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) SetMicrophoneEnabled(enabled bool) State {
	s.mu.Lock()
	if s.cancel == nil {
		state := s.state
		s.mu.Unlock()
		return state
	}
	if !enabled {
		s.microphoneEnabled = false
		cancel := s.microphoneCancel
		done := s.microphoneDone
		s.microphoneRunning = false
		s.state.Microphone = MicrophoneMuted
		state := s.state
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
		s.emit(state)
		return state
	}
	s.microphoneEnabled = true
	if s.microphoneRunning {
		state := s.state
		s.mu.Unlock()
		return state
	}
	s.state.Microphone = MicrophoneStarting
	state := s.state
	ctx := s.runContext
	generation := s.generation
	s.mu.Unlock()
	s.emit(state)
	s.startMicrophone(ctx, generation)
	return state
}

func (s *Session) SetPlaybackMuted(muted bool) State {
	s.mu.Lock()
	s.state.PlaybackMuted = muted
	if s.mixer != nil {
		s.mixer.SetMuted(muted)
	}
	state := s.state
	s.mu.Unlock()
	s.emit(state)
	return state
}

func (s *Session) startMicrophone(parent context.Context, generation uint64) {
	s.mu.Lock()
	if generation != s.generation || !s.microphoneEnabled || s.microphoneRunning {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.microphoneRunning = true
	s.microphoneCancel = cancel
	s.microphoneDone = done
	s.mu.Unlock()
	go s.runMicrophone(ctx, generation, done)
}

func (s *Session) runMicrophone(ctx context.Context, generation uint64, done chan struct{}) {
	microphone := s.config.MicrophoneFactory()
	var encoder PacketEncoder
	defer func() {
		if microphone != nil {
			microphone.Stop()
		}
		if encoder != nil {
			_ = encoder.Close()
		}
		s.mu.Lock()
		if s.microphoneDone == done {
			s.microphone = nil
			s.encoder = nil
			s.microphoneCancel = nil
			s.microphoneDone = nil
			s.microphoneRunning = false
		}
		s.mu.Unlock()
		close(done)
	}()
	if microphone == nil {
		s.microphoneFailed(generation, "voice_microphone_unavailable")
		return
	}
	if err := microphone.Start(ctx); err != nil {
		s.microphoneFailed(generation, "voice_microphone_unavailable")
		return
	}
	var err error
	encoder, err = s.config.EncoderFactory()
	if err != nil {
		s.microphoneFailed(generation, "voice_encoder_unavailable")
		return
	}
	s.mu.Lock()
	if generation != s.generation || !s.microphoneEnabled {
		s.microphoneRunning = false
		s.mu.Unlock()
		return
	}
	s.microphone = microphone
	s.encoder = encoder
	s.state.Microphone = MicrophoneLive
	state := s.state
	s.mu.Unlock()
	s.emit(state)

	for {
		select {
		case <-ctx.Done():
			return
		case <-microphone.Errors():
			s.microphoneFailed(generation, "voice_microphone_unavailable")
			return
		case frame := <-microphone.Frames():
			packets, err := encoder.EncodePCM16(frame.Samples)
			if err != nil {
				s.microphoneFailed(generation, "voice_encoder_failed")
				return
			}
			s.mu.Lock()
			connection := s.connection
			current := generation == s.generation && s.microphoneEnabled
			s.mu.Unlock()
			if !current || connection == nil {
				continue
			}
			for _, packet := range packets {
				_ = connection.WriteOpus(packet, encoder.FrameDuration())
			}
		}
	}
}

func (s *Session) microphoneFailed(generation uint64, code string) {
	s.mu.Lock()
	if generation != s.generation || !s.microphoneEnabled {
		s.mu.Unlock()
		return
	}
	s.microphoneRunning = false
	s.state.Microphone = MicrophoneUnavailable
	s.state.ErrorCode = code
	state := s.state
	s.mu.Unlock()
	s.emit(state)
}

func (s *Session) runPlayback(ctx context.Context, generation uint64, mixer *audio.VoiceMixer) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			current := generation == s.generation && s.mixer == mixer
			s.mu.Unlock()
			if !current {
				return
			}
			samples, err := mixer.Mix()
			if err == nil {
				_ = s.config.Speaker.Write(windowsvoice.PCMFrame{Samples: samples})
			}
		}
	}
}

func (s *Session) runNetwork(ctx context.Context, generation uint64) {
	attempt := 0
	for {
		if ctx.Err() != nil || !s.isCurrent(generation) {
			return
		}
		token, err := s.issueToken(ctx)
		if err != nil {
			if s.applyTerminalError(generation, err) {
				return
			}
			s.setConnectionIfCurrent(generation, ConnectionReconnecting, "voice_token_unavailable", 0)
			if !s.waitBackoff(ctx, attempt) {
				return
			}
			attempt++
			continue
		}
		connection := s.config.ConnectionFactory(s.config.TokenIssuer.Profile())
		if connection == nil {
			err = errors.New("voice connection factory returned nil")
		} else {
			err = connection.Connect(ctx, token.Token)
		}
		if err != nil {
			if connection != nil {
				connection.Close()
			}
			if s.applyTerminalError(generation, err) {
				return
			}
			s.setConnectionIfCurrent(generation, ConnectionReconnecting, "voice_connect_failed", 0)
			if !s.waitBackoff(ctx, attempt) {
				return
			}
			attempt++
			continue
		}
		if !s.installConnection(generation, connection) {
			connection.Close()
			return
		}
		connected, terminal, code := s.serveConnection(ctx, generation, connection, token.ExpiresAtUnix)
		s.clearConnection(generation, connection)
		connection.Close()
		if terminal || ctx.Err() != nil || !s.isCurrent(generation) {
			return
		}
		if connected {
			attempt = 0
		}
		state := ConnectionReconnecting
		if code == "voice_room_full" {
			state = ConnectionFull
		}
		s.setConnectionIfCurrent(generation, state, code, 0)
		if !s.waitBackoff(ctx, attempt) {
			return
		}
		attempt++
	}
}

func (s *Session) serveConnection(ctx context.Context, generation uint64, connection Connection, expiresAt int64) (bool, bool, string) {
	ticker := time.NewTicker(s.config.RefreshInterval)
	defer ticker.Stop()
	connected := false
	streams := make(map[string]*remoteStream)
	defer s.closeRemoteStreams(streams)
	for {
		select {
		case <-ctx.Done():
			return connected, false, ""
		case <-connection.Done():
			return connected, false, "voice_connection_closed"
		case event := <-connection.Events():
			if !s.isCurrentConnection(generation, connection) {
				return connected, false, ""
			}
			if event.State == ConnectionUnauthorized {
				s.setConnectionIfCurrent(generation, ConnectionUnauthorized, firstNonEmpty(event.ErrorCode, "voice_unauthorized"), event.ParticipantCount)
				return connected, true, event.ErrorCode
			}
			if event.State == ConnectionFull {
				return connected, false, "voice_room_full"
			}
			if event.State == ConnectionError {
				return connected, false, firstNonEmpty(event.ErrorCode, "voice_connection_failed")
			}
			if event.State == ConnectionConnected {
				connected = true
			}
			s.setConnectionIfCurrent(generation, event.State, event.ErrorCode, event.ParticipantCount)
		case packet := <-connection.RemotePackets():
			s.handleRemotePacket(generation, packet, streams)
		case <-ticker.C:
			token, err := s.issueToken(ctx)
			if err != nil {
				if s.applyTerminalError(generation, err) {
					return connected, true, "voice_unauthorized"
				}
				if time.Now().Unix() >= expiresAt {
					s.setConnectionIfCurrent(generation, ConnectionError, "voice_token_expired", 0)
					return connected, true, "voice_token_expired"
				}
				continue
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = connection.RefreshAuth(refreshCtx, token.Token)
			cancel()
			if err != nil {
				if time.Now().Unix() >= expiresAt {
					s.setConnectionIfCurrent(generation, ConnectionError, "voice_token_expired", 0)
					return connected, true, "voice_token_expired"
				}
				continue
			}
			expiresAt = token.ExpiresAtUnix
		}
	}
}

type remoteStream struct {
	decoder     PacketDecoder
	initialized bool
	lastSeq     uint16
}

func (s *Session) handleRemotePacket(generation uint64, packet RemotePacket, streams map[string]*remoteStream) {
	if packet.TrackID == "" || !s.isCurrent(generation) {
		return
	}
	s.mu.Lock()
	mixer := s.mixer
	s.mu.Unlock()
	if mixer == nil {
		return
	}
	if packet.End {
		if stream := streams[packet.TrackID]; stream != nil {
			_ = stream.decoder.Close()
			delete(streams, packet.TrackID)
			mixer.RemoveStream(packet.TrackID)
		}
		return
	}
	stream := streams[packet.TrackID]
	if stream == nil {
		decoder, err := s.config.DecoderFactory()
		if err != nil || mixer.AddStream(packet.TrackID) != nil {
			if decoder != nil {
				_ = decoder.Close()
			}
			return
		}
		stream = &remoteStream{decoder: decoder}
		streams[packet.TrackID] = stream
	}
	if stream.initialized {
		missing := int(uint16(packet.SequenceNumber - stream.lastSeq - 1))
		if missing > 0 && missing < 100 {
			if missing == 1 {
				if samples, err := stream.decoder.DecodeFEC(packet.Payload); err == nil {
					_ = mixer.PushFrame(packet.TrackID, samples)
				}
			} else {
				for count := 0; count < min(missing, 3); count++ {
					if samples, err := stream.decoder.ConcealLoss(); err == nil {
						_ = mixer.PushFrame(packet.TrackID, samples)
					}
				}
			}
		}
	}
	if samples, err := stream.decoder.Decode(packet.Payload); err == nil {
		_ = mixer.PushFrame(packet.TrackID, samples)
	}
	stream.initialized = true
	stream.lastSeq = packet.SequenceNumber
}

func (s *Session) closeRemoteStreams(streams map[string]*remoteStream) {
	s.mu.Lock()
	mixer := s.mixer
	s.mu.Unlock()
	for trackID, stream := range streams {
		_ = stream.decoder.Close()
		if mixer != nil {
			mixer.RemoveStream(trackID)
		}
	}
}

func (s *Session) issueToken(ctx context.Context) (gamecast.VoiceTokenResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.config.TokenIssuer.IssueVoiceToken(requestCtx, gamecast.VoiceTokenRequest{
		Room:          s.config.RoomID,
		ParticipantID: s.config.ParticipantID,
	})
}

func (s *Session) installConnection(generation uint64, connection Connection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation || s.cancel == nil {
		return false
	}
	if s.connection != nil && s.connection != connection {
		return false
	}
	s.connection = connection
	return true
}

func (s *Session) clearConnection(generation uint64, connection Connection) {
	s.mu.Lock()
	if generation == s.generation && s.connection == connection {
		s.connection = nil
	}
	s.mu.Unlock()
}

func (s *Session) isCurrent(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return generation == s.generation && s.cancel != nil
}

func (s *Session) isCurrentConnection(generation uint64, connection Connection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return generation == s.generation && s.connection == connection
}

func (s *Session) setConnectionIfCurrent(generation uint64, connection ConnectionState, code string, count int) {
	s.mu.Lock()
	if generation != s.generation {
		s.mu.Unlock()
		return
	}
	s.state.Connection = connection
	if count >= 0 {
		s.state.ParticipantCount = count
	}
	if code != "" {
		s.state.ErrorCode = code
	} else if connection == ConnectionConnected {
		s.state.ErrorCode = ""
	}
	state := s.state
	s.mu.Unlock()
	s.emit(state)
}

func (s *Session) setErrorIfCurrent(generation uint64, code string) {
	s.mu.Lock()
	if generation != s.generation {
		s.mu.Unlock()
		return
	}
	s.state.ErrorCode = code
	state := s.state
	s.mu.Unlock()
	s.emit(state)
}

func (s *Session) applyTerminalError(generation uint64, err error) bool {
	state, code, terminal := classifyError(err)
	if terminal {
		s.setConnectionIfCurrent(generation, state, code, 0)
	}
	return terminal
}

func classifyError(err error) (ConnectionState, string, bool) {
	var apiError *gamecast.APIError
	if errors.As(err, &apiError) && (apiError.StatusCode == 401 || apiError.StatusCode == 403) {
		return ConnectionUnauthorized, firstNonEmpty(apiError.Code, "voice_unauthorized"), true
	}
	var connectError *ConnectError
	if errors.As(err, &connectError) {
		switch connectError.Code {
		case "voice_unauthorized", "voice_token_expired":
			return ConnectionUnauthorized, connectError.Code, true
		case "voice_room_full":
			return ConnectionFull, connectError.Code, false
		}
	}
	return ConnectionError, "voice_connection_failed", false
}

func (s *Session) waitBackoff(ctx context.Context, attempt int) bool {
	delays := s.config.BackoffDelays
	return s.config.Wait(ctx, delays[min(attempt, len(delays)-1)]) && ctx.Err() == nil
}

func waitDelay(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Session) emit(state State) {
	if s.config.OnState != nil {
		s.config.OnState(state)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ PacketEncoder = (*audio.OpusEncoder)(nil)
var _ PacketDecoder = (*audio.OpusDecoder)(nil)
