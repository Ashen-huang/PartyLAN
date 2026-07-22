package windowcapture

import "testing"

func TestStubConstructorAcceptsParameters(t *testing.T) {
	capturer := New(123, 30)
	if capturer == nil {
		t.Fatal("expected capturer instance")
	}
}
