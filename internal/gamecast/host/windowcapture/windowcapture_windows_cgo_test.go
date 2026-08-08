//go:build windows && cgo

package windowcapture

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"PartyLAN/internal/video"

	"golang.org/x/sys/windows"
)

func TestCGOWindowCaptureUsesWindowsGraphicsCapture(t *testing.T) {
	capturer := New(1, 30)
	reporter, ok := any(capturer).(interface{ Backend() string })
	if !ok {
		t.Fatal("window capturer does not report its capture backend")
	}
	if got, want := reporter.Backend(), "windows-graphics-capture"; got != want {
		t.Fatalf("window capture backend = %q, want %q", got, want)
	}
}

func TestWindowsGraphicsCaptureWindowLifecycle(t *testing.T) {
	rawHandle := os.Getenv("PARTYLAN_WGC_TEST_HWND")
	selfManagedWindow := false
	var handle uint64
	var fixture *wgcFixtureWindow
	if rawHandle == "" && os.Getenv("PARTYLAN_WGC_TEST_LIFECYCLE") == "1" {
		fixture = createWGCFixtureWindow(t)
		handle = uint64(fixture.handle)
		selfManagedWindow = true
		t.Cleanup(fixture.close)
	} else if rawHandle == "" {
		t.Skip("set PARTYLAN_WGC_TEST_HWND to run the real-window WGC integration test")
	} else {
		var err error
		handle, err = strconv.ParseUint(rawHandle, 10, 64)
		if err != nil || handle == 0 {
			t.Fatalf("invalid PARTYLAN_WGC_TEST_HWND %q", rawHandle)
		}
	}

	capturer := New(uintptr(handle), 30)
	t.Logf("starting WGC for hwnd=%d", handle)
	if err := capturer.Start(); err != nil {
		t.Fatalf("start WGC capturer: %v", err)
	}
	t.Log("WGC started")
	defer capturer.Stop()

	assertFrame := func(stage string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			frame, frameErr := capturer.GetFrame()
			if frameErr == nil && frame != nil && frame.Width > 0 && frame.Height > 0 && len(frame.Data) >= frame.Width*frame.Height*4 {
				return
			}
			lastErr = frameErr
		}
		t.Fatalf("%s: no WGC frame before deadline: %v", stage, lastErr)
	}

	assertFrame("visible")
	t.Log("visible frame received")
	if os.Getenv("PARTYLAN_WGC_TEST_LIFECYCLE") != "1" {
		return
	}

	user32 := windows.NewLazySystemDLL("user32.dll")
	isWindow := user32.NewProc("IsWindow")
	const (
		swMinimize = 6
		swRestore  = 9
		wmClose    = 0x0010
	)

	if fixture != nil {
		fixture.show(swMinimize)
	} else {
		user32.NewProc("ShowWindow").Call(uintptr(handle), swMinimize)
	}
	time.Sleep(300 * time.Millisecond)
	assertFrame("minimized")
	t.Log("minimized frame received")

	if fixture != nil {
		fixture.show(swRestore)
	} else {
		user32.NewProc("ShowWindow").Call(uintptr(handle), swRestore)
	}
	time.Sleep(300 * time.Millisecond)
	assertFrame("restored")
	t.Log("restored frame received")

	if selfManagedWindow {
		fixture.close()
	} else {
		windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW").Call(uintptr(handle), wmClose, 0, 0)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		alive, _, _ := isWindow.Call(uintptr(handle))
		_, frameErr := capturer.GetFrame()
		if alive == 0 && errors.Is(frameErr, ErrWindowClosed) {
			return
		}
	}
	t.Fatal("closed window was not reported as ErrWindowClosed")
}

func TestWindowsGraphicsCaptureAllowsImmediateNVENCConfigure(t *testing.T) {
	if os.Getenv("PARTYLAN_TEST_NATIVE_ENCODER") != "1" {
		t.Skip("set PARTYLAN_TEST_NATIVE_ENCODER=1 to exercise the installed NVENC driver")
	}
	fixture := createWGCFixtureWindow(t)
	t.Cleanup(fixture.close)

	capturer := New(fixture.handle, 60)
	if err := capturer.Start(); err != nil {
		t.Fatalf("start WGC capturer: %v", err)
	}
	defer capturer.Stop()

	encoder := video.NewNativeEncoder()
	defer encoder.Close()
	encoder.SetInputPixelFormat(video.NativePixelFormatBGRA)
	if err := encoder.Configure(video.EncoderConfig{
		Codec:     video.CodecH264,
		Bitrate:   8000,
		Width:     800,
		Height:    500,
		FrameRate: 60,
	}); err != nil {
		t.Fatalf("configure NVENC immediately after WGC start: %v", err)
	}
}

type wgcFixtureWindow struct {
	handle   uintptr
	commands chan uintptr
	done     chan struct{}
}

func createWGCFixtureWindow(t *testing.T) *wgcFixtureWindow {
	t.Helper()
	fixture := &wgcFixtureWindow{
		commands: make(chan uintptr),
		done:     make(chan struct{}),
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(fixture.done)

		user32 := windows.NewLazySystemDLL("user32.dll")
		kernel32 := windows.NewLazySystemDLL("kernel32.dll")
		className, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("PartyLAN Windows Graphics Capture Fixture")
		module, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
		handle, _, callErr := user32.NewProc("CreateWindowExW").Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(title)),
			0x00cf0000|0x10000000,
			100,
			100,
			800,
			500,
			0,
			0,
			module,
			0,
		)
		if handle == 0 {
			ready <- callErr
			return
		}
		fixture.handle = handle
		user32.NewProc("ShowWindow").Call(handle, 5)
		user32.NewProc("UpdateWindow").Call(handle)
		ready <- nil

		for command := range fixture.commands {
			if command == ^uintptr(0) {
				user32.NewProc("DestroyWindow").Call(handle)
				return
			}
			user32.NewProc("ShowWindow").Call(handle, command)
			user32.NewProc("UpdateWindow").Call(handle)
		}
	}()
	if err := <-ready; err != nil {
		t.Fatalf("create WGC fixture window: %v", err)
	}
	return fixture
}

func (w *wgcFixtureWindow) show(command uintptr) {
	if w == nil || w.handle == 0 {
		return
	}
	w.commands <- command
}

func (w *wgcFixtureWindow) close() {
	if w == nil || w.handle == 0 {
		return
	}
	select {
	case <-w.done:
		return
	default:
	}
	w.commands <- ^uintptr(0)
	<-w.done
	w.handle = 0
}
