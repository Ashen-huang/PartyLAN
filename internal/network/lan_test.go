package network

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestLANNetworkApplyBeacon(t *testing.T) {
	net := newLANNetworkWithOptions(lanOptions{
		NodeID:   "lan-self",
		Hostname: "self",
		IP:       netIP4(192, 168, 1, 10),
	})

	node, changed := net.applyBeacon(lanBeacon{
		App:      lanDiscoveryAppName,
		Version:  lanProtocolVersion,
		NodeID:   "lan-peer",
		Hostname: "peer-host",
		IP:       "127.0.0.1",
	}, nil, time.Now())

	if !changed {
		t.Fatal("expected new peer to be reported as changed")
	}
	if node.ID != "lan-peer" || node.Hostname != "peer-host" || node.IP.String() != "127.0.0.1" {
		t.Fatalf("unexpected node: %#v", node)
	}

	nodes := net.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected one LAN node, got %d", len(nodes))
	}
	if nodes[0].ID != "lan-peer" {
		t.Fatalf("expected lan-peer, got %q", nodes[0].ID)
	}
}

func TestLANNetworkApplyBeaconIgnoresSelf(t *testing.T) {
	net := newLANNetworkWithOptions(lanOptions{NodeID: "lan-self", Hostname: "self"})

	if _, changed := net.applyBeacon(lanBeacon{
		App:     lanDiscoveryAppName,
		Version: lanProtocolVersion,
		NodeID:  "lan-self",
		IP:      "192.168.1.20",
	}, nil, time.Now()); changed {
		t.Fatal("self beacon should not update peers")
	}
	if nodes := net.ListNodes(); len(nodes) != 0 {
		t.Fatalf("expected no peers, got %d", len(nodes))
	}
}

func TestLANNetworkApplyBeaconPrefersRemoteIP(t *testing.T) {
	lan := newLANNetworkWithOptions(lanOptions{NodeID: "lan-self", Hostname: "self"})

	node, changed := lan.applyBeacon(lanBeacon{
		App:      lanDiscoveryAppName,
		Version:  lanProtocolVersion,
		NodeID:   "lan-peer",
		Hostname: "peer-host",
		IP:       "192.168.56.1",
	}, netIP4(192, 168, 1, 20), time.Now())

	if !changed {
		t.Fatal("expected beacon to add peer")
	}
	if node.IP.String() != "192.168.1.20" {
		t.Fatalf("expected remote source IP, got %s", node.IP)
	}
}

func TestLANNetworkPrunesStalePeers(t *testing.T) {
	net := newLANNetworkWithOptions(lanOptions{
		NodeID:     "lan-self",
		Hostname:   "self",
		StaleAfter: 50 * time.Millisecond,
	})

	net.applyBeacon(lanBeacon{
		App:     lanDiscoveryAppName,
		Version: lanProtocolVersion,
		NodeID:  "lan-peer",
		IP:      "192.168.1.20",
	}, nil, time.Now().Add(-time.Second))

	if nodes := net.ListNodes(); len(nodes) != 0 {
		t.Fatalf("expected stale peer to be pruned, got %d", len(nodes))
	}
}

func TestLANNetworkDialDirectIP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErr <- err
			return
		}
		if string(buf) != "ping" {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		_, err = conn.Write([]byte("pong"))
		serverErr <- err
	}()

	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatalf("LookupPort: %v", err)
	}

	lan := NewLANNetwork()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := lan.Dial(ctx, "127.0.0.1", port)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(reply) != "pong" {
		t.Fatalf("expected pong, got %q", string(reply))
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func netIP4(a, b, c, d byte) net.IP {
	return net.IPv4(a, b, c, d).To4()
}
