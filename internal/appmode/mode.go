package appmode

import (
	"os"
	"strings"
)

type Mode string

const (
	ModeLoopback        Mode = "loopback"
	ModeLAN             Mode = "lan"
	ModeNetworked       Mode = "networked"
	ModeTailscaleSystem Mode = "tailscale-system"
)

// DefaultModeName can be overridden at link time so packaged builds can pick a
// different startup mode without changing runtime overrides.
var DefaultModeName = string(ModeTailscaleSystem)

func ParseMode(raw string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case ModeLoopback:
		return ModeLoopback, true
	case ModeLAN:
		return ModeLAN, true
	case ModeNetworked:
		return ModeNetworked, true
	case ModeTailscaleSystem:
		return ModeTailscaleSystem, true
	default:
		return "", false
	}
}

func ResolveMode() Mode {
	if mode, ok := ParseMode(os.Getenv("PARTYLAN_MODE")); ok {
		return mode
	}

	if mode, ok := ParseMode(DefaultModeName); ok {
		return mode
	}
	return ModeLoopback
}
