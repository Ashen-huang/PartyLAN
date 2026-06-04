package network

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func derpExpectationFromEnv() DERPExpectation {
	return DERPExpectation{
		RegionID:   envInt("PARTYLAN_DERP_REGION_ID"),
		RegionCode: strings.TrimSpace(os.Getenv("PARTYLAN_DERP_REGION_CODE")),
		HostName:   strings.TrimSpace(os.Getenv("PARTYLAN_DERP_HOSTNAME")),
		DERPPort:   envInt("PARTYLAN_DERP_PORT"),
	}
}

func envInt(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func mergeDERPExpectation(base, override DERPExpectation) DERPExpectation {
	if override.RegionID != 0 {
		base.RegionID = override.RegionID
	}
	if override.RegionCode != "" {
		base.RegionCode = override.RegionCode
	}
	if override.HostName != "" {
		base.HostName = override.HostName
	}
	if override.DERPPort != 0 {
		base.DERPPort = override.DERPPort
	}
	return base
}

func (e DERPExpectation) configured() bool {
	return e.RegionID != 0 || e.RegionCode != "" || e.HostName != "" || e.DERPPort != 0
}

func derpExpectationHealth(expect DERPExpectation, derpMap *tailcfg.DERPMap) []string {
	if !expect.configured() {
		return nil
	}
	if derpMap == nil || len(derpMap.Regions) == 0 {
		return []string{"custom DERP expected but active DERP map is unavailable"}
	}

	if region, node, ok := findExpectedDERP(expect, derpMap); ok {
		return []string{fmt.Sprintf(
			"custom DERP present: region=%d/%s host=%s port=%d",
			region.RegionID,
			firstNonEmptyString(region.RegionCode, "unknown"),
			node.HostName,
			normalizedDERPPort(node.DERPPort),
		)}
	}

	return []string{fmt.Sprintf(
		"custom DERP missing from active DERP map: expected region_id=%d code=%q host=%q port=%d",
		expect.RegionID,
		expect.RegionCode,
		expect.HostName,
		expect.DERPPort,
	)}
}

func findExpectedDERP(expect DERPExpectation, derpMap *tailcfg.DERPMap) (*tailcfg.DERPRegion, *tailcfg.DERPNode, bool) {
	if derpMap == nil {
		return nil, nil, false
	}
	for _, region := range derpMap.Regions {
		if region == nil || !matchesExpectedRegion(expect, region) {
			continue
		}
		for _, node := range region.Nodes {
			if node == nil {
				continue
			}
			if matchesExpectedDERPNode(expect, node) {
				return region, node, true
			}
		}
		if expect.HostName == "" && expect.DERPPort == 0 {
			return region, &tailcfg.DERPNode{RegionID: region.RegionID}, true
		}
	}
	return nil, nil, false
}

func matchesExpectedRegion(expect DERPExpectation, region *tailcfg.DERPRegion) bool {
	if expect.RegionID != 0 && region.RegionID != expect.RegionID {
		return false
	}
	if expect.RegionCode != "" && !strings.EqualFold(region.RegionCode, expect.RegionCode) {
		return false
	}
	return true
}

func matchesExpectedDERPNode(expect DERPExpectation, node *tailcfg.DERPNode) bool {
	if expect.HostName != "" && !strings.EqualFold(node.HostName, expect.HostName) {
		return false
	}
	if expect.DERPPort != 0 && normalizedDERPPort(node.DERPPort) != expect.DERPPort {
		return false
	}
	return true
}

func normalizedDERPPort(port int) int {
	if port == 0 {
		return 443
	}
	return port
}

func summarizeDERPMap(derpMap *tailcfg.DERPMap) string {
	if derpMap == nil || len(derpMap.Regions) == 0 {
		return "DERP map unavailable"
	}
	ids := derpMap.RegionIDs()
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		region := derpMap.Regions[id]
		if region == nil {
			continue
		}
		nodeHosts := make([]string, 0, len(region.Nodes))
		for _, node := range region.Nodes {
			if node == nil {
				continue
			}
			nodeHosts = append(nodeHosts, fmt.Sprintf("%s:%d", node.HostName, normalizedDERPPort(node.DERPPort)))
		}
		parts = append(parts, fmt.Sprintf("%d/%s[%s]", region.RegionID, region.RegionCode, strings.Join(nodeHosts, ",")))
	}
	return "DERP map regions: " + strings.Join(parts, " ")
}

func summarizePeerPath(peer *ipnstate.PeerStatus) string {
	if peer == nil {
		return ""
	}
	name := firstNonEmptyString(peer.HostName, strings.TrimSuffix(peer.DNSName, "."), string(peer.ID))
	switch {
	case peer.CurAddr != "":
		return fmt.Sprintf("peer %s path=direct addr=%s", name, peer.CurAddr)
	case peer.PeerRelay != "":
		return fmt.Sprintf("peer %s path=peer-relay addr=%s", name, peer.PeerRelay)
	case peer.Relay != "":
		return fmt.Sprintf("peer %s path=derp relay=%s", name, peer.Relay)
	default:
		return fmt.Sprintf("peer %s path=unknown online=%t active=%t", name, peer.Online, peer.Active)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
