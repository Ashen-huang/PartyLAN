package voice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/native/windowsvoice"
)

type fakeTokenIssuer struct {
	mu        sync.Mutex
	calls     int
	err       error
	expiresAt int64
}

func (i *fakeTokenIssuer) IssueVoiceToken(context.Context, gamecast.VoiceTokenRequest) (gamecast.VoiceTokenResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	if i.err != nil {
		return gamecast.VoiceTokenResponse{}, i.err
	}
	expiresAt := i.expiresAt
	if expiresAt <= 0 {
		expiresAt = time.Now().Add(15 * time.Second).Unix()
	}
	return gamecast.VoiceTokenResponse{OK: true, Token: "token", ExpiresAtUnix: expiresAt}, nil
}

func (i *fakeTokenIssuer) Profile() gamecast.Profile {
	return gamecast.Profile{BaseURL: "http://localhost"}
}

type fakeConnection struct {
	connectErr error
	events     chan ConnectionEvent
	packets    chan RemotePacket
	done       chan struct{}
	closeOnce  sync.Once
	refreshes  atomic.Int32
	writes     atomic.Int32
	refreshErr error
}

func newFakeConnection(connectErr error) *fakeConnection {
	return &fakeConnection{
		connectErr: connectErr,
		events:     make(chan ConnectionEvent, 8),
		packets:    make(chan RemotePacket, 8),
		done:       make(chan struct{}),
	}
}

func (c *fakeConnection) Connect(context.Context, string) error { return c.connectErr }
func (c *fakeConnection) Events() <-chan ConnectionEvent        { return c.events }
func (c *fakeConnection) RemotePackets() <-chan RemotePacket    { return c.packets }
func (c *fakeConnection) Done() <-chan struct{}                 { return c.done }
func (c *fakeConnection) WriteOpus([]byte, time.Duration) error {
	c.writes.Add(1)
	return nil
}
func (c *fakeConnection) RefreshAuth(context.Context, string) error {
	c.refreshes.Add(1)
	return c.refreshErr
}
func (c *fakeConnection) Close() { c.closeOnce.Do(func() { close(c.done) }) }

type fakeMicrophone struct {
	startErr error
	frames   chan windowsvoice.PCMFrame
	errors   chan error
	starts   atomic.Int32
	stops    atomic.Int32
}

func newFakeMicrophone(startErr error) *fakeMicrophone {
	return &fakeMicrophone{
		startErr: startErr,
		frames:   make(chan windowsvoice.PCMFrame, 4),
		errors:   make(chan error, 1),
	}
}

func (m *fakeMicrophone) Start(context.Context) error          { m.starts.Add(1); return m.startErr }
func (m *fakeMicrophone) Frames() <-chan windowsvoice.PCMFrame { return m.frames }
func (m *fakeMicrophone) Errors() <-chan error                 { return m.errors }
func (m *fakeMicrophone) Stop()                                { m.stops.Add(1) }

type fakeSpeaker struct {
	starts atomic.Int32
	stops  atomic.Int32
	writes atomic.Int32
	errors chan error
}

func newFakeSpeaker() *fakeSpeaker                       { return &fakeSpeaker{errors: make(chan error, 1)} }
func (s *fakeSpeaker) Start(context.Context) error       { s.starts.Add(1); return nil }
func (s *fakeSpeaker) Write(windowsvoice.PCMFrame) error { s.writes.Add(1); return nil }
func (s *fakeSpeaker) Errors() <-chan error              { return s.errors }
func (s *fakeSpeaker) Stop()                             { s.stops.Add(1) }

type fakeEncoder struct{}

func (e *fakeEncoder) EncodePCM16([]int16) ([][]byte, error) { return [][]byte{{1}}, nil }
func (e *fakeEncoder) FrameDuration() time.Duration          { return 20 * time.Millisecond }
func (e *fakeEncoder) Close() error                          { return nil }

func waitForState(t *testing.T, session *Session, predicate func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := session.Snapshot()
		if predicate(state) {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state := session.Snapshot()
	t.Fatalf("state timeout: %#v", state)
	return State{}
}

func baseConfig(issuer *fakeTokenIssuer, connection *fakeConnection, microphone *fakeMicrophone, speaker *fakeSpeaker) Config {
	return Config{
		RoomID:        "room",
		ParticipantID: "client",
		TokenIssuer:   issuer,
		ConnectionFactory: func(gamecast.Profile) Connection {
			return connection
		},
		MicrophoneFactory: func() windowsvoice.Microphone { return microphone },
		Speaker:           speaker,
		EncoderFactory: func() (PacketEncoder, error) {
			return &fakeEncoder{}, nil
		},
		RefreshInterval: 20 * time.Millisecond,
		BackoffDelays:   []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		Wait:            func(context.Context, time.Duration) bool { return true },
	}
}

func TestSessionConnectsWhenMicrophoneUnavailableAndKeepsPlaybackEnabled(t *testing.T) {
	issuer := &fakeTokenIssuer{}
	connection := newFakeConnection(nil)
	microphone := newFakeMicrophone(windowsvoice.ErrUnavailable)
	speaker := newFakeSpeaker()
	session := New(baseConfig(issuer, connection, microphone, speaker))
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	connection.events <- ConnectionEvent{State: ConnectionConnected, ParticipantCount: 3}
	state := waitForState(t, session, func(state State) bool {
		return state.Connection == ConnectionConnected && state.Microphone == MicrophoneUnavailable
	})
	if state.PlaybackMuted || state.ParticipantCount != 3 || speaker.starts.Load() != 1 {
		t.Fatalf("state=%#v speakerStarts=%d", state, speaker.starts.Load())
	}
}

func TestMicrophoneAndPlaybackControlsAreIndependent(t *testing.T) {
	issuer := &fakeTokenIssuer{}
	connection := newFakeConnection(nil)
	microphone := newFakeMicrophone(nil)
	speaker := newFakeSpeaker()
	session := New(baseConfig(issuer, connection, microphone, speaker))
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	connection.events <- ConnectionEvent{State: ConnectionConnected}
	waitForState(t, session, func(state State) bool { return state.Microphone == MicrophoneLive })
	state := session.SetPlaybackMuted(true)
	if !state.PlaybackMuted || state.Microphone != MicrophoneLive || microphone.stops.Load() != 0 {
		t.Fatalf("playback mute state=%#v microphoneStops=%d", state, microphone.stops.Load())
	}
	state = session.SetMicrophoneEnabled(false)
	if state.Microphone != MicrophoneMuted || !state.PlaybackMuted || microphone.stops.Load() != 1 {
		t.Fatalf("microphone mute state=%#v microphoneStops=%d", state, microphone.stops.Load())
	}
}

func TestSessionUsesBoundedReconnectBackoffAndRefreshesToken(t *testing.T) {
	issuer := &fakeTokenIssuer{}
	connections := []*fakeConnection{
		newFakeConnection(errors.New("temporary")),
		newFakeConnection(errors.New("temporary")),
		newFakeConnection(errors.New("temporary")),
		newFakeConnection(nil),
	}
	var factoryCalls atomic.Int32
	var waitsMu sync.Mutex
	waits := make([]time.Duration, 0)
	config := baseConfig(issuer, connections[0], newFakeMicrophone(nil), newFakeSpeaker())
	config.ConnectionFactory = func(gamecast.Profile) Connection {
		index := int(factoryCalls.Add(1)) - 1
		return connections[index]
	}
	config.Wait = func(_ context.Context, delay time.Duration) bool {
		waitsMu.Lock()
		waits = append(waits, delay)
		waitsMu.Unlock()
		return true
	}
	session := New(config)
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, session, func(State) bool { return factoryCalls.Load() == 4 })
	connections[3].events <- ConnectionEvent{State: ConnectionConnected}
	waitForState(t, session, func(state State) bool { return state.Connection == ConnectionConnected })
	deadline := time.Now().Add(time.Second)
	for connections[3].refreshes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	waitsMu.Lock()
	gotWaits := append([]time.Duration(nil), waits...)
	waitsMu.Unlock()
	if len(gotWaits) < 3 || gotWaits[0] != time.Second || gotWaits[1] != 2*time.Second || gotWaits[2] != 4*time.Second {
		t.Fatalf("backoff waits = %v", gotWaits)
	}
	if connections[3].refreshes.Load() == 0 {
		t.Fatal("voice token was not refreshed")
	}
}

func TestLateEventsFromOldGenerationCannotChangeNewSession(t *testing.T) {
	issuer := &fakeTokenIssuer{}
	first := newFakeConnection(nil)
	second := newFakeConnection(nil)
	var calls atomic.Int32
	config := baseConfig(issuer, first, newFakeMicrophone(nil), newFakeSpeaker())
	config.ConnectionFactory = func(gamecast.Profile) Connection {
		if calls.Add(1) == 1 {
			return first
		}
		return second
	}
	session := New(config)
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	waitForState(t, session, func(State) bool { return calls.Load() == 1 })
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	waitForState(t, session, func(State) bool { return calls.Load() == 2 })
	first.events <- ConnectionEvent{State: ConnectionConnected, ParticipantCount: 9}
	time.Sleep(20 * time.Millisecond)
	if state := session.Snapshot(); state.Connection == ConnectionConnected || state.ParticipantCount == 9 {
		t.Fatalf("late generation changed state: %#v", state)
	}
	second.events <- ConnectionEvent{State: ConnectionConnected, ParticipantCount: 2}
	state := waitForState(t, session, func(state State) bool { return state.Connection == ConnectionConnected })
	if state.ParticipantCount != 2 {
		t.Fatalf("new generation state = %#v", state)
	}
}

func TestFailedAuthRefreshExpiresAtLastAcceptedToken(t *testing.T) {
	issuer := &fakeTokenIssuer{expiresAt: time.Now().Add(150 * time.Millisecond).Unix()}
	connection := newFakeConnection(nil)
	connection.refreshErr = errors.New("refresh write failed")
	config := baseConfig(issuer, connection, newFakeMicrophone(nil), newFakeSpeaker())
	config.RefreshInterval = 10 * time.Millisecond
	session := New(config)
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	connection.events <- ConnectionEvent{State: ConnectionConnected}
	state := waitForState(t, session, func(state State) bool {
		return state.ErrorCode == "voice_token_expired"
	})
	if state.Connection != ConnectionError {
		t.Fatalf("expired refresh state = %#v", state)
	}
	if connection.refreshes.Load() == 0 {
		t.Fatal("auth refresh was not attempted")
	}
}
