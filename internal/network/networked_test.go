//go:build !partylan_tsnet

package network

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkedNetworkStartReturnsUnavailableByDefault(t *testing.T) {
	net := NewNetworkedNetwork()
	err := net.Start(context.Background(), "")
	if !errors.Is(err, ErrNetworkedBackendUnavailable) {
		t.Fatalf("expected ErrNetworkedBackendUnavailable, got %v", err)
	}
}

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
