package network

import (
	"net"
	"testing"
	"time"
)

func TestConnectionPool(t *testing.T) {
	pool := NewConnectionPool()

	server, client := net.Pipe()
	defer server.Close()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := server.Read(buf)
			if err != nil {
				return
			}
			server.Write(buf[:n])
		}
	}()

	pool.Put("node1", 8080, client)

	conn := pool.Get("node1", 8080)
	if conn == nil {
		t.Fatal("expected to get pooled connection")
	}

	n, err := conn.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}

	buf := make([]byte, 1024)
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("expected 'hello', got %q", string(buf[:n]))
	}

	info, ok := pool.GetInfo("node1")
	if !ok {
		t.Fatal("expected connection info")
	}
	if info.BytesSent == 0 {
		t.Error("expected non-zero bytes sent")
	}
	if info.BytesRecv == 0 {
		t.Error("expected non-zero bytes recv")
	}
}

func TestConnectionPool_Missing(t *testing.T) {
	pool := NewConnectionPool()

	conn := pool.Get("nonexistent", 8080)
	if conn != nil {
		t.Error("expected nil for missing connection")
	}

	_, ok := pool.GetInfo("nonexistent")
	if ok {
		t.Error("expected false for missing connection info")
	}
}

func TestConnectionPool_CloseAll(t *testing.T) {
	pool := NewConnectionPool()

	server1, client1 := net.Pipe()
	server2, client2 := net.Pipe()
	defer server1.Close()
	defer server2.Close()

	pool.Put("node1", 8080, client1)
	pool.Put("node2", 8080, client2)

	pool.CloseAll()

	if len(pool.conns) != 0 {
		t.Errorf("expected 0 connections after CloseAll, got %d", len(pool.conns))
	}
}

func TestTrackedConn(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	pc := &pooledConn{
		conn:      client,
		nodeID:    "test",
		port:      8080,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	tc := &trackedConn{Conn: client, pc: pc}

	go func() {
		buf := make([]byte, 1024)
		n, _ := server.Read(buf)
		server.Write(buf[:n])
	}()

	tc.Write([]byte("test-data"))
	buf := make([]byte, 1024)
	tc.Read(buf)

	if pc.bytesSent.Load() == 0 {
		t.Error("expected tracked bytes sent")
	}
	if pc.bytesRecv.Load() == 0 {
		t.Error("expected tracked bytes recv")
	}
}

func TestConnectionInfo_TypeDetection(t *testing.T) {
	info := ConnectionInfo{
		Type:      "p2p",
		Latency:   10,
		BytesSent: 1024,
		BytesRecv: 2048,
	}

	if info.Type != "p2p" {
		t.Errorf("expected type 'p2p', got %q", info.Type)
	}
	if info.Latency != 10 {
		t.Errorf("expected latency 10, got %d", info.Latency)
	}
}

func TestNodeInfo(t *testing.T) {
	node := NodeInfo{
		ID:       "test-id",
		Hostname: "test-host",
		IP:       net.ParseIP("100.64.0.1"),
		Online:   true,
		Latency:  15,
	}

	if node.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", node.ID)
	}
	if !node.Online {
		t.Error("expected node to be online")
	}
	if node.Latency != 15 {
		t.Errorf("expected latency 15, got %d", node.Latency)
	}
}

func TestParseDialTargetIP(t *testing.T) {
	tests := map[string]string{
		"100.89.178.4":             "100.89.178.4",
		"100.89.178.4:9000":        "100.89.178.4",
		"[fd7a:115c:a1e0::1]:9000": "fd7a:115c:a1e0::1",
		"not-an-ip":                "",
	}

	for input, want := range tests {
		got := parseDialTargetIP(input)
		if want == "" {
			if got != nil {
				t.Fatalf("parseDialTargetIP(%q) = %s, want nil", input, got)
			}
			continue
		}
		if got == nil || got.String() != want {
			t.Fatalf("parseDialTargetIP(%q) = %v, want %s", input, got, want)
		}
	}
}

func TestPoolKey(t *testing.T) {
	key := poolKey("node1", 8080)
	if key == "" {
		t.Error("expected non-empty pool key")
	}

	key2 := poolKey("node1", 8081)
	if key == key2 {
		t.Error("different ports should produce different keys")
	}

	key3 := poolKey("node2", 8080)
	if key == key3 {
		t.Error("different nodes should produce different keys")
	}
}
