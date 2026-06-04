package room

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/network"
	"PartyLAN/internal/video"
)

var (
	protocolPort     = 9000
	videoPort        = 9001
	roomProbePort    = 9002
	heartbeatTicker  = 5 * time.Second
	peerTimeout      = 60 * time.Second
	roomDialTimeout  = 8 * time.Second
	roomWriteTimeout = 3 * time.Second
)

type roomPeer struct {
	info      PeerInfo
	conn      net.Conn
	lastBeat  time.Time
	streaming bool
}

type RoomImpl struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	network  network.Network
	state    RoomState
	config   RoomConfig
	selfID   string
	selfHost string
	peers    map[string]*roomPeer

	listener      net.Listener
	videoListener net.Listener
	probeListener net.Listener
	ctx           context.Context
	cancel        context.CancelFunc

	onPeerJoin    func(peer PeerInfo)
	onPeerLeave   func(peerID string)
	onStreamStart func(peerID string, width, height, fps int, codec, backend string)
	onStreamStop  func(peerID string)
	onMediaSignal func(payload MediaSignalPayload)
	onFatal       func(error)
	fatalOnce     sync.Once

	localVideoCapability video.BackendCapability
}

func NewRoomImpl(net network.Network) *RoomImpl {
	hostname, _ := os.Hostname()
	return &RoomImpl{
		network:              net,
		state:                RoomStateClosed,
		selfID:               net.GetNodeID(),
		selfHost:             hostname,
		peers:                make(map[string]*roomPeer),
		localVideoCapability: video.NewNativeRuntimeBackend().Capability(),
	}
}

func (r *RoomImpl) Create(config RoomConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != RoomStateClosed {
		return fmt.Errorf("room already exists (state=%d)", r.state)
	}
	if err := r.refreshSelfIdentityLocked(); err != nil {
		return err
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())

	r.config = config
	r.state = RoomStateWaiting

	if err := r.startListening(); err != nil {
		r.state = RoomStateClosed
		return fmt.Errorf("start listening: %w", err)
	}
	log.Printf("[room/create] self=%s host=%s room=%s", r.selfID, r.selfHost, r.config.ID)

	go r.heartbeatLoop()
	go r.acceptLoop()
	go r.peerTimeoutLoop()
	go r.probeAcceptLoop()

	return nil
}

func (r *RoomImpl) Join(roomID string, password string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != RoomStateClosed {
		return fmt.Errorf("already in a room")
	}
	if err := r.refreshSelfIdentityLocked(); err != nil {
		return err
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.config = RoomConfig{ID: roomID, Password: password}

	if err := r.startListening(); err != nil {
		r.state = RoomStateClosed
		return fmt.Errorf("start listening: %w", err)
	}

	r.state = RoomStateActive
	log.Printf("[room/join] self=%s host=%s target=%s", r.selfID, r.selfHost, r.config.ID)

	go r.heartbeatLoop()
	go r.acceptLoop()
	go r.peerTimeoutLoop()
	go r.probeAcceptLoop()

	return nil
}

func (r *RoomImpl) Leave() error {
	r.mu.Lock()

	if r.state == RoomStateClosed {
		r.mu.Unlock()
		return nil
	}

	r.state = RoomStateClosed
	if r.cancel != nil {
		r.cancel()
	}

	if r.listener != nil {
		r.listener.Close()
	}
	if r.videoListener != nil {
		r.videoListener.Close()
	}
	if r.probeListener != nil {
		r.probeListener.Close()
	}

	for _, p := range r.peers {
		if p.conn != nil {
			p.conn.Close()
		}
	}
	r.peers = make(map[string]*roomPeer)

	r.mu.Unlock()
	return nil
}

func (r *RoomImpl) GetState() RoomState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *RoomImpl) GetPeers() []PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	peers := make([]PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p.info)
	}
	return peers
}

func (r *RoomImpl) StartSharing(width, height, fps int, codec, backend string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.state == RoomStateClosed {
		return fmt.Errorf("not in a room")
	}

	payload := StreamStartPayload{
		PeerID:  r.selfID,
		Width:   width,
		Height:  height,
		FPS:     fps,
		Codec:   codec,
		Backend: backend,
	}
	data, _ := MarshalPayload(payload)
	msg := Message{Type: MsgStreamStart, Payload: data}

	for _, p := range r.peers {
		if p.conn != nil {
			if err := r.writeMessageWithDeadline(p.conn, msg.Type, msg.Payload); err != nil {
				log.Printf("[room/control] stream start write failed peer=%s: %v", p.info.ID, err)
			}
		}
	}
	return nil
}

func (r *RoomImpl) StopSharing() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.state == RoomStateClosed {
		return fmt.Errorf("not in a room")
	}

	payload := StreamStopPayload{PeerID: r.selfID}
	data, _ := MarshalPayload(payload)
	msg := Message{Type: MsgStreamStop, Payload: data}

	for _, p := range r.peers {
		if p.conn != nil {
			if err := r.writeMessageWithDeadline(p.conn, msg.Type, msg.Payload); err != nil {
				log.Printf("[room/control] stream stop write failed peer=%s: %v", p.info.ID, err)
			}
		}
	}
	return nil
}

func (r *RoomImpl) NotifyPeerSharing(peerID string, width, height, fps int, codec, backend string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.state == RoomStateClosed {
		return fmt.Errorf("not in a room")
	}

	peer, ok := r.peers[peerID]
	if !ok || peer.conn == nil {
		return fmt.Errorf("peer %s is not connected", peerID)
	}

	payload := StreamStartPayload{
		PeerID:  r.selfID,
		Width:   width,
		Height:  height,
		FPS:     fps,
		Codec:   codec,
		Backend: backend,
	}
	data, err := MarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("marshal stream start payload: %w", err)
	}

	if err := r.writeMessageWithDeadline(peer.conn, MsgStreamStart, data); err != nil {
		return fmt.Errorf("write stream start to peer %s: %w", peerID, err)
	}
	return nil
}

func (r *RoomImpl) SendMediaSignal(peerID string, payload MediaSignalPayload) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.state == RoomStateClosed {
		return fmt.Errorf("not in a room")
	}
	if peerID == "" {
		return fmt.Errorf("media signal target peer is required")
	}

	peer, ok := r.peers[peerID]
	if !ok || peer.conn == nil {
		return fmt.Errorf("peer %s is not connected", peerID)
	}

	if payload.FromPeerID == "" {
		payload.FromPeerID = r.selfID
	}
	if payload.ToPeerID == "" {
		payload.ToPeerID = peerID
	}

	data, err := MarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("marshal media signal payload: %w", err)
	}
	if err := r.writeMessageWithDeadline(peer.conn, MsgMediaSignal, data); err != nil {
		return fmt.Errorf("write media signal to peer %s: %w", peerID, err)
	}
	return nil
}

func (r *RoomImpl) OnPeerJoin(fn func(peer PeerInfo))  { r.onPeerJoin = fn }
func (r *RoomImpl) OnPeerLeave(fn func(peerID string)) { r.onPeerLeave = fn }
func (r *RoomImpl) OnStreamStart(fn func(peerID string, width, height, fps int, codec, backend string)) {
	r.onStreamStart = fn
}
func (r *RoomImpl) OnStreamStop(fn func(peerID string)) { r.onStreamStop = fn }
func (r *RoomImpl) OnFatal(fn func(error))              { r.onFatal = fn }
func (r *RoomImpl) OnMediaSignal(fn func(payload MediaSignalPayload)) {
	r.onMediaSignal = fn
}

func (r *RoomImpl) startListening() error {
	ln, err := r.network.Listen(protocolPort)
	if err != nil {
		return fmt.Errorf("listen protocol port %d: %w", protocolPort, err)
	}
	r.listener = ln
	log.Printf("[room/listen] protocol port=%d addr=%s", protocolPort, ln.Addr())

	vln, err := r.network.Listen(videoPort)
	if err != nil {
		ln.Close()
		return fmt.Errorf("listen video port %d: %w", videoPort, err)
	}
	r.videoListener = vln
	log.Printf("[room/listen] video port=%d addr=%s", videoPort, vln.Addr())

	pln, err := r.network.Listen(roomProbePort)
	if err != nil {
		ln.Close()
		vln.Close()
		return fmt.Errorf("listen room probe port %d: %w", roomProbePort, err)
	}
	r.probeListener = pln
	log.Printf("[room/listen] probe port=%d addr=%s", roomProbePort, pln.Addr())

	return nil
}

func (r *RoomImpl) acceptLoop() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			select {
			case <-r.ctx.Done():
				return
			default:
				if r.isShuttingDown() {
					return
				}
				if isClosedConnErr(err) {
					r.invalidate(fmt.Errorf("room protocol listener closed unexpectedly: %w", err))
					return
				}
				log.Printf("accept error: %v", err)
				continue
			}
		}
		go r.handleConn(conn)
	}
}

func (r *RoomImpl) probeAcceptLoop() {
	for {
		conn, err := r.probeListener.Accept()
		if err != nil {
			select {
			case <-r.ctx.Done():
				return
			default:
				if r.isShuttingDown() {
					return
				}
				if isClosedConnErr(err) {
					r.invalidate(fmt.Errorf("room probe listener closed unexpectedly: %w", err))
					return
				}
				log.Printf("probe accept error: %v", err)
				continue
			}
		}
		go r.handleProbeConn(conn)
	}
}

func (r *RoomImpl) handleProbeConn(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 16)
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	if string(buf[:n]) != "PING\n" {
		return
	}

	r.mu.RLock()
	roomID := r.config.ID
	state := r.state
	r.mu.RUnlock()

	if state == RoomStateClosed || roomID == "" {
		return
	}

	log.Printf("[room/probe] ready room=%s remote=%s", roomID, conn.RemoteAddr())
	_, _ = conn.Write([]byte(fmt.Sprintf("READY %s\n", roomID)))
}

func (r *RoomImpl) handleConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("handleConn panic: %v", r)
		}
	}()

	for {
		msg, err := ReadMessage(conn)
		if err != nil {
			log.Printf("[room/control] read failed remote=%s: %v", conn.RemoteAddr(), err)
			r.removePeerByConn(conn)
			conn.Close()
			return
		}
		r.handleMessage(conn, msg)
	}
}

func (r *RoomImpl) handleMessage(conn net.Conn, msg *Message) {
	switch msg.Type {
	case MsgRoomJoin:
		var p RoomJoinPayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		if p.PeerID == "" {
			return
		}
		peer, isNew := r.upsertPeerByIDLocked(p.PeerID, p.Hostname, conn, p.VideoCapability)
		r.updatePeerHeartbeat(p.PeerID)
		if isNew {
			r.notifyPeerJoin(peer)
		}
		if p.PeerID != r.selfID {
			_ = r.sendPeerAnnounce(conn, PeerInfo{
				ID:       r.selfID,
				Hostname: r.selfHost,
				Role:     PeerRoleHost,
			})
		}

	case MsgPeerAnnounce:
		var p PeerAnnouncePayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		peer, isNew := r.upsertPeerByIDLocked(p.PeerID, p.Hostname, conn, p.VideoCapability)
		r.updatePeerHeartbeat(p.PeerID)
		if isNew {
			r.notifyPeerJoin(peer)
		}

	case MsgHeartbeat:
		var p HeartbeatPayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		r.updatePeerHeartbeat(p.PeerID)

	case MsgStreamStart:
		var p StreamStartPayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		r.updatePeerHeartbeat(p.PeerID)
		r.setPeerStreaming(p.PeerID, true)
		if r.onStreamStart != nil {
			r.onStreamStart(p.PeerID, p.Width, p.Height, p.FPS, p.Codec, p.Backend)
		}

	case MsgStreamStop:
		var p StreamStopPayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		r.updatePeerHeartbeat(p.PeerID)
		r.setPeerStreaming(p.PeerID, false)
		if r.onStreamStop != nil {
			r.onStreamStop(p.PeerID)
		}

	case MsgMediaSignal:
		var p MediaSignalPayload
		if err := UnmarshalPayload(msg.Payload, &p); err != nil {
			return
		}
		if p.FromPeerID == "" || p.ToPeerID == "" || p.Kind == "" {
			return
		}
		r.updatePeerHeartbeat(p.FromPeerID)
		if r.onMediaSignal != nil {
			r.onMediaSignal(p)
		}
	}
}

func (r *RoomImpl) upsertPeerByIDLocked(peerID, hostname string, conn net.Conn, cap *VideoCapabilityPayload) (PeerInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.peers[peerID]; ok {
		existing.info.Hostname = hostname
		existing.conn = conn
		existing.lastBeat = time.Now()
		applyVideoCapabilityPayload(&existing.info, cap)
		return existing.info, false
	}

	info := PeerInfo{
		ID:           peerID,
		Hostname:     hostname,
		Role:         PeerRoleViewer,
		JoinedAtUnix: time.Now().Unix(),
	}
	applyVideoCapabilityPayload(&info, cap)
	r.peers[peerID] = &roomPeer{
		info:     info,
		conn:     conn,
		lastBeat: time.Now(),
	}
	return info, true
}

func (r *RoomImpl) removePeerByConn(conn net.Conn) {
	r.mu.Lock()
	var removedPeer string
	for id, p := range r.peers {
		if p.conn == conn {
			removedPeer = id
			delete(r.peers, id)
			break
		}
	}
	r.mu.Unlock()

	if removedPeer != "" && r.onPeerLeave != nil {
		log.Printf("[room/control] peer removed peer=%s remote=%s", removedPeer, conn.RemoteAddr())
		r.onPeerLeave(removedPeer)
	}
}

func (r *RoomImpl) updatePeerHeartbeat(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.peers[peerID]; ok {
		p.lastBeat = time.Now()
	}
}

func (r *RoomImpl) setPeerStreaming(peerID string, streaming bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.peers[peerID]; ok {
		p.streaming = streaming
		p.info.IsSharing = streaming
	}
}

func (r *RoomImpl) refreshSelfIdentityLocked() error {
	nodeID := strings.TrimSpace(r.network.GetNodeID())
	if nodeID == "" {
		return fmt.Errorf("network node id is unavailable; wait for network startup before creating or joining a room")
	}
	r.selfID = nodeID
	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" && r.selfHost == "" {
		r.selfHost = hostname
	}
	return nil
}

func (r *RoomImpl) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatTicker)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.sendHeartbeats()
		}
	}
}

func (r *RoomImpl) sendHeartbeats() {
	r.mu.RLock()
	payload := HeartbeatPayload{
		PeerID:    r.selfID,
		Timestamp: time.Now().UnixMilli(),
	}
	data, _ := MarshalPayload(payload)
	msgType := MsgHeartbeat
	conns := make([]net.Conn, 0, len(r.peers))
	for _, p := range r.peers {
		if p.conn != nil {
			conns = append(conns, p.conn)
		}
	}
	r.mu.RUnlock()

	for _, conn := range conns {
		if err := r.writeMessageWithDeadline(conn, msgType, data); err != nil {
			log.Printf("[room/control] heartbeat write failed remote=%s: %v", conn.RemoteAddr(), err)
		}
	}
}

func (r *RoomImpl) peerTimeoutLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.checkPeerTimeouts()
		}
	}
}

func (r *RoomImpl) checkPeerTimeouts() {
	r.mu.Lock()
	now := time.Now()
	var timedOut []string
	for id, p := range r.peers {
		if now.Sub(p.lastBeat) > peerTimeout {
			timedOut = append(timedOut, id)
			if p.conn != nil {
				p.conn.Close()
			}
			delete(r.peers, id)
		}
	}
	r.mu.Unlock()

	for _, id := range timedOut {
		log.Printf("[room/control] peer timeout peer=%s timeout=%s", id, peerTimeout)
		r.notifyPeerLeave(id)
	}
}

func (r *RoomImpl) VideoListener() net.Listener {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.videoListener
}

func (r *RoomImpl) ConnectToPeer(nodeID string) (net.Conn, error) {
	conn, err := r.dialPeer(nodeID, protocolPort)
	if err != nil {
		return nil, fmt.Errorf("dial peer %s: %w", nodeID, err)
	}

	_ = conn.SetDeadline(time.Now().Add(roomDialTimeout))
	if err := r.sendRoomJoin(conn); err != nil {
		conn.Close()
		return nil, err
	}

	if err := r.sendPeerAnnouncement(conn); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})

	peer, isNew := r.upsertPeerByIDLocked(nodeID, "", conn, nil)
	if isNew {
		r.notifyPeerJoin(peer)
	}

	go r.handleConn(conn)
	return conn, nil
}

func (r *RoomImpl) ConnectVideoToPeer(nodeID string) (net.Conn, error) {
	conn, err := r.dialPeer(nodeID, videoPort)
	if err != nil {
		return nil, fmt.Errorf("dial peer video %s: %w", nodeID, err)
	}
	return conn, nil
}

func (r *RoomImpl) dialPeer(nodeID string, port int) (net.Conn, error) {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, cancel := context.WithTimeout(ctx, roomDialTimeout)
	defer cancel()

	log.Printf("[room/dial] peer=%s port=%d timeout=%s", nodeID, port, roomDialTimeout)
	conn, err := r.network.Dial(dialCtx, nodeID, port)
	if err != nil {
		log.Printf("[room/dial] peer=%s port=%d failed: %v", nodeID, port, err)
		return nil, err
	}
	log.Printf("[room/dial] peer=%s port=%d connected local=%s remote=%s", nodeID, port, conn.LocalAddr(), conn.RemoteAddr())
	return conn, nil
}

func (r *RoomImpl) Network() network.Network {
	return r.network
}

func (r *RoomImpl) SelfID() string {
	return r.selfID
}

func (r *RoomImpl) SelfHostname() string {
	return r.selfHost
}

func (r *RoomImpl) SetSelfHostname(hostname string) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return
	}

	r.mu.Lock()
	r.selfHost = hostname
	r.mu.Unlock()
}

func (r *RoomImpl) sendPeerAnnounce(conn net.Conn, peer PeerInfo) error {
	payload := PeerAnnouncePayload{
		PeerID:          peer.ID,
		Hostname:        peer.Hostname,
		Role:            int(peer.Role),
		VideoCapability: r.localVideoCapabilityPayload(),
	}
	return r.writeTypedMessageWithDeadline(conn, MsgPeerAnnounce, payload)
}

func (r *RoomImpl) sendRoomJoin(conn net.Conn) error {
	payload := RoomJoinPayload{
		PeerID:          r.selfID,
		RoomID:          r.config.ID,
		Hostname:        r.selfHost,
		VideoCapability: r.localVideoCapabilityPayload(),
	}
	if err := r.writeTypedMessageWithDeadline(conn, MsgRoomJoin, payload); err != nil {
		return fmt.Errorf("join peer room: %w", err)
	}
	return nil
}

func (r *RoomImpl) sendPeerAnnouncement(conn net.Conn) error {
	payload := PeerAnnouncePayload{
		PeerID:          r.selfID,
		Hostname:        r.selfHost,
		Role:            int(PeerRoleViewer),
		VideoCapability: r.localVideoCapabilityPayload(),
	}
	if err := r.writeTypedMessageWithDeadline(conn, MsgPeerAnnounce, payload); err != nil {
		return fmt.Errorf("announce to peer: %w", err)
	}
	return nil
}

func (r *RoomImpl) writeTypedMessageWithDeadline(conn net.Conn, msgType MessageType, payload interface{}) error {
	data, err := MarshalPayload(payload)
	if err != nil {
		return err
	}
	return r.writeMessageWithDeadline(conn, msgType, data)
}

func (r *RoomImpl) writeMessageWithDeadline(conn net.Conn, msgType MessageType, payload []byte) error {
	if conn == nil {
		return fmt.Errorf("connection is nil")
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(roomWriteTimeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	err := WriteMessage(conn, msgType, payload)
	if clearErr := conn.SetWriteDeadline(time.Time{}); clearErr != nil && err == nil {
		err = fmt.Errorf("clear write deadline: %w", clearErr)
	}
	return err
}

func (r *RoomImpl) notifyPeerJoin(peer PeerInfo) {
	if r.onPeerJoin != nil {
		r.onPeerJoin(peer)
	}
}

func (r *RoomImpl) notifyPeerLeave(peerID string) {
	if r.onPeerLeave != nil {
		r.onPeerLeave(peerID)
	}
}

func (r *RoomImpl) invalidate(err error) {
	if err == nil {
		return
	}

	r.fatalOnce.Do(func() {
		log.Printf("room invalidated: %v", err)
		_ = r.Leave()
		if r.onFatal != nil {
			r.onFatal(err)
		}
	})
}

func (r *RoomImpl) isShuttingDown() bool {
	r.mu.RLock()
	state := r.state
	ctx := r.ctx
	r.mu.RUnlock()
	return state == RoomStateClosed || ctx == nil || ctx.Err() != nil
}

func isClosedConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}

func (r *RoomImpl) localVideoCapabilityPayload() *VideoCapabilityPayload {
	return &VideoCapabilityPayload{
		Backend:      string(r.localVideoCapability.Backend),
		Status:       string(r.localVideoCapability.Status),
		Reason:       r.localVideoCapability.Reason,
		SupportsHost: r.localVideoCapability.SupportsHost,
		SupportsView: r.localVideoCapability.SupportsView,
	}
}

func applyVideoCapabilityPayload(info *PeerInfo, cap *VideoCapabilityPayload) {
	if info == nil || cap == nil {
		return
	}
	info.VideoBackend = cap.Backend
	info.VideoStatus = cap.Status
	info.VideoReason = cap.Reason
	info.VideoSupportsHost = cap.SupportsHost
	info.VideoSupportsView = cap.SupportsView
}
