//go:build windows && cgo && partylan_native_media

package video

import "PartyLAN/internal/native/windowsmedia"

type windowsMediaBridge struct {
	bridge windowsmedia.Bridge
}

func init() {
	nativeBridgeFactory = func() NativeBridge {
		return windowsMediaBridge{
			bridge: windowsmedia.NewBridge(),
		}
	}
}

func (b windowsMediaBridge) QueryCapability() NativeBridgeCapability {
	cap := b.bridge.QueryCapability()
	status := NativeBridgeStatusUnavailable
	switch cap.Status {
	case windowsmedia.StatusOK:
		status = NativeBridgeStatusOK
	case windowsmedia.StatusUnsupportedGPU:
		status = NativeBridgeStatusUnsupportedGPU
	case windowsmedia.StatusNotImplemented:
		status = NativeBridgeStatusNotImplemented
	case windowsmedia.StatusInternalError:
		status = NativeBridgeStatusInternalError
	default:
		status = NativeBridgeStatusUnavailable
	}

	return NativeBridgeCapability{
		Status:       status,
		AdapterName:  cap.AdapterName,
		DriverName:   cap.DriverName,
		Reason:       cap.Reason,
		SupportsHost: cap.SupportsHost,
		SupportsView: cap.SupportsView,
	}
}
