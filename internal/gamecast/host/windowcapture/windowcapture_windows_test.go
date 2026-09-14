//go:build windows

package windowcapture

import (
	"errors"
	"testing"
)

func TestStartClassifiesDestroyedWindowHandle(t *testing.T) {
	capturer := New(^uintptr(0), 30)
	if err := capturer.Start(); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("Start(invalid handle) error = %v, want ErrWindowClosed", err)
	}
}
