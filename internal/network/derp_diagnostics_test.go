package network

import (
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestDERPExpectationHealthPresent(t *testing.T) {
	derpMap := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			901: {
				RegionID:   901,
				RegionCode: "cn1",
				RegionName: "PartyLAN Relay",
				Nodes: []*tailcfg.DERPNode{
					{
						Name:     "901a",
						RegionID: 901,
						HostName: "relay.example.com",
						DERPPort: 443,
					},
				},
			},
		},
	}

	health := derpExpectationHealth(DERPExpectation{
		RegionID:   901,
		RegionCode: "cn1",
		HostName:   "relay.example.com",
		DERPPort:   443,
	}, derpMap)

	if len(health) != 1 {
		t.Fatalf("expected one health line, got %d", len(health))
	}
	if !strings.Contains(health[0], "custom DERP present") {
		t.Fatalf("expected present health line, got %q", health[0])
	}
}

func TestDERPExpectationHealthMissing(t *testing.T) {
	derpMap := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			902: {
				RegionID:   902,
				RegionCode: "other",
				Nodes: []*tailcfg.DERPNode{
					{RegionID: 902, HostName: "other.example.com"},
				},
			},
		},
	}

	health := derpExpectationHealth(DERPExpectation{
		RegionID: 901,
		HostName: "relay.example.com",
	}, derpMap)

	if len(health) != 1 {
		t.Fatalf("expected one health line, got %d", len(health))
	}
	if !strings.Contains(health[0], "custom DERP missing") {
		t.Fatalf("expected missing health line, got %q", health[0])
	}
}

func TestDERPExpectationHealthIgnoresEmptyExpectation(t *testing.T) {
	health := derpExpectationHealth(DERPExpectation{}, &tailcfg.DERPMap{})
	if len(health) != 0 {
		t.Fatalf("expected no health lines, got %v", health)
	}
}

func TestSummarizePeerPath(t *testing.T) {
	direct := summarizePeerPath(&ipnstate.PeerStatus{HostName: "peer-a", CurAddr: "1.2.3.4:41641"})
	if !strings.Contains(direct, "path=direct") {
		t.Fatalf("expected direct path, got %q", direct)
	}

	relay := summarizePeerPath(&ipnstate.PeerStatus{HostName: "peer-b", Relay: "cn1"})
	if !strings.Contains(relay, "path=derp") {
		t.Fatalf("expected derp path, got %q", relay)
	}

	peerRelay := summarizePeerPath(&ipnstate.PeerStatus{HostName: "peer-c", PeerRelay: "100.64.0.1:1234:vni:1"})
	if !strings.Contains(peerRelay, "path=peer-relay") {
		t.Fatalf("expected peer relay path, got %q", peerRelay)
	}
}
