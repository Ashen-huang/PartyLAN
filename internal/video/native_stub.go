package video

type NativeBackendStub struct{}

func NewNativeBackendStub() *NativeBackendStub {
	return &NativeBackendStub{}
}

func (b *NativeBackendStub) Name() string {
	return "native-stub"
}

func (b *NativeBackendStub) Capability() BackendCapability {
	return BackendCapability{
		Backend:      BackendNative,
		Status:       CapabilityUnavailable,
		Adapter:      "",
		Driver:       "",
		Reason:       ErrNativeBackendUnavailable.Error(),
		SupportsHost: false,
		SupportsView: false,
	}
}

func (b *NativeBackendStub) NewCapturer() Capturer {
	return NewUnavailableCapturer(ErrNativeBackendUnavailable)
}

func (b *NativeBackendStub) NewEncoder() Encoder {
	return NewUnavailableEncoder(ErrNativeBackendUnavailable)
}

func (b *NativeBackendStub) NewDecoder() Decoder {
	return NewUnavailableDecoder(ErrNativeBackendUnavailable)
}
