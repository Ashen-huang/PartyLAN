//go:build windows

package main

import (
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowShareFixture struct {
	handle  uintptr
	thread  uint32
	ready   chan error
	command chan uintptr
	done    chan struct{}
}

func newWindowShareFixture(t *testing.T) *windowShareFixture {
	t.Helper()
	fixture := &windowShareFixture{
		ready:   make(chan error, 1),
		command: make(chan uintptr),
		done:    make(chan struct{}),
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(fixture.done)

		user32 := windows.NewLazySystemDLL("user32.dll")
		kernel32 := windows.NewLazySystemDLL("kernel32.dll")
		className, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("PartyLAN minimized window share fixture")
		module, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
		threadID, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
		fixture.thread = uint32(threadID)
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
			fixture.ready <- callErr
			return
		}
		fixture.handle = handle
		user32.NewProc("ShowWindow").Call(handle, 5)
		user32.NewProc("UpdateWindow").Call(handle)
		fixture.ready <- nil

		var message struct {
			hwnd    uintptr
			message uint32
			wParam  uintptr
			lParam  uintptr
			time    uint32
			pt      struct{ x, y int32 }
		}
		getMessage := user32.NewProc("GetMessageW")
		translateMessage := user32.NewProc("TranslateMessage")
		dispatchMessage := user32.NewProc("DispatchMessageW")
		for {
			result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&message)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	if err := <-fixture.ready; err != nil {
		t.Fatalf("create window fixture: %v", err)
	}
	t.Cleanup(func() {
		user32 := windows.NewLazySystemDLL("user32.dll")
		user32.NewProc("PostMessageW").Call(fixture.handle, 0x0010, 0, 0)
		time.Sleep(20 * time.Millisecond)
		user32.NewProc("PostThreadMessageW").Call(uintptr(fixture.thread), 0x0012, 0, 0)
		select {
		case <-fixture.done:
		case <-time.After(time.Second):
			t.Error("window fixture thread did not stop")
		}
	})
	return fixture
}

func TestResolveWindowShareSelectionRestoresMinimizedWindow(t *testing.T) {
	fixture := newWindowShareFixture(t)
	user32 := windows.NewLazySystemDLL("user32.dll")
	user32.NewProc("ShowWindowAsync").Call(fixture.handle, 6)
	isIconic := user32.NewProc("IsIconic")
	deadline := time.Now().Add(time.Second)
	minimized := false
	for time.Now().Before(deadline) {
		iconic, _, _ := isIconic.Call(fixture.handle)
		if iconic != 0 {
			minimized = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !minimized {
		t.Fatal("window fixture did not enter the minimized state")
	}

	_, width, height, _, _, err := resolveWindowShareSelection(fmt.Sprintf("window:%d", fixture.handle))
	if err != nil {
		t.Fatalf("resolve minimized window: %v", err)
	}
	iconic, _, _ := isIconic.Call(fixture.handle)
	if iconic != 0 {
		t.Fatal("window remained minimized after share preparation")
	}
	var bounds struct {
		left   int32
		top    int32
		right  int32
		bottom int32
	}
	result, _, callErr := user32.NewProc("GetWindowRect").Call(fixture.handle, uintptr(unsafe.Pointer(&bounds)))
	if result == 0 {
		t.Fatalf("read restored window bounds: %v", callErr)
	}
	actualWidth := int(bounds.right - bounds.left)
	actualHeight := int(bounds.bottom - bounds.top)
	if width != actualWidth || height != actualHeight {
		t.Fatalf("resolved restored dimensions = %dx%d, GetWindowRect = %dx%d", width, height, actualWidth, actualHeight)
	}
	if width < 640 || height < 100 {
		t.Fatalf("resolved minimized window dimensions = %dx%d, want restored content size", width, height)
	}
}
