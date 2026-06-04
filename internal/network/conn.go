package network

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type pooledConn struct {
	conn      net.Conn
	nodeID    string
	port      int
	createdAt time.Time
	lastUsed  time.Time

	bytesSent atomic.Uint64
	bytesRecv atomic.Uint64
}

type ConnectionPool struct {
	mu    sync.RWMutex
	conns map[string]*pooledConn
}

func NewConnectionPool() *ConnectionPool {
	return &ConnectionPool{
		conns: make(map[string]*pooledConn),
	}
}

func poolKey(nodeID string, port int) string {
	return fmt.Sprintf("%s:%d", nodeID, port)
}

func (p *ConnectionPool) Get(nodeID string, port int) net.Conn {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := poolKey(nodeID, port)
	pc, ok := p.conns[key]
	if !ok {
		return nil
	}

	if time.Since(pc.lastUsed) > 5*time.Minute {
		return nil
	}

	pc.lastUsed = time.Now()
	return &trackedConn{Conn: pc.conn, pc: pc}
}

func (p *ConnectionPool) Put(nodeID string, port int, conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := poolKey(nodeID, port)
	now := time.Now()
	p.conns[key] = &pooledConn{
		conn:      conn,
		nodeID:    nodeID,
		port:      port,
		createdAt: now,
		lastUsed:  now,
	}
}

func (p *ConnectionPool) Remove(nodeID string, port int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := poolKey(nodeID, port)
	if pc, ok := p.conns[key]; ok {
		pc.conn.Close()
		delete(p.conns, key)
	}
}

func (p *ConnectionPool) GetInfo(nodeID string) (ConnectionInfo, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, pc := range p.conns {
		if pc.nodeID == nodeID {
			return ConnectionInfo{
				Type:      "p2p",
				Latency:   time.Since(pc.lastUsed).Milliseconds(),
				BytesSent: pc.bytesSent.Load(),
				BytesRecv: pc.bytesRecv.Load(),
			}, true
		}
	}

	return ConnectionInfo{}, false
}

func (p *ConnectionPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, pc := range p.conns {
		pc.conn.Close()
		delete(p.conns, key)
	}
}

type trackedConn struct {
	net.Conn
	pc *pooledConn
}

func (c *trackedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.pc.bytesRecv.Add(uint64(n))
		c.pc.lastUsed = time.Now()
	}
	return n, err
}

func (c *trackedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.pc.bytesSent.Add(uint64(n))
		c.pc.lastUsed = time.Now()
	}
	return n, err
}
