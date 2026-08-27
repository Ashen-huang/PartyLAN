package sharecatalog

import (
	"image"
	"testing"
)

func TestDisplaySourcesBuildExpectedMetadata(t *testing.T) {
	sources := DisplaySources([]image.Rectangle{
		image.Rect(0, 0, 1920, 1080),
		image.Rect(0, 0, 1280, 720),
	})

	if len(sources) != 2 {
		t.Fatalf("display source count = %d, want 2", len(sources))
	}
	if sources[0].ID != "display:0" || sources[0].Kind != SourceKindDisplay {
		t.Fatalf("unexpected first display source: %#v", sources[0])
	}
	if sources[0].AudioDefault != AudioDefaultSystem {
		t.Fatalf("display audio default = %q, want %q", sources[0].AudioDefault, AudioDefaultSystem)
	}
	if sources[0].Subtitle != "1920x1080" {
		t.Fatalf("display subtitle = %q, want 1920x1080", sources[0].Subtitle)
	}
}

func TestWindowSourcesFilterLegacyAndOwnWindows(t *testing.T) {
	candidates := []WindowCandidate{
		{Handle: 1, Title: "", ProcessID: 22, ProcessName: "game.exe", Visible: true},
		{Handle: 2, Title: "partylan", ProcessID: 99, ProcessName: "partylan.exe", Visible: true},
		{Handle: 3, Title: "Worker", ProcessID: 20, ProcessName: "worker.exe", ClassName: "WorkerW", Visible: true},
		{Handle: 4, Title: "Chat", ProcessID: 25, ProcessName: "chat.exe", Visible: true, HasOwner: true},
		{Handle: 5, Title: "Game Window", ProcessID: 55, ProcessName: "game.exe", Visible: true, PreviewDataURL: "data:image/jpeg;base64,test"},
	}

	sources := WindowSources(candidates, 99)
	if len(sources) != 1 {
		t.Fatalf("window source count = %d, want 1", len(sources))
	}
	if sources[0].ID != "window:5" || sources[0].AudioDefault != AudioDefaultProcess {
		t.Fatalf("unexpected window source: %#v", sources[0])
	}
	if sources[0].AudioProcessName != "game.exe" {
		t.Fatalf("window audio process = %q, want game.exe", sources[0].AudioProcessName)
	}
	if sources[0].PreviewDataURL != "data:image/jpeg;base64,test" {
		t.Fatalf("window preview = %q, want preserved preview", sources[0].PreviewDataURL)
	}
}
