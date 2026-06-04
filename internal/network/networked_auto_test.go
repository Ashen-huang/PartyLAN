package network

import (
	"context"
	"errors"
	"net"
	"testing"
)

type fakeNetworkBackend struct {
	startErr    error
	startCalls  int
	stopCalls   int
	nodeID      string
	ip          net.IP
	runtimeInfo RuntimeInfo
}

func (f *fakeNetworkBackend) Start(ctx context.Context, authKey string) error {
	_ = ctx
	_ = authKey
	f.startCalls++
	return f.startErr
}

func (f *fakeNetworkBackend) Stop() error {
	f.stopCalls++
	return nil
}

func (f *fakeNetworkBackend) GetNodeID() string     { return f.nodeID }
func (f *fakeNetworkBackend) GetIP() net.IP         { return f.ip }
func (f *fakeNetworkBackend) ListNodes() []NodeInfo { return []NodeInfo{} }
func (f *fakeNetworkBackend) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	_ = ctx
	_ = nodeID
	_ = port
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkBackend) Listen(port int) (net.Listener, error) {
	_ = port
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkBackend) GetConnectionInfo(nodeID string) ConnectionInfo {
	_ = nodeID
	return ConnectionInfo{}
}
func (f *fakeNetworkBackend) GetRuntimeInfo() RuntimeInfo { return f.runtimeInfo }
func (f *fakeNetworkBackend) OnNodeStateChange(fn NodeStateChangeFunc) {
	_ = fn
}

func TestAutoNetworkedPrefersSystemBackendWhenReady(t *testing.T) {
	system := &fakeNetworkBackend{
		nodeID:      "sys-node",
		ip:          net.ParseIP("100.64.0.2"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}
	tsnet := &fakeNetworkBackend{
		nodeID:      "ts-node",
		ip:          net.ParseIP("100.64.0.3"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}

	n := &AutoNetworkedNetwork{
		system: system,
		tsnet:  tsnet,
	}

	if err := n.Start(context.Background(), ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if got := n.ActiveBackend(); got != "tailscale-system" {
		t.Fatalf("expected tailscale-system backend, got %q", got)
	}
	if system.startCalls != 1 {
		t.Fatalf("expected system backend to start once, got %d", system.startCalls)
	}
	if tsnet.startCalls != 0 {
		t.Fatalf("expected tsnet backend not to start, got %d", tsnet.startCalls)
	}
}

func TestAutoNetworkedFallsBackToTsnetWhenSystemFails(t *testing.T) {
	system := &fakeNetworkBackend{
		startErr: errors.New("system unavailable"),
	}
	tsnet := &fakeNetworkBackend{
		nodeID:      "ts-node",
		ip:          net.ParseIP("100.64.0.3"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}

	n := &AutoNetworkedNetwork{
		system: system,
		tsnet:  tsnet,
	}

	if err := n.Start(context.Background(), ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if got := n.ActiveBackend(); got != "tsnet" {
		t.Fatalf("expected tsnet backend, got %q", got)
	}
	if tsnet.startCalls != 1 {
		t.Fatalf("expected tsnet backend to start once, got %d", tsnet.startCalls)
	}
}

func TestAutoNetworkedSkipsNotReadySystemBackend(t *testing.T) {
	system := &fakeNetworkBackend{
		nodeID:      "",
		ip:          nil,
		runtimeInfo: RuntimeInfo{BackendState: "NeedsLogin"},
	}
	tsnet := &fakeNetworkBackend{
		nodeID:      "ts-node",
		ip:          net.ParseIP("100.64.0.3"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}

	n := &AutoNetworkedNetwork{
		system: system,
		tsnet:  tsnet,
	}

	if err := n.Start(context.Background(), ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if got := n.ActiveBackend(); got != "tsnet" {
		t.Fatalf("expected tsnet backend, got %q", got)
	}
	if system.stopCalls != 1 {
		t.Fatalf("expected not-ready system backend to be stopped once, got %d", system.stopCalls)
	}
}

func TestAutoNetworkedHonorsBackendPreference(t *testing.T) {
	t.Setenv("PARTYLAN_NETWORKED_BACKEND", "tsnet")

	system := &fakeNetworkBackend{
		nodeID:      "sys-node",
		ip:          net.ParseIP("100.64.0.2"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}
	tsnet := &fakeNetworkBackend{
		nodeID:      "ts-node",
		ip:          net.ParseIP("100.64.0.3"),
		runtimeInfo: RuntimeInfo{BackendState: "Running"},
	}

	n := &AutoNetworkedNetwork{
		system: system,
		tsnet:  tsnet,
	}

	if err := n.Start(context.Background(), ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if got := n.ActiveBackend(); got != "tsnet" {
		t.Fatalf("expected tsnet backend due to preference, got %q", got)
	}
	if system.startCalls != 0 {
		t.Fatalf("expected system backend not started when tsnet is forced, got %d", system.startCalls)
	}
}
