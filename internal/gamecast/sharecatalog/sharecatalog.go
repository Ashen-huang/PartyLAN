package sharecatalog

import (
	"fmt"
	"image"
	"sort"
	"strings"

	"PartyLAN/internal/video"
)

type SourceKind string

const (
	SourceKindDisplay SourceKind = "display"
	SourceKindWindow  SourceKind = "window"
)

type AudioDefault string

const (
	AudioDefaultNone    AudioDefault = "none"
	AudioDefaultSystem  AudioDefault = "system"
	AudioDefaultProcess AudioDefault = "process"
)

type Source struct {
	ID               string       `json:"id"`
	Kind             SourceKind   `json:"kind"`
	Label            string       `json:"label"`
	Subtitle         string       `json:"subtitle,omitempty"`
	DisplayIndex     int          `json:"displayIndex,omitempty"`
	WindowHandle     string       `json:"windowHandle,omitempty"`
	ProcessID        int          `json:"processId,omitempty"`
	ProcessName      string       `json:"processName,omitempty"`
	IconDataURL      string       `json:"iconDataUrl,omitempty"`
	PreviewDataURL   string       `json:"previewDataUrl,omitempty"`
	AudioDefault     AudioDefault `json:"audioDefault"`
	AudioProcessName string       `json:"audioProcessName,omitempty"`
}

type WindowCandidate struct {
	Handle         uintptr
	Title          string
	ProcessID      uint32
	ProcessName    string
	ClassName      string
	Visible        bool
	HasOwner       bool
	ExStyle        uint32
	IconDataURL    string
	PreviewDataURL string
}

type WindowProcess struct {
	ProcessID   uint32
	ProcessName string
}

func ResolveWindowProcess(handle uintptr) (WindowProcess, error) {
	if handle == 0 {
		return WindowProcess{}, fmt.Errorf("window handle is required")
	}
	return resolveWindowProcess(handle)
}

func ListSources() []Source {
	displays := video.ListDisplays()
	sources := DisplaySources(displays)
	for index := range sources {
		sources[index].PreviewDataURL = displayPreviewDataURL(index, displays[index])
	}
	sources = append(sources, WindowSources(listWindowCandidates(), uint32(currentProcessID()))...)
	return sources
}

func DisplaySources(displays []image.Rectangle) []Source {
	out := make([]Source, 0, len(displays))
	for index, bounds := range displays {
		width := bounds.Dx()
		height := bounds.Dy()
		out = append(out, Source{
			ID:           fmt.Sprintf("display:%d", index),
			Kind:         SourceKindDisplay,
			Label:        fmt.Sprintf("整个屏幕 %d", index+1),
			Subtitle:     fmt.Sprintf("%dx%d", width, height),
			DisplayIndex: index,
			AudioDefault: AudioDefaultSystem,
		})
	}
	return out
}

func WindowSources(candidates []WindowCandidate, currentPID uint32) []Source {
	out := make([]Source, 0, len(candidates))
	for _, candidate := range candidates {
		if !shouldIncludeWindow(candidate, currentPID) {
			continue
		}
		processName := strings.TrimSpace(candidate.ProcessName)
		title := strings.TrimSpace(candidate.Title)
		if processName == "" {
			processName = fmt.Sprintf("PID %d", candidate.ProcessID)
		}
		out = append(out, Source{
			ID:               fmt.Sprintf("window:%d", candidate.Handle),
			Kind:             SourceKindWindow,
			Label:            title,
			Subtitle:         processName,
			WindowHandle:     fmt.Sprintf("%d", candidate.Handle),
			ProcessID:        int(candidate.ProcessID),
			ProcessName:      processName,
			IconDataURL:      candidate.IconDataURL,
			PreviewDataURL:   windowPreviewDataURL(candidate),
			AudioDefault:     AudioDefaultProcess,
			AudioProcessName: processName,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(out[i].Label))
		right := strings.ToLower(strings.TrimSpace(out[j].Label))
		if left == right {
			return out[i].ProcessID < out[j].ProcessID
		}
		return left < right
	})

	return out
}

func shouldIncludeWindow(candidate WindowCandidate, currentPID uint32) bool {
	title := strings.TrimSpace(candidate.Title)
	if title == "" || !candidate.Visible || candidate.Handle == 0 {
		return false
	}
	if candidate.ProcessID == 0 || candidate.ProcessID == currentPID {
		return false
	}
	if candidate.HasOwner {
		return false
	}
	className := strings.TrimSpace(candidate.ClassName)
	switch className {
	case "Shell_TrayWnd", "Progman", "WorkerW":
		return false
	}
	if candidate.ExStyle&windowExStyleToolWindow != 0 {
		return false
	}
	if candidate.ExStyle&windowExStyleNoActivate != 0 {
		return false
	}
	return true
}

const (
	windowExStyleToolWindow = 0x00000080
	windowExStyleNoActivate = 0x08000000
)
