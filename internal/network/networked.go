//go:build partylan_tsnet

package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type NetworkedNetwork struct {
	mu sync.RWMutex

	server     *tsnet.Server
	local      *local.Client
	ctx        context.Context
	cancel     context.CancelFunc
	started    bool
	nodeID     string
	ip         net.IP
	peers      map[string]NodeInfo
	lookup     map[string]net.IP
	stateFn    NodeStateChangeFunc
	lastStatus string
	authURL    string
	health     []string
	derpExpect DERPExpectation
	controlURL string
}

func NewNetworkedNetwork() *NetworkedNetwork {
	return &NetworkedNetwork{
		peers:  make(map[string]NodeInfo),
		lookup: make(map[string]net.IP),
	}
}

func (n *NetworkedNetwork) Start(ctx context.Context, authKey string) error {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return nil
	}
	n.mu.Unlock()

	if authKey == "" {
		authKey = os.Getenv("TS_AUTHKEY")
	}
	n.mu.Lock()
	n.derpExpect = mergeDERPExpectation(n.derpExpect, derpExpectationFromEnv())
	n.mu.Unlock()

	baseDir, err := os.UserConfigDir()
	if err != nil {
		baseDir = os.TempDir()
	}
	dir := filepath.Join(baseDir, "PartyLAN", "tsnet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create tsnet dir: %w", err)
	}

	hostname, _ := os.Hostname()
	srv := &tsnet.Server{
		Hostname:   sanitizeHostname("partylan-" + hostname),
		Dir:        dir,
		AuthKey:    authKey,
		Ephemeral:  false,
		ControlURL: n.tsControlURL(),
	}

	status, err := srv.Up(ctx)
	if err != nil {
		return fmt.Errorf("start tsnet server: %w", err)
	}

	lc, err := srv.LocalClient()
	if err != nil {
		_ = srv.Close()
		return fmt.Errorf("open tsnet local client: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())

	n.mu.Lock()
	n.server = srv
	n.local = lc
	n.ctx = runCtx
	n.cancel = cancel
	n.started = true
	n.mu.Unlock()

	if status != nil {
		n.applyStatus(status, nil, nil)
	}

	if _, err := n.refreshStatus(runCtx, true); err != nil {
		return fmt.Errorf("refresh tsnet status: %w", err)
	}

	log.Printf("[network/tsnet] started hostname=%q node=%s ip=%s dir=%q state=%s",
		srv.Hostname, n.GetNodeID(), n.GetIP(), dir, n.GetRuntimeInfo().BackendState)
	if srv.ControlURL != "" {
		log.Printf("[network/tsnet] control_url=%s", srv.ControlURL)
	}
	go n.pollLoop(runCtx)
	return nil
}

func (n *NetworkedNetwork) Stop() error {
	n.mu.Lock()
	if !n.started {
		n.mu.Unlock()
		return nil
	}
	cancel := n.cancel
	srv := n.server
	n.server = nil
	n.local = nil
	n.ctx = nil
	n.cancel = nil
	n.started = false
	n.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if srv != nil {
		return srv.Close()
	}
	return nil
}

func (n *NetworkedNetwork) GetNodeID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.nodeID != "" {
		return n.nodeID
	}
	if n.server != nil {
		return n.server.Hostname
	}
	return ""
}

func (n *NetworkedNetwork) GetIP() net.IP {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.ip != nil {
		return append(net.IP(nil), n.ip...)
	}
	return nil
}

func (n *NetworkedNetwork) ListNodes() []NodeInfo {
	if _, err := n.refreshStatus(context.Background(), true); err != nil {
		n.mu.RLock()
		defer n.mu.RUnlock()
		out := make([]NodeInfo, 0, len(n.peers))
		for _, peer := range n.peers {
			out = append(out, peer)
		}
		return out
	}

	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]NodeInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		out = append(out, peer)
	}
	return out
}

func (n *NetworkedNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	if _, err := n.refreshStatus(ctx, false); err != nil {
		return nil, err
	}

	n.mu.RLock()
	peerIP, ok := n.lookup[nodeID]
	server := n.server
	n.mu.RUnlock()

	if !ok || peerIP == nil {
		if ip := parseDialTargetIP(nodeID); ip != nil {
			peerIP = ip
			ok = true
		}
	}
	if !ok || peerIP == nil {
		return nil, fmt.Errorf("tailnet peer %q not found", nodeID)
	}
	if server == nil {
		return nil, fmt.Errorf("tsnet server not started")
	}

	addr := net.JoinHostPort(peerIP.String(), strconv.Itoa(port))
	log.Printf("[network/tsnet] dial node=%s addr=%s", nodeID, addr)
	conn, err := server.Dial(ctx, "tcp", addr)
	if err != nil {
		log.Printf("[network/tsnet] dial node=%s addr=%s failed: %v", nodeID, addr, err)
		return nil, err
	}
	return conn, nil
}

func (n *NetworkedNetwork) Listen(port int) (net.Listener, error) {
	n.mu.RLock()
	server := n.server
	n.mu.RUnlock()
	if server == nil {
		return nil, fmt.Errorf("tsnet server not started")
	}
	ln, err := server.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("[network/tsnet] listen port=%d failed: %v", port, err)
		return nil, err
	}
	log.Printf("[network/tsnet] listen port=%d addr=%s", port, ln.Addr())
	return ln, nil
}

func (n *NetworkedNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	peer, ok := n.peers[nodeID]
	if !ok {
		return ConnectionInfo{Type: "p2p"}
	}
	return ConnectionInfo{
		Type:    "p2p",
		Latency: peer.Latency,
	}
}

func (n *NetworkedNetwork) GetRuntimeInfo() RuntimeInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return RuntimeInfo{
		BackendState: n.lastStatus,
		AuthURL:      n.authURL,
		Health:       append([]string(nil), n.health...),
	}
}

func (n *NetworkedNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	n.mu.Lock()
	n.stateFn = fn
	n.mu.Unlock()
}

func (n *NetworkedNetwork) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if changed, err := n.refreshStatus(ctx, true); err == nil && changed {
				n.notifyNodes()
			}
		}
	}
}

func (n *NetworkedNetwork) refreshStatus(ctx context.Context, probeJoinable bool) (bool, error) {
	n.mu.RLock()
	lc := n.local
	server := n.server
	n.mu.RUnlock()
	if lc == nil {
		return false, fmt.Errorf("tsnet local client unavailable")
	}

	st, err := lc.Status(ctx)
	if err != nil {
		return false, err
	}

	derpMap, derpErr := lc.CurrentDERPMap(ctx)
	if derpErr != nil {
		derpMap = nil
	}

	n.applyStatus(st, derpMap, derpErr)

	selfNode := nodeInfoFromPeerStatus(st.Self)
	peers, lookup := buildPeerMaps(st)
	if probeJoinable {
		annotateJoinablePeers(ctx, serverDial(server), peers)
	}

	n.mu.Lock()
	changed := n.nodeID != selfNode.ID || !sameIP(n.ip, selfNode.IP) || !peerMapsEqual(n.peers, peers)
	n.nodeID = selfNode.ID
	n.ip = selfNode.IP
	n.peers = peers
	n.lookup = lookup
	n.mu.Unlock()

	return changed, nil
}

func (n *NetworkedNetwork) applyStatus(st *ipnstate.Status, derpMap *tailcfg.DERPMap, derpErr error) {
	if st == nil {
		return
	}

	health := append([]string(nil), derpExpectationHealth(n.derpExpectation(), derpMap)...)
	if controlURL := strings.TrimSpace(n.tsControlURL()); controlURL != "" {
		health = append(health, "tsnet control URL override configured: "+controlURL)
	}
	if derpErr != nil {
		health = append(health, "DERP diagnostics unavailable: "+derpErr.Error())
	}
	if derpMap != nil {
		health = append(health, summarizeDERPMap(derpMap))
	}
	health = append(health, st.Health...)
	for _, peer := range st.Peer {
		if line := summarizePeerPath(peer); line != "" {
			health = append(health, line)
		}
	}
	for _, line := range health {
		if strings.Contains(line, "custom DERP") || strings.Contains(line, "path=") {
			log.Printf("[network/derp] %s", line)
		}
	}

	n.mu.Lock()
	n.lastStatus = st.BackendState
	n.authURL = st.AuthURL
	n.health = health
	n.mu.Unlock()
}

func (n *NetworkedNetwork) SetDERPExpectation(expect DERPExpectation) {
	n.mu.Lock()
	n.derpExpect = expect
	n.mu.Unlock()
}

func (n *NetworkedNetwork) SetTSNetControlURL(controlURL string) {
	n.mu.Lock()
	n.controlURL = strings.TrimSpace(controlURL)
	n.mu.Unlock()
}

func (n *NetworkedNetwork) derpExpectation() DERPExpectation {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.derpExpect
}

func (n *NetworkedNetwork) tsControlURL() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.controlURL
}

func (n *NetworkedNetwork) notifyNodes() {
	n.mu.RLock()
	fn := n.stateFn
	nodes := make([]NodeInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		nodes = append(nodes, peer)
	}
	n.mu.RUnlock()

	if fn == nil {
		return
	}
	for _, node := range nodes {
		fn(node)
	}
}

func nodeInfoFromPeerStatus(peer *ipnstate.PeerStatus) NodeInfo {
	if peer == nil {
		return NodeInfo{}
	}

	var ip net.IP
	if len(peer.TailscaleIPs) > 0 {
		ip = net.ParseIP(peer.TailscaleIPs[0].String())
	}

	id := ""
	if ip != nil {
		id = ip.String()
	} else if peer.HostName != "" {
		id = peer.HostName
	} else {
		id = strings.TrimSuffix(peer.DNSName, ".")
	}

	return NodeInfo{
		ID:       id,
		Hostname: peer.HostName,
		IP:       ip,
		Online:   true,
		Latency:  0,
		Joinable: false,
		RoomID:   "",
	}
}

func buildPeerMaps(st *ipnstate.Status) (map[string]NodeInfo, map[string]net.IP) {
	peers := make(map[string]NodeInfo)
	lookup := make(map[string]net.IP)
	if st == nil {
		return peers, lookup
	}

	for _, peer := range st.Peer {
		if peer == nil {
			continue
		}
		node := nodeInfoFromPeerStatus(peer)
		if node.ID == "" {
			continue
		}

		if len(peer.TailscaleIPs) > 0 {
			node.IP = net.ParseIP(peer.TailscaleIPs[0].String())
			node.ID = node.IP.String()
		}

		node.Hostname = firstNonEmpty(peer.HostName, strings.TrimSuffix(peer.DNSName, "."))
		node.Online = true

		peers[node.ID] = node
		if node.IP != nil {
			lookup[node.ID] = node.IP
			if peer.HostName != "" {
				lookup[peer.HostName] = node.IP
			}
			if peer.DNSName != "" {
				lookup[strings.TrimSuffix(peer.DNSName, ".")] = node.IP
			}
		}
	}

	return peers, lookup
}

type networkedDialFunc func(context.Context, string, string) (net.Conn, error)

func serverDial(server *tsnet.Server) networkedDialFunc {
	if server == nil {
		return nil
	}
	return server.Dial
}

func annotateJoinablePeers(ctx context.Context, dial networkedDialFunc, peers map[string]NodeInfo) {
	if dial == nil || len(peers) == 0 {
		return
	}
	for id, node := range peers {
		if node.IP == nil {
			continue
		}
		node.Joinable, node.RoomID = probeRoomInfoNetworkedPair(ctx, dial, node.IP)
		peers[id] = node
	}
}

func peerMapsEqual(a, b map[string]NodeInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av.ID != bv.ID || av.Hostname != bv.Hostname || !sameIP(av.IP, bv.IP) || av.Joinable != bv.Joinable || av.RoomID != bv.RoomID {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func probeRoomInfoNetworkedPair(parent context.Context, dial networkedDialFunc, ip net.IP) (bool, string) {
	if ip == nil || dial == nil {
		return false, ""
	}

	if parent == nil {
		parent = context.Background()
	}

	probeCtx, cancel := context.WithTimeout(parent, roomProbeTimeout)
	defer cancel()

	conn, err := dial(probeCtx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(roomProbePort)))
	if err != nil {
		log.Printf("[network/probe] target=%s port=%d dial failed: %v", ip, roomProbePort, err)
		return false, ""
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(roomProbeTimeout))
	if _, err := conn.Write([]byte("PING\n")); err != nil {
		log.Printf("[network/probe] target=%s port=%d write failed: %v", ip, roomProbePort, err)
		return false, ""
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		log.Printf("[network/probe] target=%s port=%d read failed: %v", ip, roomProbePort, err)
		return false, ""
	}

	line := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(line, "READY ") {
		log.Printf("[network/probe] target=%s port=%d unexpected response=%q", ip, roomProbePort, line)
		return false, ""
	}

	return true, strings.TrimSpace(strings.TrimPrefix(line, "READY "))
}

var _ Network = (*NetworkedNetwork)(nil)
