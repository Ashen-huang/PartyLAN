//go:build partylan_tsnet

package network

import (
	"context"
	"net"
	"testing"
)

func TestNetworkedNetworkInitialState(t *testing.T) {
	net := NewNetworkedNetwork()

	if got := net.GetNodeID(); got != "" {
		t.Fatalf("expected empty node ID before start, got %q", got)
	}

	if ip := net.GetIP(); ip != nil {
		t.Fatalf("expected nil IP before start, got %v", ip)
	}

	if nodes := net.ListNodes(); len(nodes) != 0 {
		t.Fatalf("expected no peers before start, got %d", len(nodes))
	}
}

func TestProbeRoomInfoNetworkedPairUsesDialer(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	var gotNetwork, gotAddr string
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		_ = ctx
		gotNetwork = network
		gotAddr = address
		return serverConn, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 16)
		n, err := clientConn.Read(buf)
		if err != nil {
			t.Errorf("Read: %v", err)
			return
		}
		if got := string(buf[:n]); got != "PING\n" {
			t.Errorf("probe payload = %q, want %q", got, "PING\n")
			return
		}
		if _, err := clientConn.Write([]byte("READY room-123\n")); err != nil {
			t.Errorf("Write: %v", err)
		}
	}()

	ok, roomID := probeRoomInfoNetworkedPair(context.Background(), dialer, net.ParseIP("100.89.178.4"))
	<-done

	if !ok {
		t.Fatal("expected probe to report joinable")
	}
	if roomID != "room-123" {
		t.Fatalf("roomID = %q, want %q", roomID, "room-123")
	}
	if gotNetwork != "tcp" {
		t.Fatalf("network = %q, want %q", gotNetwork, "tcp")
	}
	if gotAddr != "100.89.178.4:9002" {
		t.Fatalf("addr = %q, want %q", gotAddr, "100.89.178.4:9002")
	}
}
