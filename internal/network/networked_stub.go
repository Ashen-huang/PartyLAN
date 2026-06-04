//go:build !partylan_tsnet

package network

import (
	"context"
	"fmt"
	"net"
)

var ErrNetworkedBackendUnavailable = fmt.Errorf("networked backend unavailable: build with -tags=partylan_tsnet to enable tsnet")

type NetworkedNetwork struct{}

func NewNetworkedNetwork() *NetworkedNetwork {
	return &NetworkedNetwork{}
}

func (n *NetworkedNetwork) Start(ctx context.Context, authKey string) error {
	_ = ctx
	_ = authKey
	return ErrNetworkedBackendUnavailable
}

func (n *NetworkedNetwork) Stop() error { return nil }

func (n *NetworkedNetwork) GetNodeID() string { return "" }

func (n *NetworkedNetwork) GetIP() net.IP { return nil }

func (n *NetworkedNetwork) ListNodes() []NodeInfo { return []NodeInfo{} }

func (n *NetworkedNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	_ = ctx
	_ = nodeID
	_ = port
	return nil, ErrNetworkedBackendUnavailable
}

func (n *NetworkedNetwork) Listen(port int) (net.Listener, error) {
	_ = port
	return nil, ErrNetworkedBackendUnavailable
}

func (n *NetworkedNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	_ = nodeID
	return ConnectionInfo{Type: "networked"}
}

func (n *NetworkedNetwork) GetRuntimeInfo() RuntimeInfo {
	return RuntimeInfo{
		BackendState: "Unavailable",
		Health:       []string{ErrNetworkedBackendUnavailable.Error()},
	}
}

func (n *NetworkedNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	_ = fn
}

func (n *NetworkedNetwork) SetDERPExpectation(expect DERPExpectation) {
	_ = expect
}

func (n *NetworkedNetwork) SetTSNetControlURL(controlURL string) {
	_ = controlURL
}

var _ Network = (*NetworkedNetwork)(nil)
