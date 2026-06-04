package video

type NativeBridgeStatus int

const (
	NativeBridgeStatusOK NativeBridgeStatus = iota
	NativeBridgeStatusUnavailable
	NativeBridgeStatusUnsupportedGPU
	NativeBridgeStatusNotImplemented
	NativeBridgeStatusInternalError
)

type NativeBridgeCapability struct {
	Status       NativeBridgeStatus
	AdapterName  string
	DriverName   string
	Reason       string
	SupportsHost bool
	SupportsView bool
}

type NativeBridge interface {
	QueryCapability() NativeBridgeCapability
}

func OpenNativeBridge() NativeBridge {
	return nativeBridgeFactory()
}

type nativeBridgeStub struct{}

func (nativeBridgeStub) QueryCapability() NativeBridgeCapability {
	return NativeBridgeCapability{
		Status:       NativeBridgeStatusUnavailable,
		AdapterName:  "",
		DriverName:   "",
		Reason:       ErrNativeBackendUnavailable.Error(),
		SupportsHost: false,
		SupportsView: false,
	}
}

var nativeBridgeFactory = func() NativeBridge {
	return nativeBridgeStub{}
}
