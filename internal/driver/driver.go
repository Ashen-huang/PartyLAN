package driver

type DriverStatus int

const (
	StatusNotInstalled DriverStatus = iota
	StatusInstalled
	StatusError
)

type AudioDevice struct {
	ID          string
	Name        string
	IsDefault   bool
	IsVirtual   bool
}

type VirtualAudioDriver interface {
	GetStatus() DriverStatus
	Install() error
	Uninstall() error
	RouteAppToVirtualDevice(processID int) error
	UnrouteApp(processID int) error
	ListDevices() []AudioDevice
	CaptureFromVirtualDevice() ([]byte, error)
}
