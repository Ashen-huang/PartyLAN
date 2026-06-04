package video

type SoftwareBackend struct{}

func NewSoftwareBackend() *SoftwareBackend {
	return &SoftwareBackend{}
}

func (b *SoftwareBackend) Name() string {
	return "software-backend"
}

func (b *SoftwareBackend) Capability() BackendCapability {
	return BackendCapability{
		Backend:      BackendSoftware,
		Status:       CapabilitySupported,
		Adapter:      "Software",
		Driver:       "",
		Reason:       "",
		SupportsHost: true,
		SupportsView: true,
	}
}

func (b *SoftwareBackend) NewCapturer() Capturer {
	return NewScreenCapturer()
}

func (b *SoftwareBackend) NewEncoder() Encoder {
	return NewSoftwareEncoder()
}

func (b *SoftwareBackend) NewDecoder() Decoder {
	return NewSoftwareDecoder()
}
