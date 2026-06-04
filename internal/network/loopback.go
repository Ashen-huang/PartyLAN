package network

import (
	"context"
	"fmt"
	"net"
	"sync"
)

const LoopbackNodeID = "loopback-viewer"

type LoopbackNetwork struct {
	mu        sync.RWMutex
	nodeID    string
	ip        net.IP
	listeners map[int]*loopbackListener
}

func NewLoopbackNetwork(nodeID string) *LoopbackNetwork {
	if nodeID == "" {
		nodeID = "local-node"
	}
	return &LoopbackNetwork{
		nodeID:    nodeID,
		ip:        net.ParseIP("127.0.0.1"),
		listeners: make(map[int]*loopbackListener),
	}
}

func (n *LoopbackNetwork) Start(ctx context.Context, authKey string) error {
	return nil
}

func (n *LoopbackNetwork) Stop() error {
	n.mu.Lock()
	listeners := make([]*loopbackListener, 0, len(n.listeners))
	for port, listener := range n.listeners {
		listeners = append(listeners, listener)
		delete(n.listeners, port)
	}
	n.mu.Unlock()

	for _, listener := range listeners {
		_ = listener.Close()
	}
	return nil
}

func (n *LoopbackNetwork) GetNodeID() string {
	return n.nodeID
}

func (n *LoopbackNetwork) GetIP() net.IP {
	return n.ip
}

func (n *LoopbackNetwork) ListNodes() []NodeInfo {
	return []NodeInfo{
		{
			ID:       LoopbackNodeID,
			Hostname: "Loopback Viewer",
			IP:       n.ip,
			Online:   true,
			Latency:  1,
		},
	}
}

func (n *LoopbackNetwork) Dial(ctx context.Context, nodeID string, port int) (net.Conn, error) {
	n.mu.RLock()
	listener, ok := n.listeners[port]
	n.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("loopback listener on port %d not found", port)
	}

	serverConn, clientConn := net.Pipe()
	select {
	case <-ctx.Done():
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, ctx.Err()
	case <-listener.closed:
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, fmt.Errorf("listener closed")
	case listener.acceptCh <- serverConn:
		return clientConn, nil
	}
}

func (n *LoopbackNetwork) Listen(port int) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.listeners[port]; exists {
		return nil, fmt.Errorf("port %d already in use", port)
	}

	listener := &loopbackListener{
		port:     port,
		acceptCh: make(chan net.Conn, 8),
		closed:   make(chan struct{}),
		onClose: func() {
			n.mu.Lock()
			delete(n.listeners, port)
			n.mu.Unlock()
		},
	}
	n.listeners[port] = listener
	return listener, nil
}

func (n *LoopbackNetwork) GetConnectionInfo(nodeID string) ConnectionInfo {
	return ConnectionInfo{
		Type:    "loopback",
		Latency: 1,
	}
}

func (n *LoopbackNetwork) GetRuntimeInfo() RuntimeInfo {
	return RuntimeInfo{
		BackendState: "Running",
	}
}

func (n *LoopbackNetwork) OnNodeStateChange(fn NodeStateChangeFunc) {
	if fn == nil {
		return
	}
	fn(NodeInfo{
		ID:       LoopbackNodeID,
		Hostname: "Loopback Viewer",
		IP:       n.ip,
		Online:   true,
		Latency:  1,
	})
}

type loopbackListener struct {
	port      int
	acceptCh  chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	onClose   func()
}

func (l *loopbackListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn := <-l.acceptCh:
		return conn, nil
	}
}

func (l *loopbackListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
		if l.onClose != nil {
			l.onClose()
		}
	})
	return nil
}

func (l *loopbackListener) Addr() net.Addr {
	return loopbackAddr{port: l.port}
}

type loopbackAddr struct {
	port int
}

func (a loopbackAddr) Network() string {
	return "loopback"
}

func (a loopbackAddr) String() string {
	return fmt.Sprintf("loopback:%d", a.port)
}

var _ Network = (*LoopbackNetwork)(nil)
var _ net.Listener = (*loopbackListener)(nil)
