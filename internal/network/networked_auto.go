package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

type AutoNetworkedNetwork struct {
	mu         sync.RWMutex
	system     Network
	tsnet      Network
	active     Network
	backend    string
	stateFn    NodeStateChangeFunc
	derpExpect DERPExpectation
}

func NewAutoNetworkedNetwork() *AutoNetworkedNetwork {
	return &AutoNetworkedNetwork{
		system: NewTailscaleSystemNetwork(),
		tsnet:  NewNetworkedNetwork(),
	}
}

func (n *AutoNetworkedNetwork) Start(ctx context.Context, authKey string) error {
	n.mu.Lock()
	if n.active != nil {
		n.mu.Unlock()
		return nil
	}
	stateFn := n.stateFn
	n.mu.Unlock()

	n.mu.Lock()
	n.derpExpect = mergeDERPExpectation(n.derpExpect, derpExpectationFromEnv())
	expect := n.derpExpect
	n.mu.Unlock()

	candidates := n.candidateOrder()
	var errs []string
	for _, candidate := range candidates {
		backendName, backend := n.backendFor(candidate)
		if backend == nil {
			continue
		}
		if setter, ok := backend.(DERPExpectationSetter); ok {
			setter.SetDERPExpectation(expect)
		}

		if err := backend.Start(ctx, authKey); err != nil {
			errs = append(errs, fmt.Sprintf("%s start failed: %v", backendName, err))
			continue
		}
		if !networkReady(backend) {
			_ = backend.Stop()
			errs = append(errs, fmt.Sprintf("%s not ready (state=%s)", backendName, backend.GetRuntimeInfo().BackendState))
			continue
		}

		if stateFn != nil {
			backend.OnNodeStateChange(stateFn)
		}

		n.mu.Lock()
		n.active = backend
		n.backend = backendName
		n.mu.Unlock()
		return nil
	}

	if len(errs) == 0 {
		return fmt.Errorf("no network backend candidates configured")
	}
	return fmt.Errorf("networked startup failed: %s", strings.Join(errs, "; "))
}

func (n *AutoNetworkedNetwork) Stop() error {
	n.mu.Lock()
	active := n.active
	n.active = nil
	n.backend = ""
	n.mu.Unlock()

	if active == nil {
		return nil
	}
	return active.Stop()
}

func (n *AutoNetworkedNetwork) GetNodeID() string {
	if active := n.getActive(); active != nil {
		return active.GetNodeID()
	}
	return ""
}

func (n *AutoNetworkedNetwork) GetIP() net.IP {
	if active := n.getActive(); active != nil {
		return active.GetIP()
	}
	return nil
}

func (n *AutoNetworkedNetwork) ListNodes() []NodeInfo {
	if active := n.getActive(); active != nil {
		return active.ListNodes()
	}
	return []NodeInfo{}
}

func (n *AutoNetworkedNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	if active := n.getActive(); active != nil {
		return active.Dial(ctx, nodeID, port)
	}
	return nil, fmt.Errorf("network not started")
}

func (n *AutoNetworkedNetwork) Listen(port int) (net.Listener, error) {
	if active := n.getActive(); active != nil {
		return active.Listen(port)
	}
	return nil, fmt.Errorf("network not started")
}

func (n *AutoNetworkedNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	if active := n.getActive(); active != nil {
		return active.GetConnectionInfo(nodeID)
	}
	return ConnectionInfo{Type: "networked"}
}

func (n *AutoNetworkedNetwork) GetRuntimeInfo() RuntimeInfo {
	if active := n.getActive(); active != nil {
		info := active.GetRuntimeInfo()
		if info.BackendState == "" {
			info.BackendState = "Running"
		}
		if len(info.Health) == 0 {
			info.Health = []string{"active backend: " + n.ActiveBackend()}
		}
		return info
	}

	return RuntimeInfo{
		BackendState: "Unavailable",
		Health:       []string{"network backend not started"},
	}
}

func (n *AutoNetworkedNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	n.mu.Lock()
	n.stateFn = fn
	active := n.active
	n.mu.Unlock()

	if active != nil {
		active.OnNodeStateChange(fn)
	}
}

func (n *AutoNetworkedNetwork) SetDERPExpectation(expect DERPExpectation) {
	n.mu.Lock()
	n.derpExpect = expect
	system := n.system
	tsnet := n.tsnet
	n.mu.Unlock()

	if setter, ok := system.(DERPExpectationSetter); ok {
		setter.SetDERPExpectation(expect)
	}
	if setter, ok := tsnet.(DERPExpectationSetter); ok {
		setter.SetDERPExpectation(expect)
	}
}

func (n *AutoNetworkedNetwork) SetTSNetControlURL(controlURL string) {
	n.mu.RLock()
	tsnet := n.tsnet
	n.mu.RUnlock()

	if setter, ok := tsnet.(TSNetControlURLSetter); ok {
		setter.SetTSNetControlURL(controlURL)
	}
}

func (n *AutoNetworkedNetwork) ActiveBackend() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.backend
}

func (n *AutoNetworkedNetwork) getActive() Network {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.active
}

func (n *AutoNetworkedNetwork) candidateOrder() []string {
	preference := strings.ToLower(strings.TrimSpace(os.Getenv("PARTYLAN_NETWORKED_BACKEND")))
	switch preference {
	case "tailscale-system", "system":
		return []string{"tailscale-system"}
	case "tsnet":
		return []string{"tsnet"}
	default:
		return []string{"tailscale-system", "tsnet"}
	}
}

func (n *AutoNetworkedNetwork) backendFor(name string) (string, Network) {
	switch name {
	case "tailscale-system":
		return name, n.system
	case "tsnet":
		return name, n.tsnet
	default:
		return "", nil
	}
}

func networkReady(backend Network) bool {
	if backend == nil {
		return false
	}
	if backend.GetNodeID() == "" {
		return false
	}
	ip := backend.GetIP()
	if ip == nil || ip.IsUnspecified() {
		return false
	}

	state := strings.ToLower(strings.TrimSpace(backend.GetRuntimeInfo().BackendState))
	if state == "" {
		return true
	}
	return state == "running"
}

var _ Network = (*AutoNetworkedNetwork)(nil)
