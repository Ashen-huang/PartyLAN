package network

import (
	"errors"
	"net"
	"strings"
)

func sanitizeHostname(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "PartyLAN"
	}
	name = strings.ToLower(name)
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-")
	return replacer.Replace(name)
}

func parseDialTargetIP(nodeID string) net.IP {
	target := strings.TrimSpace(nodeID)
	if target == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(target); err == nil && host != "" {
		target = host
	}
	target = strings.Trim(target, "[]")
	return net.ParseIP(target)
}

func sameIP(a, b net.IP) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(b)
}

func isClosedConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}
