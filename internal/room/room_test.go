package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"PartyLAN/internal/network"
)

func TestRoomCreateAndLeave(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	if r.GetState() != RoomStateClosed {
		t.Error("initial state should be closed")
	}

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if r.GetState() != RoomStateWaiting {
		t.Errorf("expected state waiting, got %d", r.GetState())
	}

	if err := r.Leave(); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	if r.GetState() != RoomStateClosed {
		t.Errorf("expected state closed, got %d", r.GetState())
	}
}

func TestRoomSelfHostnameCanUseConfiguredDisplayName(t *testing.T) {
	r := NewRoomImpl(newTestNetwork())

	r.SetSelfHostname(" Player One ")

	if got := r.SelfHostname(); got != "Player One" {
		t.Fatalf("SelfHostname after configured display name = %q, want Player One", got)
	}

	r.SetSelfHostname(" ")

	if got := r.SelfHostname(); got != "Player One" {
		t.Fatalf("blank display name should not replace existing hostname, got %q", got)
	}
}

func TestRoomJoinAndLeave(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	if err := r.Join("room-id", ""); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if r.GetState() != RoomStateActive {
		t.Errorf("expected state active, got %d", r.GetState())
	}

	r.Leave()
}

func TestRoomDoubleCreate(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := r.Create(cfg)
	if err == nil {
		t.Error("expected error on double create")
	}

	r.Leave()
}

func TestRoomCallbacks(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	var joinCalled bool
	var joinPeer PeerInfo
	r.OnPeerJoin(func(peer PeerInfo) {
		joinCalled = true
		joinPeer = peer
	})

	var leaveCalled bool
	var leavePeerID string
	r.OnPeerLeave(func(peerID string) {
		leaveCalled = true
		leavePeerID = peerID
	})

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Connect a peer
	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Send join message
	joinPayload := RoomJoinPayload{
		PeerID:   "peer-join-1",
		RoomID:   "test-room",
		Hostname: "test-host",
	}
	WriteTypedMessage(conn, MsgRoomJoin, joinPayload)

	time.Sleep(100 * time.Millisecond)

	if !joinCalled {
		t.Error("OnPeerJoin not called")
	}
	if joinPeer.Hostname != "test-host" {
		t.Errorf("expected hostname 'test-host', got %q", joinPeer.Hostname)
	}

	// Close connection to trigger leave
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	if !leaveCalled {
		t.Error("OnPeerLeave not called")
	}
	if leavePeerID == "" {
		t.Error("expected non-empty peer ID")
	}

	r.Leave()
}

func TestRoomHeartbeat(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Connect and join
	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	joinPayload := RoomJoinPayload{PeerID: "peer-heartbeat-1", RoomID: "test-room", Hostname: "test-host"}
	WriteTypedMessage(conn, MsgRoomJoin, joinPayload)

	time.Sleep(100 * time.Millisecond)

	// Read peer announce first, then heartbeat
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	msg, err := ReadMessage(conn)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if msg.Type != MsgPeerAnnounce {
		t.Fatalf("expected peer announce first, got type %d", msg.Type)
	}

	msg, err = ReadMessage(conn)
	if err != nil {
		t.Fatalf("ReadMessage heartbeat: %v", err)
	}

	if msg.Type != MsgHeartbeat {
		t.Errorf("expected heartbeat, got type %d", msg.Type)
	}

	r.Leave()
}

func TestRoomGetPeers(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	if peers := r.GetPeers(); len(peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(peers))
	}

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Connect a peer
	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	joinPayload := RoomJoinPayload{PeerID: "peer-list-1", RoomID: "test-room", Hostname: "test-host"}
	WriteTypedMessage(conn, MsgRoomJoin, joinPayload)

	time.Sleep(100 * time.Millisecond)

	peers := r.GetPeers()
	if len(peers) != 1 {
		t.Errorf("expected 1 peer, got %d", len(peers))
	}

	var foundJoinedPeer bool
	for _, peer := range peers {
		if peer.Hostname == "test-host" {
			foundJoinedPeer = true
			break
		}
	}
	if !foundJoinedPeer {
		t.Error("expected joined peer to be returned by GetPeers")
	}

	conn.Close()
	r.Leave()
}

func TestRoomStreamStartStop(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	var streamStartCalled bool
	var streamStopCalled bool
	var streamCodec string
	var streamBackend string
	r.OnStreamStart(func(peerID string, width, height, fps int, codec, backend string) {
		streamStartCalled = true
		streamCodec = codec
		streamBackend = backend
	})
	r.OnStreamStop(func(peerID string) { streamStopCalled = true })

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Connect and join
	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	joinPayload := RoomJoinPayload{PeerID: "peer-stream-1", RoomID: "test-room", Hostname: "test-host"}
	WriteTypedMessage(conn, MsgRoomJoin, joinPayload)
	time.Sleep(100 * time.Millisecond)

	// Send stream start
	startPayload := StreamStartPayload{
		PeerID:  "test-peer",
		Width:   1920,
		Height:  1080,
		FPS:     30,
		Codec:   StreamCodecSoftwareDelta,
		Backend: "software-capture+software-encoder",
	}
	WriteTypedMessage(conn, MsgStreamStart, startPayload)
	time.Sleep(100 * time.Millisecond)

	if !streamStartCalled {
		t.Error("OnStreamStart not called")
	}
	if streamCodec != StreamCodecSoftwareDelta {
		t.Fatalf("expected codec %q, got %q", StreamCodecSoftwareDelta, streamCodec)
	}
	if streamBackend != "software-capture+software-encoder" {
		t.Fatalf("expected backend to round-trip, got %q", streamBackend)
	}

	// Send stream stop
	stopPayload := StreamStopPayload{PeerID: "test-peer"}
	WriteTypedMessage(conn, MsgStreamStop, stopPayload)
	time.Sleep(100 * time.Millisecond)

	if !streamStopCalled {
		t.Error("OnStreamStop not called")
	}

	conn.Close()
	r.Leave()
}

func TestNotifyPeerSharingTargetsSinglePeer(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Leave()

	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	joinPayload := RoomJoinPayload{PeerID: "peer-target-1", RoomID: "test-room", Hostname: "target-host"}
	if err := WriteTypedMessage(conn, MsgRoomJoin, joinPayload); err != nil {
		t.Fatalf("WriteTypedMessage join: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ReadMessage(conn); err != nil {
		t.Fatalf("read peer announce: %v", err)
	}

	if err := r.NotifyPeerSharing("peer-target-1", 1280, 720, 30, StreamCodecSoftwareDelta, "software-capture+software-encoder"); err != nil {
		t.Fatalf("NotifyPeerSharing: %v", err)
	}

	var msg *Message
	for i := 0; i < 12; i++ {
		msg, err = ReadMessage(conn)
		if err != nil {
			t.Fatalf("read post-notify message: %v", err)
		}
		if msg.Type == MsgStreamStart {
			break
		}
	}
	if msg == nil || msg.Type != MsgStreamStart {
		t.Fatalf("expected MsgStreamStart after notify, got %v", msg)
	}

	var payload StreamStartPayload
	if err := UnmarshalPayload(msg.Payload, &payload); err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	if payload.Width != 1280 || payload.Height != 720 || payload.FPS != 30 {
		t.Fatalf("unexpected payload dimensions: %+v", payload)
	}
	if payload.Codec != StreamCodecSoftwareDelta {
		t.Fatalf("expected codec %q, got %q", StreamCodecSoftwareDelta, payload.Codec)
	}
}

func TestSendMediaSignalTargetsSinglePeer(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Leave()

	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	joinPayload := RoomJoinPayload{PeerID: "peer-media-target-1", RoomID: "test-room", Hostname: "target-host"}
	if err := WriteTypedMessage(conn, MsgRoomJoin, joinPayload); err != nil {
		t.Fatalf("WriteTypedMessage join: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ReadMessage(conn); err != nil {
		t.Fatalf("read peer announce: %v", err)
	}

	signal := MediaSignalPayload{
		SessionID: "share-host-1",
		Kind:      "offer",
		Media:     "screen",
		Payload:   json.RawMessage(`{"type":"offer","sdp":"v=0"}`),
	}
	if err := r.SendMediaSignal("peer-media-target-1", signal); err != nil {
		t.Fatalf("SendMediaSignal: %v", err)
	}

	var msg *Message
	for i := 0; i < 12; i++ {
		msg, err = ReadMessage(conn)
		if err != nil {
			t.Fatalf("read post-signal message: %v", err)
		}
		if msg.Type == MsgMediaSignal {
			break
		}
	}
	if msg == nil || msg.Type != MsgMediaSignal {
		t.Fatalf("expected MsgMediaSignal after send, got %v", msg)
	}

	var payload MediaSignalPayload
	if err := UnmarshalPayload(msg.Payload, &payload); err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	if payload.FromPeerID != r.SelfID() {
		t.Fatalf("expected from peer %q, got %q", r.SelfID(), payload.FromPeerID)
	}
	if payload.ToPeerID != "peer-media-target-1" {
		t.Fatalf("expected target peer to round-trip, got %q", payload.ToPeerID)
	}
	if payload.Kind != "offer" || payload.Media != "screen" || payload.SessionID != "share-host-1" {
		t.Fatalf("unexpected signal payload: %+v", payload)
	}
}

func TestIncomingMediaSignalCallback(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	var got MediaSignalPayload
	done := make(chan struct{})
	r.OnMediaSignal(func(payload MediaSignalPayload) {
		got = payload
		close(done)
	})

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Leave()

	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	signal := MediaSignalPayload{
		SessionID:  "share-peer-1",
		FromPeerID: "peer-media-source-1",
		ToPeerID:   r.SelfID(),
		Kind:       "answer",
		Media:      "screen",
		Payload:    json.RawMessage(`{"type":"answer","sdp":"v=0"}`),
	}
	if err := WriteTypedMessage(conn, MsgMediaSignal, signal); err != nil {
		t.Fatalf("WriteTypedMessage signal: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnMediaSignal not called")
	}

	if got.SessionID != signal.SessionID || got.FromPeerID != signal.FromPeerID || got.Kind != signal.Kind {
		t.Fatalf("unexpected callback payload: %+v", got)
	}
}

func TestPeerAnnounceIsIdempotent(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	var joinCount int
	var lastPeer PeerInfo
	r.OnPeerJoin(func(peer PeerInfo) {
		joinCount++
		lastPeer = peer
	})

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	first := PeerAnnouncePayload{
		PeerID:   "peer-idempotent-1",
		Hostname: "host-a",
		Role:     int(PeerRoleViewer),
	}
	if err := WriteTypedMessage(conn, MsgPeerAnnounce, first); err != nil {
		t.Fatalf("WriteTypedMessage first: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	second := PeerAnnouncePayload{
		PeerID:   "peer-idempotent-1",
		Hostname: "host-b",
		Role:     int(PeerRoleViewer),
	}
	if err := WriteTypedMessage(conn, MsgPeerAnnounce, second); err != nil {
		t.Fatalf("WriteTypedMessage second: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	peers := r.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after duplicate announce, got %d", len(peers))
	}
	if peers[0].Hostname != "host-b" {
		t.Fatalf("expected hostname to update to host-b, got %q", peers[0].Hostname)
	}
	if joinCount != 1 {
		t.Fatalf("expected join callback once, got %d", joinCount)
	}
	if lastPeer.ID != "peer-idempotent-1" {
		t.Fatalf("expected peer ID peer-idempotent-1, got %q", lastPeer.ID)
	}

	r.Leave()
}

func TestPeerCapabilityIsCapturedFromJoinPayload(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Leave()

	conn, err := net.Dial(nil, "test-node", protocolPort)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	joinPayload := RoomJoinPayload{
		PeerID:   "peer-cap-1",
		RoomID:   "test-room",
		Hostname: "test-host",
		VideoCapability: &VideoCapabilityPayload{
			Backend:      "native",
			Status:       "supported",
			Reason:       "",
			SupportsHost: true,
			SupportsView: true,
		},
	}
	if err := WriteTypedMessage(conn, MsgRoomJoin, joinPayload); err != nil {
		t.Fatalf("WriteTypedMessage: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	peers := r.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].VideoBackend != "native" {
		t.Fatalf("expected video backend native, got %q", peers[0].VideoBackend)
	}
	if !peers[0].VideoSupportsView {
		t.Fatal("expected peer video decode support to be tracked")
	}
}

func TestConnectToPeerSendsJoinThenAnnounce(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newHandshakeNetwork()
	r := NewRoomImpl(net)

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Leave()

	msgCh := make(chan *Message, 2)
	errCh := make(chan error, 1)
	go func() {
		defer close(msgCh)
		for i := 0; i < 2; i++ {
			msg, err := ReadMessage(net.serverConn)
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	conn, err := r.ConnectToPeer("host-node")
	if err != nil {
		t.Fatalf("ConnectToPeer: %v", err)
	}
	defer conn.Close()

	var msgs []*Message
	for msg := range msgCh {
		msgs = append(msgs, msg)
	}

	select {
	case err := <-errCh:
		t.Fatalf("handshake reader: %v", err)
	default:
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 handshake messages, got %d", len(msgs))
	}

	msg1 := msgs[0]
	if msg1.Type != MsgRoomJoin {
		t.Fatalf("expected first message to be MsgRoomJoin, got %d", msg1.Type)
	}

	var joinPayload RoomJoinPayload
	if err := UnmarshalPayload(msg1.Payload, &joinPayload); err != nil {
		t.Fatalf("Unmarshal join payload: %v", err)
	}
	if joinPayload.PeerID != r.SelfID() {
		t.Fatalf("expected join peerId %q, got %q", r.SelfID(), joinPayload.PeerID)
	}
	if joinPayload.RoomID != "test-room" {
		t.Fatalf("expected roomId test-room, got %q", joinPayload.RoomID)
	}

	msg2 := msgs[1]
	if msg2.Type != MsgPeerAnnounce {
		t.Fatalf("expected second message to be MsgPeerAnnounce, got %d", msg2.Type)
	}

	var announcePayload PeerAnnouncePayload
	if err := UnmarshalPayload(msg2.Payload, &announcePayload); err != nil {
		t.Fatalf("Unmarshal announce payload: %v", err)
	}
	if announcePayload.PeerID != r.SelfID() {
		t.Fatalf("expected announce peerId %q, got %q", r.SelfID(), announcePayload.PeerID)
	}
}

func TestRoomLeaveDoesNotTriggerFatal(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newTestNetwork()
	r := NewRoomImpl(net)

	fatalCh := make(chan error, 1)
	r.OnFatal(func(err error) {
		fatalCh <- err
	})

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := r.Leave(); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	select {
	case err := <-fatalCh:
		t.Fatalf("unexpected fatal callback on normal leave: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRoomInvalidatesWhenProtocolListenerClosesUnexpectedly(t *testing.T) {
	restore := useUniqueRoomPorts(t)
	defer restore()
	restoreTiming := useFastRoomTiming(t)
	defer restoreTiming()

	net := newFailingRoomNetwork(errors.New("tsnet: use of closed network connection"))
	r := NewRoomImpl(net)

	fatalCh := make(chan error, 1)
	r.OnFatal(func(err error) {
		fatalCh <- err
	})

	cfg := RoomConfig{ID: "test-room", Name: "Test Room"}
	if err := r.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	select {
	case err := <-fatalCh:
		if err == nil {
			t.Fatal("expected fatal error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected fatal callback after protocol listener failure")
	}

	if got := r.GetState(); got != RoomStateClosed {
		t.Fatalf("expected room state closed after invalidation, got %d", got)
	}
}

// testNetwork implements network.Network for testing using real TCP
type testNetwork struct {
	nodeID string
}

func newTestNetwork() *testNetwork {
	return &testNetwork{nodeID: "test-node"}
}

func (n *testNetwork) Start(_ context.Context, _ string) error          { return nil }
func (n *testNetwork) Stop() error                                      { return nil }
func (n *testNetwork) GetNodeID() string                                { return n.nodeID }
func (n *testNetwork) GetIP() net.IP                                    { return net.ParseIP("127.0.0.1") }
func (n *testNetwork) ListNodes() []network.NodeInfo                    { return nil }
func (n *testNetwork) OnNodeStateChange(fn network.NodeStateChangeFunc) {}

func (n *testNetwork) Dial(_ context.Context, _ string, port int) (net.Conn, error) {
	return net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
}

func (n *testNetwork) Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
}

func (n *testNetwork) GetConnectionInfo(_ string) network.ConnectionInfo {
	return network.ConnectionInfo{}
}

func (n *testNetwork) GetRuntimeInfo() network.RuntimeInfo {
	return network.RuntimeInfo{}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

// Ensure testNetwork satisfies the interface
var _ network.Network = (*testNetwork)(nil)

type handshakeNetwork struct {
	serverConn net.Conn
	clientConn net.Conn
}

func newHandshakeNetwork() *handshakeNetwork {
	server, client := net.Pipe()
	return &handshakeNetwork{
		serverConn: server,
		clientConn: client,
	}
}

func (n *handshakeNetwork) Start(_ context.Context, _ string) error          { return nil }
func (n *handshakeNetwork) Stop() error                                      { return nil }
func (n *handshakeNetwork) GetNodeID() string                                { return "host-node" }
func (n *handshakeNetwork) GetIP() net.IP                                    { return net.ParseIP("127.0.0.1") }
func (n *handshakeNetwork) ListNodes() []network.NodeInfo                    { return nil }
func (n *handshakeNetwork) OnNodeStateChange(fn network.NodeStateChangeFunc) {}

func (n *handshakeNetwork) Dial(_ context.Context, _ string, _ int) (net.Conn, error) {
	return n.clientConn, nil
}

func (n *handshakeNetwork) Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
}

func (n *handshakeNetwork) GetConnectionInfo(_ string) network.ConnectionInfo {
	return network.ConnectionInfo{}
}

func (n *handshakeNetwork) GetRuntimeInfo() network.RuntimeInfo {
	return network.RuntimeInfo{}
}

var _ network.Network = (*handshakeNetwork)(nil)

type failingRoomNetwork struct {
	protocolErr error
	videoLn     net.Listener
	probeLn     net.Listener
}

func newFailingRoomNetwork(protocolErr error) *failingRoomNetwork {
	return &failingRoomNetwork{
		protocolErr: protocolErr,
		videoLn:     &blockingListener{closed: make(chan struct{})},
		probeLn:     &blockingListener{closed: make(chan struct{})},
	}
}

func (n *failingRoomNetwork) Start(_ context.Context, _ string) error          { return nil }
func (n *failingRoomNetwork) Stop() error                                      { return nil }
func (n *failingRoomNetwork) GetNodeID() string                                { return "failing-room-host" }
func (n *failingRoomNetwork) GetIP() net.IP                                    { return net.ParseIP("127.0.0.1") }
func (n *failingRoomNetwork) ListNodes() []network.NodeInfo                    { return nil }
func (n *failingRoomNetwork) OnNodeStateChange(fn network.NodeStateChangeFunc) {}
func (n *failingRoomNetwork) Dial(_ context.Context, _ string, _ int) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (n *failingRoomNetwork) Listen(port int) (net.Listener, error) {
	switch port {
	case protocolPort:
		return &failingListener{err: n.protocolErr}, nil
	case videoPort:
		return n.videoLn, nil
	case roomProbePort:
		return n.probeLn, nil
	default:
		return nil, fmt.Errorf("unexpected port %d", port)
	}
}
func (n *failingRoomNetwork) GetConnectionInfo(_ string) network.ConnectionInfo {
	return network.ConnectionInfo{}
}
func (n *failingRoomNetwork) GetRuntimeInfo() network.RuntimeInfo {
	return network.RuntimeInfo{}
}

var _ network.Network = (*failingRoomNetwork)(nil)

type failingListener struct {
	err error
}

func (l *failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *failingListener) Close() error              { return nil }
func (l *failingListener) Addr() net.Addr            { return dummyAddr("failing") }

type blockingListener struct {
	closed chan struct{}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}
func (l *blockingListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *blockingListener) Addr() net.Addr { return dummyAddr("blocking") }

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func useUniqueRoomPorts(t *testing.T) func() {
	t.Helper()

	oldProtocol := protocolPort
	oldVideo := videoPort
	oldProbe := roomProbePort

	protocolPort = mustFreePort(t)
	videoPort = mustFreePort(t)
	roomProbePort = mustFreePort(t)

	return func() {
		protocolPort = oldProtocol
		videoPort = oldVideo
		roomProbePort = oldProbe
	}
}

func useFastRoomTiming(t *testing.T) func() {
	t.Helper()

	oldHeartbeat := heartbeatTicker
	oldTimeout := peerTimeout

	heartbeatTicker = 10 * time.Millisecond
	peerTimeout = 50 * time.Millisecond

	return func() {
		heartbeatTicker = oldHeartbeat
		peerTimeout = oldTimeout
	}
}

func mustFreePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener addr type %T", ln.Addr())
	}
	return addr.Port
}
