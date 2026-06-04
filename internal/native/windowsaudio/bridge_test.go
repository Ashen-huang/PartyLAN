package windowsaudio

import (
	"os"
	"strings"
	"testing"
)

func TestBridgeCapabilityReportsConcreteReason(t *testing.T) {
	capability := NewBridge().QueryCapability()
	if strings.TrimSpace(capability.Reason) == "" {
		t.Fatal("expected audio capability to include a diagnostic reason")
	}
	if strings.Contains(strings.ToLower(capability.Reason), "audio process capture not implemented yet") {
		t.Fatalf("audio capability still reports the old permanent stub reason: %q", capability.Reason)
	}
}

func TestSessionConfigurationValidatesProcessLoopbackTarget(t *testing.T) {
	bridge := NewBridge()
	handle := bridge.CreateSession()
	if handle == 0 {
		t.Skip("audio bridge session allocation unavailable in this build")
	}
	defer bridge.DestroySession(handle)

	if status := bridge.ConfigureSession(handle, SessionConfig{}); status != StatusInternalError {
		t.Fatalf("ConfigureSession without target PID = %s, want %s", status, StatusInternalError)
	}
	if reason := bridge.LastErrorReason(); !strings.Contains(strings.ToLower(reason), "target process id") {
		t.Fatalf("expected target PID validation reason, got %q", reason)
	}

	status := bridge.ConfigureSession(handle, SessionConfig{
		TargetProcessID: uint32(os.Getpid()),
		Mode:            CaptureIncludeProcessTree,
	})
	if status != StatusOK {
		t.Fatalf("ConfigureSession with current process target = %s, want %s; reason=%q", status, StatusOK, bridge.LastErrorReason())
	}
}
