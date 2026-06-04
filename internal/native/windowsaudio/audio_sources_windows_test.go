//go:build windows && cgo

package windowsaudio

import "testing"

func TestListActiveAudioSourcesReturnsSnapshot(t *testing.T) {
	sources, err := ListActiveAudioSources()
	if err != nil {
		t.Skipf("active audio session enumeration unavailable on this host: %v", err)
	}
	for _, source := range sources {
		if source.ProcessID == 0 {
			t.Fatalf("audio source has no process id: %#v", source)
		}
		if source.Name == "" {
			t.Fatalf("audio source has no process name: %#v", source)
		}
		if !source.Active {
			t.Fatalf("audio source should be active: %#v", source)
		}
	}
}
