//go:build !windows

package sharecatalog

import (
	"fmt"
	"image"
)

func currentProcessID() int {
	return 0
}

func listWindowCandidates() []WindowCandidate {
	return nil
}

func displayPreviewDataURL(_ int, _ image.Rectangle) string {
	return ""
}

func windowPreviewDataURL(_ WindowCandidate) string {
	return ""
}

func resolveWindowProcess(_ uintptr) (WindowProcess, error) {
	return WindowProcess{}, fmt.Errorf("window process lookup is unavailable")
}
