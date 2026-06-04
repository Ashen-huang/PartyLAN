package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

const (
	tailscaleCLIPath = `C:\Program Files\Tailscale\tailscale.exe`
	roomProbePort    = 9002
	roomProbeRequest = "PING\n"
	roomProbePrefix  = "READY "
	roomProbeTimeout = 2500 * time.Millisecond
)

type TailscaleSystemNetwork struct {
	mu         sync.RWMutex
	self       NodeInfo
	peers      map[string]NodeInfo
	status     tailscaleStatus
	health     []string
	derpExpect DERPExpectation
}

type tailscaleStatus struct {
	BackendState string                         `json:"BackendState"`
	TailscaleIPs []string                       `json:"TailscaleIPs"`
	Self         tailscaleStatusPeer            `json:"Self"`
	Peer         map[string]tailscaleStatusPeer `json:"Peer"`
}

type tailscaleStatusPeer struct {
	ID           string   `json:"ID"`
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
	Active       bool     `json:"Active"`
}

func NewTailscaleSystemNetwork() *TailscaleSystemNetwork {
	return &TailscaleSystemNetwork{
		peers: make(map[string]NodeInfo),
	}
}

func (n *TailscaleSystemNetwork) Start(ctx context.Context, authKey string) error {
	n.mu.Lock()
	n.derpExpect = mergeDERPExpectation(n.derpExpect, derpExpectationFromEnv())
	n.mu.Unlock()
	_ = authKey
	if err := n.refreshStatus(ctx, true); err != nil {
		return err
	}
	log.Printf("[network/tailscale-system] started node=%s ip=%s state=%s", n.GetNodeID(), n.GetIP(), n.GetRuntimeInfo().BackendState)
	return nil
}

func (n *TailscaleSystemNetwork) Stop() error {
	return nil
}

func (n *TailscaleSystemNetwork) GetNodeID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.self.ID
}

func (n *TailscaleSystemNetwork) GetIP() net.IP {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.self.IP
}

func (n *TailscaleSystemNetwork) ListNodes() []NodeInfo {
	if err := n.refreshStatus(context.Background(), true); err != nil {
		return []NodeInfo{}
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	nodes := make([]NodeInfo, 0, len(n.peers))
	for _, peer := range n.peers {
		nodes = append(nodes, peer)
	}
	return nodes
}

func (n *TailscaleSystemNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	if err := n.refreshStatus(ctx, false); err != nil {
		return nil, err
	}

	n.mu.RLock()
	peer, ok := n.peers[nodeID]
	if !ok {
		for _, candidate := range n.peers {
			if candidate.Hostname == nodeID || (candidate.IP != nil && candidate.IP.String() == nodeID) {
				peer = candidate
				ok = true
				break
			}
		}
	}
	n.mu.RUnlock()
	if !ok || peer.IP == nil {
		if ip := parseDialTargetIP(nodeID); ip != nil {
			peer = NodeInfo{ID: nodeID, IP: ip}
			ok = true
		}
	}
	if !ok || peer.IP == nil {
		return nil, fmt.Errorf("tailscale peer %s not found", nodeID)
	}

	dialer := &net.Dialer{}
	addr := net.JoinHostPort(peer.IP.String(), strconv.Itoa(port))
	log.Printf("[network/tailscale-system] dial node=%s addr=%s", nodeID, addr)
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		log.Printf("[network/tailscale-system] dial node=%s addr=%s failed: %v", nodeID, addr, err)
		return nil, err
	}
	return conn, nil
}

func (n *TailscaleSystemNetwork) Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		log.Printf("[network/tailscale-system] listen port=%d failed: %v", port, err)
		return nil, err
	}
	log.Printf("[network/tailscale-system] listen port=%d addr=%s", port, ln.Addr())
	return ln, nil
}

func (n *TailscaleSystemNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	return ConnectionInfo{
		Type:    "tailscale-system",
		Latency: 0,
	}
}

func (n *TailscaleSystemNetwork) GetRuntimeInfo() RuntimeInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return RuntimeInfo{
		BackendState: n.status.BackendState,
		Health:       append([]string(nil), n.health...),
	}
}

func (n *TailscaleSystemNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	if fn == nil {
		return
	}

	for _, node := range n.ListNodes() {
		fn(node)
	}
}

func (n *TailscaleSystemNetwork) refreshStatus(ctx context.Context, probeJoinable bool) error {
	cmd := exec.CommandContext(ctx, tailscaleCLIPath, "status", "--json")
	hideCommandWindow(cmd)

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("tailscale status --json: %w", err)
	}

	var status tailscaleStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return fmt.Errorf("decode tailscale status: %w", err)
	}

	derpMap, localStatus, derpErr := systemTailscaleDiagnostics(ctx)
	health := append([]string(nil), derpExpectationHealth(n.derpExpectation(), derpMap)...)
	if derpErr != nil {
		health = append(health, "DERP diagnostics unavailable: "+derpErr.Error())
	}
	if derpMap != nil {
		health = append(health, summarizeDERPMap(derpMap))
	}
	if localStatus != nil {
		health = append(health, localStatus.Health...)
	}
	if localStatus != nil {
		for _, peer := range localStatus.Peer {
			if line := summarizePeerPath(peer); line != "" {
				health = append(health, line)
			}
		}
	}
	for _, line := range health {
		if strings.Contains(line, "custom DERP") || strings.Contains(line, "path=") {
			log.Printf("[network/derp] %s", line)
		}
	}

	self := nodeInfoFromStatusPeer(status.Self)
	peers := make(map[string]NodeInfo, len(status.Peer))
	for _, peer := range status.Peer {
		node := nodeInfoFromStatusPeer(peer)
		if node.ID == "" || node.IP == nil || !node.Online {
			continue
		}
		if probeJoinable {
			node.Joinable, node.RoomID = probeRoomTailscale(node.IP)
		}
		peers[node.ID] = node
	}

	n.mu.Lock()
	n.status = status
	n.self = self
	n.peers = peers
	n.health = health
	n.mu.Unlock()
	return nil
}

func (n *TailscaleSystemNetwork) SetDERPExpectation(expect DERPExpectation) {
	n.mu.Lock()
	n.derpExpect = expect
	n.mu.Unlock()
}

func (n *TailscaleSystemNetwork) derpExpectation() DERPExpectation {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.derpExpect
}

func systemTailscaleDiagnostics(ctx context.Context) (*tailcfg.DERPMap, *ipnstate.Status, error) {
	lc := &local.Client{}
	st, statusErr := lc.Status(ctx)
	derpMap, derpErr := lc.CurrentDERPMap(ctx)
	if statusErr != nil && derpErr != nil {
		return nil, nil, fmt.Errorf("status: %v; derpmap: %v", statusErr, derpErr)
	}
	if statusErr != nil {
		return derpMap, nil, fmt.Errorf("status: %w", statusErr)
	}
	if derpErr != nil {
		return nil, st, fmt.Errorf("derpmap: %w", derpErr)
	}
	return derpMap, st, nil
}

func nodeInfoFromStatusPeer(peer tailscaleStatusPeer) NodeInfo {
	var ip net.IP
	if len(peer.TailscaleIPs) > 0 {
		ip = net.ParseIP(peer.TailscaleIPs[0])
	}

	hostname := peer.HostName
	if hostname == "" {
		hostname = peer.DNSName
	}

	return NodeInfo{
		ID:       peer.ID,
		Hostname: hostname,
		IP:       ip,
		Online:   peer.Online || peer.Active,
		Latency:  0,
	}
}

func probeRoomTailscale(ip net.IP) (bool, string) {
	if ip == nil {
		return false, ""
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(roomProbePort)), roomProbeTimeout)
	if err != nil {
		log.Printf("[network/probe] target=%s port=%d dial failed: %v", ip, roomProbePort, err)
		return false, ""
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(roomProbeTimeout))
	if _, err := conn.Write([]byte(roomProbeRequest)); err != nil {
		log.Printf("[network/probe] target=%s port=%d write failed: %v", ip, roomProbePort, err)
		return false, ""
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("[network/probe] target=%s port=%d read failed: %v", ip, roomProbePort, err)
		return false, ""
	}

	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, roomProbePrefix) {
		log.Printf("[network/probe] target=%s port=%d unexpected response=%q", ip, roomProbePort, line)
		return false, ""
	}

	return true, strings.TrimSpace(strings.TrimPrefix(line, roomProbePrefix))
}

func hideCommandWindow(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
}

var _ Network = (*TailscaleSystemNetwork)(nil)
