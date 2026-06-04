package network

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	lanDiscoveryPort     = 9003
	lanBeaconInterval    = 1500 * time.Millisecond
	lanPeerStaleAfter    = 10 * time.Second
	lanProtocolVersion   = 1
	lanDiscoveryAppName  = "partylan"
	lanDiscoveryMaxBytes = 2048
)

type LANNetwork struct {
	mu sync.RWMutex

	nodeID        string
	hostname      string
	ip            net.IP
	discoveryPort int
	beaconEvery   time.Duration
	staleAfter    time.Duration

	conn    *net.UDPConn
	ctx     context.Context
	cancel  context.CancelFunc
	started bool

	peers   map[string]lanPeer
	stateFn NodeStateChangeFunc
}

type lanPeer struct {
	node     NodeInfo
	lastSeen time.Time
}

type lanBeacon struct {
	App       string `json:"app"`
	Version   int    `json:"version"`
	NodeID    string `json:"nodeId"`
	Hostname  string `json:"hostname"`
	IP        string `json:"ip"`
	ProbePort int    `json:"probePort"`
	SentAt    int64  `json:"sentAt"`
}

type lanOptions struct {
	NodeID        string
	Hostname      string
	IP            net.IP
	DiscoveryPort int
	BeaconEvery   time.Duration
	StaleAfter    time.Duration
}

func NewLANNetwork() *LANNetwork {
	hostname, _ := os.Hostname()
	return newLANNetworkWithOptions(lanOptions{
		NodeID:        makeLANNodeID(hostname),
		Hostname:      hostname,
		DiscoveryPort: lanDiscoveryPort,
		BeaconEvery:   lanBeaconInterval,
		StaleAfter:    lanPeerStaleAfter,
	})
}

func newLANNetworkWithOptions(opts lanOptions) *LANNetwork {
	hostname := strings.TrimSpace(opts.Hostname)
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	if hostname == "" {
		hostname = "partylan"
	}

	nodeID := strings.TrimSpace(opts.NodeID)
	if nodeID == "" {
		nodeID = makeLANNodeID(hostname)
	}

	discoveryPort := opts.DiscoveryPort
	if discoveryPort <= 0 {
		discoveryPort = lanDiscoveryPort
	}

	beaconEvery := opts.BeaconEvery
	if beaconEvery <= 0 {
		beaconEvery = lanBeaconInterval
	}

	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = lanPeerStaleAfter
	}

	var ip net.IP
	if opts.IP != nil {
		ip = append(net.IP(nil), opts.IP.To4()...)
	}

	return &LANNetwork{
		nodeID:        nodeID,
		hostname:      hostname,
		ip:            ip,
		discoveryPort: discoveryPort,
		beaconEvery:   beaconEvery,
		staleAfter:    staleAfter,
		peers:         make(map[string]lanPeer),
	}
}

func (n *LANNetwork) Start(ctx context.Context, authKey string) error {
	_ = ctx
	_ = authKey

	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return nil
	}
	if n.ip == nil {
		n.ip = primaryLANIPv4()
	}
	if n.ip == nil {
		n.ip = net.IPv4(127, 0, 0, 1)
	}
	n.mu.Unlock()

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: n.discoveryPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("listen LAN discovery UDP %d: %w", n.discoveryPort, err)
	}

	runCtx, cancel := context.WithCancel(context.Background())

	n.mu.Lock()
	n.conn = conn
	n.ctx = runCtx
	n.cancel = cancel
	n.started = true
	n.mu.Unlock()

	log.Printf("[network/lan] started node=%s host=%s ip=%s discovery_udp=%d",
		n.GetNodeID(), n.hostname, n.GetIP(), n.discoveryPort)

	go n.readLoop(runCtx, conn)
	go n.beaconLoop(runCtx, conn)
	return nil
}

func (n *LANNetwork) Stop() error {
	n.mu.Lock()
	cancel := n.cancel
	conn := n.conn
	n.cancel = nil
	n.conn = nil
	n.ctx = nil
	n.started = false
	n.peers = make(map[string]lanPeer)
	n.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (n *LANNetwork) GetNodeID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeID
}

func (n *LANNetwork) GetIP() net.IP {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.ip == nil {
		return nil
	}
	return append(net.IP(nil), n.ip...)
}

func (n *LANNetwork) ListNodes() []NodeInfo {
	now := time.Now()
	n.prunePeers(now)

	n.mu.RLock()
	nodes := make([]NodeInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		nodes = append(nodes, peer.node)
	}
	n.mu.RUnlock()

	for i := range nodes {
		nodes[i].Joinable, nodes[i].RoomID = probeRoomTailscale(nodes[i].IP)
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Hostname == nodes[j].Hostname {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].Hostname < nodes[j].Hostname
	})
	return nodes
}

func (n *LANNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	ip := n.lookupIP(nodeID)
	if ip == nil {
		ip = parseDialTargetIP(nodeID)
	}
	if ip == nil {
		return nil, fmt.Errorf("LAN peer %q not found", nodeID)
	}

	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	log.Printf("[network/lan] dial node=%s addr=%s", nodeID, addr)
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		log.Printf("[network/lan] dial node=%s addr=%s failed: %v", nodeID, addr, err)
		return nil, err
	}
	return conn, nil
}

func (n *LANNetwork) Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		log.Printf("[network/lan] listen port=%d failed: %v", port, err)
		return nil, err
	}
	log.Printf("[network/lan] listen port=%d addr=%s", port, ln.Addr())
	return ln, nil
}

func (n *LANNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	_ = nodeID
	return ConnectionInfo{Type: "lan"}
}

func (n *LANNetwork) GetRuntimeInfo() RuntimeInfo {
	n.mu.RLock()
	started := n.started
	port := n.discoveryPort
	ip := n.ip
	n.mu.RUnlock()

	state := "Stopped"
	if started {
		state = "Running"
	}

	health := []string{
		fmt.Sprintf("LAN discovery active on UDP %d", port),
		"LAN room probes use TCP 9002; room control uses TCP 9000; legacy video port uses TCP 9001",
	}
	if ip != nil {
		health = append(health, "LAN bind candidate "+ip.String())
	}

	return RuntimeInfo{
		BackendState: state,
		Health:       health,
	}
}

func (n *LANNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	n.mu.Lock()
	n.stateFn = fn
	nodes := make([]NodeInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		nodes = append(nodes, peer.node)
	}
	n.mu.Unlock()

	if fn == nil {
		return
	}
	for _, node := range nodes {
		fn(node)
	}
}

func (n *LANNetwork) readLoop(ctx context.Context, conn *net.UDPConn) {
	buf := make([]byte, lanDiscoveryMaxBytes)
	for {
		nRead, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				if isClosedConnErr(err) {
					return
				}
				log.Printf("[network/lan] discovery read failed: %v", err)
				continue
			}
		}

		var beacon lanBeacon
		if err := json.Unmarshal(buf[:nRead], &beacon); err != nil {
			continue
		}

		var remoteIP net.IP
		if addr != nil {
			remoteIP = addr.IP
		}
		node, changed := n.applyBeacon(beacon, remoteIP, time.Now())
		if changed {
			log.Printf("[network/lan] discovered node=%s host=%s ip=%s", node.ID, node.Hostname, node.IP)
			if fn := n.stateCallback(); fn != nil {
				fn(node)
			}
		}
	}
}

func (n *LANNetwork) beaconLoop(ctx context.Context, conn *net.UDPConn) {
	n.sendBeacon(conn)

	ticker := time.NewTicker(n.beaconEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.sendBeacon(conn)
			n.prunePeers(time.Now())
		}
	}
}

func (n *LANNetwork) sendBeacon(conn *net.UDPConn) {
	n.mu.RLock()
	beacon := lanBeacon{
		App:       lanDiscoveryAppName,
		Version:   lanProtocolVersion,
		NodeID:    n.nodeID,
		Hostname:  n.hostname,
		IP:        "",
		ProbePort: roomProbePort,
		SentAt:    time.Now().Unix(),
	}
	if n.ip != nil {
		beacon.IP = n.ip.String()
	}
	port := n.discoveryPort
	n.mu.RUnlock()

	payload, err := json.Marshal(beacon)
	if err != nil {
		return
	}

	for _, target := range lanBroadcastTargets(port) {
		if _, err := conn.WriteToUDP(payload, target); err != nil {
			log.Printf("[network/lan] discovery broadcast target=%s failed: %v", target, err)
		}
	}
}

func (n *LANNetwork) applyBeacon(beacon lanBeacon, remoteIP net.IP, seenAt time.Time) (NodeInfo, bool) {
	if beacon.App != lanDiscoveryAppName || beacon.Version != lanProtocolVersion || strings.TrimSpace(beacon.NodeID) == "" {
		return NodeInfo{}, false
	}

	n.mu.RLock()
	selfID := n.nodeID
	n.mu.RUnlock()
	if beacon.NodeID == selfID {
		return NodeInfo{}, false
	}

	var ip net.IP
	if remoteIP != nil && !remoteIP.IsUnspecified() && !remoteIP.IsLoopback() {
		ip = remoteIP
	}
	if ip == nil {
		ip = net.ParseIP(beacon.IP)
	}
	if ip == nil {
		return NodeInfo{}, false
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = append(net.IP(nil), ip4...)
	}

	hostname := strings.TrimSpace(beacon.Hostname)
	if hostname == "" {
		hostname = beacon.NodeID
	}

	node := NodeInfo{
		ID:       beacon.NodeID,
		Hostname: hostname,
		IP:       ip,
		Online:   true,
		Latency:  0,
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	prev, exists := n.peers[node.ID]
	changed := !exists ||
		prev.node.Hostname != node.Hostname ||
		!sameIP(prev.node.IP, node.IP) ||
		!prev.node.Online

	n.peers[node.ID] = lanPeer{
		node:     node,
		lastSeen: seenAt,
	}
	return node, changed
}

func (n *LANNetwork) prunePeers(now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for id, peer := range n.peers {
		if now.Sub(peer.lastSeen) > n.staleAfter {
			delete(n.peers, id)
		}
	}
}

func (n *LANNetwork) lookupIP(nodeID string) net.IP {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if peer, ok := n.peers[nodeID]; ok && peer.node.IP != nil {
		return append(net.IP(nil), peer.node.IP...)
	}
	for _, peer := range n.peers {
		if peer.node.Hostname == nodeID && peer.node.IP != nil {
			return append(net.IP(nil), peer.node.IP...)
		}
	}
	return nil
}

func (n *LANNetwork) stateCallback() NodeStateChangeFunc {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.stateFn
}

func makeLANNodeID(hostname string) string {
	name := sanitizeLANName(hostname)
	if name == "" {
		name = "partylan"
	}

	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.ToLower(hostname)))
	for _, hw := range interfaceHardwareAddrs() {
		_, _ = hash.Write([]byte(hw))
	}

	return fmt.Sprintf("lan-%s-%08x", name, hash.Sum32())
}

func sanitizeLANName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func interfaceHardwareAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.HardwareAddr == nil {
			continue
		}
		out = append(out, iface.HardwareAddr.String())
	}
	sort.Strings(out)
	return out
}

func primaryLANIPv4() net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var fallback net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := ipv4FromAddr(addr)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if isPrivateIPv4(ip) {
				return append(net.IP(nil), ip...)
			}
			if fallback == nil {
				fallback = append(net.IP(nil), ip...)
			}
		}
	}
	return fallback
}

func lanBroadcastTargets(port int) []*net.UDPAddr {
	targets := map[string]*net.UDPAddr{
		"255.255.255.255": {IP: net.IPv4bcast, Port: port},
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return mapValuesSorted(targets)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, mask := ipv4AndMaskFromAddr(addr)
			if ip == nil || mask == nil || ip.IsLoopback() {
				continue
			}
			broadcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				broadcast[i] = ip[i] | ^mask[i]
			}
			targets[broadcast.String()] = &net.UDPAddr{IP: broadcast, Port: port}
		}
	}

	return mapValuesSorted(targets)
}

func mapValuesSorted(values map[string]*net.UDPAddr) []*net.UDPAddr {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]*net.UDPAddr, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func ipv4FromAddr(addr net.Addr) net.IP {
	ip, _ := ipv4AndMaskFromAddr(addr)
	return ip
}

func ipv4AndMaskFromAddr(addr net.Addr) (net.IP, net.IPMask) {
	switch v := addr.(type) {
	case *net.IPNet:
		ip := v.IP.To4()
		if ip == nil {
			return nil, nil
		}
		return append(net.IP(nil), ip...), v.Mask
	case *net.IPAddr:
		ip := v.IP.To4()
		if ip == nil {
			return nil, nil
		}
		return append(net.IP(nil), ip...), nil
	default:
		return nil, nil
	}
}

func isPrivateIPv4(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168)
}

var _ Network = (*LANNetwork)(nil)
