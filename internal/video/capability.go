package video

type BackendType string

const (
	BackendSoftware BackendType = "software"
	BackendNative   BackendType = "native"
)

type CapabilityStatus string

const (
	CapabilityUnknown     CapabilityStatus = "unknown"
	CapabilitySupported   CapabilityStatus = "supported"
	CapabilityUnsupported CapabilityStatus = "unsupported"
	CapabilityUnavailable CapabilityStatus = "unavailable"
)

type BackendCapability struct {
	Backend      BackendType
	Status       CapabilityStatus
	Adapter      string
	Driver       string
	Reason       string
	SupportsHost bool
	SupportsView bool
}
