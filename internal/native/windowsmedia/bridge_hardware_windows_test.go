//go:build windows && cgo

package windowsmedia

import (
	"os"
	"testing"
)

func TestNVENCCompatibilityConfiguration(t *testing.T) {
	if os.Getenv("PARTYLAN_TEST_NATIVE_ENCODER") != "1" {
		t.Skip("set PARTYLAN_TEST_NATIVE_ENCODER=1 to exercise the installed NVENC driver")
	}

	bridge := NewBridge()
	handle := bridge.CreateEncoder()
	if handle == 0 {
		t.Fatal("create native encoder returned an empty handle")
	}
	defer bridge.DestroyEncoder(handle)
	defer bridge.EncoderClose(handle)

	status := bridge.EncoderConfigure(handle, EncoderConfig{
		Width:             1920,
		Height:            1080,
		FPS:               60,
		BitrateKbps:       8000,
		PixelFormat:       PixelFormatBGRA,
		CompatibilityMode: true,
	})
	if status != StatusOK {
		t.Fatalf("compatibility configure status=%d reason=%q", status, bridge.LastErrorReason())
	}
}
