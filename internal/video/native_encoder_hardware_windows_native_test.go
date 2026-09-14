//go:build windows && cgo && partylan_native_media

package video

import (
	"os"
	"testing"
)

func TestNativeEncoderHardwareConfigure1080p60(t *testing.T) {
	if os.Getenv("PARTYLAN_TEST_NATIVE_ENCODER") != "1" {
		t.Skip("set PARTYLAN_TEST_NATIVE_ENCODER=1 to exercise the installed NVENC driver")
	}

	encoder := NewNativeEncoder()
	defer encoder.Close()
	t.Logf("displays=%v", ListDisplays())
	encoder.SetInputPixelFormat(NativePixelFormatBGRA)

	if err := encoder.Configure(EncoderConfig{
		Codec:     CodecH264,
		Bitrate:   8000,
		Width:     1920,
		Height:    1080,
		FrameRate: 60,
	}); err != nil {
		t.Fatalf("configure 1920x1080@60 H264 encoder: %v", err)
	}
}

func TestNativeEncoderHardwareConfigureAfterCapturerStart(t *testing.T) {
	if os.Getenv("PARTYLAN_TEST_NATIVE_ENCODER") != "1" {
		t.Skip("set PARTYLAN_TEST_NATIVE_ENCODER=1 to exercise the installed NVENC driver")
	}

	capturer := NewNativeCapturer()
	if err := capturer.Start(CaptureConfig{DisplayIndex: 0, FrameRate: 60}); err != nil {
		t.Fatalf("start native capturer: %v", err)
	}
	defer capturer.Stop()

	encoder := NewNativeEncoder()
	defer encoder.Close()
	encoder.SetInputPixelFormat(NativePixelFormatBGRA)
	if err := encoder.Configure(EncoderConfig{
		Codec:     CodecH264,
		Bitrate:   8000,
		Width:     1920,
		Height:    1080,
		FrameRate: 60,
	}); err != nil {
		t.Fatalf("configure after capturer start: %v", err)
	}
}
