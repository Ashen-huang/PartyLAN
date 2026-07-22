package network

import (
	"fmt"

	"PartyLAN/internal/appmode"
)

func NewNetworkForMode(mode appmode.Mode) (Network, error) {
	switch mode {
	case appmode.ModeGameCastClient:
		return NewLoopbackNetwork("gamecast-client"), nil
	case appmode.ModeLoopback:
		return NewLoopbackNetwork("host-node"), nil
	case appmode.ModeLAN:
		return NewLANNetwork(), nil
	case appmode.ModeTailscaleSystem:
		return NewTailscaleSystemNetwork(), nil
	case appmode.ModeNetworked:
		return NewAutoNetworkedNetwork(), nil
	default:
		return nil, fmt.Errorf("unknown app mode: %s", mode)
	}
}
