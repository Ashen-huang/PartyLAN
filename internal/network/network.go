package network

import (
	"context"
	"net"
)

type DERPExpectation struct {
	RegionID   int
	RegionCode string
	HostName   string
	DERPPort   int
}

type NodeInfo struct {
	ID       string
	Hostname string
	IP       net.IP
	Online   bool
	Latency  int64 // milliseconds
	Joinable bool
	RoomID   string
}

type ConnectionInfo struct {
	Type      string // "p2p" or "relay"
	Latency   int64
	BytesSent uint64
	BytesRecv uint64
}

type RuntimeInfo struct {
	BackendState string
	AuthURL      string
	Health       []string
}

type DERPExpectationSetter interface {
	SetDERPExpectation(DERPExpectation)
}

type TSNetControlURLSetter interface {
	SetTSNetControlURL(string)
}

type NodeStateChangeFunc func(node NodeInfo)

type Network interface {
	Start(ctx context.Context, authKey string) error
	Stop() error
	GetNodeID() string
	GetIP() net.IP
	ListNodes() []NodeInfo
	Dial(ctx context.Context, nodeID string, port int) (net.Conn, error)
	Listen(port int) (net.Listener, error)
	GetConnectionInfo(nodeID string) ConnectionInfo
	GetRuntimeInfo() RuntimeInfo
	OnNodeStateChange(fn NodeStateChangeFunc)
}
