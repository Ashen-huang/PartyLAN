package video

type NativeRuntimeBackend struct {
	bridge     NativeBridge
	capability BackendCapability
}

func NewNativeRuntimeBackend() *NativeRuntimeBackend {
	bridge := OpenNativeBridge()
	bridgeCapability := bridge.QueryCapability()

	status := CapabilityUnavailable
	if bridgeCapability.Status == NativeBridgeStatusOK {
		status = CapabilitySupported
	} else if bridgeCapability.Status == NativeBridgeStatusNotImplemented {
		status = CapabilityUnavailable
	} else if bridgeCapability.Status == NativeBridgeStatusUnsupportedGPU {
		status = CapabilityUnsupported
	}

	return &NativeRuntimeBackend{
		bridge: bridge,
		capability: BackendCapability{
			Backend:      BackendNative,
			Status:       status,
			Adapter:      bridgeCapability.AdapterName,
			Driver:       bridgeCapability.DriverName,
			Reason:       bridgeCapability.Reason,
			SupportsHost: bridgeCapability.SupportsHost,
			SupportsView: bridgeCapability.SupportsView,
		},
	}
}

func (b *NativeRuntimeBackend) Name() string {
	return "native-runtime"
}

func (b *NativeRuntimeBackend) Capability() BackendCapability {
	return b.capability
}

func (b *NativeRuntimeBackend) NewCapturer() Capturer {
	if !b.capability.SupportsHost {
		return NewUnavailableCapturer(ErrNativeBackendUnavailable)
	}
	return NewNativeCapturer()
}

func (b *NativeRuntimeBackend) NewEncoder() Encoder {
	if !b.capability.SupportsHost {
		return NewUnavailableEncoder(ErrNativeBackendUnavailable)
	}
	return NewNativeEncoder()
}

func (b *NativeRuntimeBackend) NewDecoder() Decoder {
	if !b.capability.SupportsView {
		return NewUnavailableDecoder(ErrNativeBackendUnavailable)
	}
	return NewNativeDecoder()
}
