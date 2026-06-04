package network

import (
	"context"
	"fmt"
	"net"
)

type StubNetwork struct {
	nodeID string
	ip     net.IP
}

func NewStubNetwork() *StubNetwork {
	return &StubNetwork{
		nodeID: "stub-node",
		ip:     net.ParseIP("127.0.0.1"),
	}
}

func (s *StubNetwork) Start(ctx context.Context, authKey string) error {
	return nil
}

func (s *StubNetwork) Stop() error {
	return nil
}

func (s *StubNetwork) GetNodeID() string {
	return s.nodeID
}

func (s *StubNetwork) GetIP() net.IP {
	return s.ip
}

func (s *StubNetwork) ListNodes() []NodeInfo {
	return []NodeInfo{}
}

func (s *StubNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	return nil, fmt.Errorf("stub network: dial not implemented")
}

func (s *StubNetwork) Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func (s *StubNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	return ConnectionInfo{}
}

func (s *StubNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {}
