package video

type NativeBackend interface {
	Name() string
	Capability() BackendCapability
	NewCapturer() Capturer
	NewEncoder() Encoder
	NewDecoder() Decoder
}

func NewPreferredHostBackend() NativeBackend {
	backend := NewNativeRuntimeBackend()
	if backend.Capability().SupportsHost {
		return backend
	}
	return NewSoftwareBackend()
}

func NewPreferredViewerBackend() NativeBackend {
	backend := NewNativeRuntimeBackend()
	if backend.Capability().SupportsView {
		return backend
	}
	return NewSoftwareBackend()
}

func GetPreferredHostCapability() BackendCapability {
	return NewPreferredHostBackend().Capability()
}

func GetPreferredViewerCapability() BackendCapability {
	return NewPreferredViewerBackend().Capability()
}
