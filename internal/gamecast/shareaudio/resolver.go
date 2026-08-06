package shareaudio

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"PartyLAN/internal/native/windowsaudio"
)

var (
	ErrUnsafeTarget        = errors.New("unsafe shared audio target")
	ErrInvalidProcessTree  = errors.New("invalid process tree")
	ErrSnapshotUnavailable = errors.New("shared audio process snapshot unavailable")
)

const (
	ReasonWindowProcess   = "window-process"
	ReasonWindowAudio     = "window-tree-audio"
	ReasonSiblingAudio    = "sibling-audio"
	ReasonApplicationRoot = "application-root"
)

type ProcessInfo struct {
	PID            uint32
	ParentPID      uint32
	SessionID      uint32
	ExecutableName string
	ExecutablePath string
}

type AudioSession struct {
	PID uint32
}

type WindowTarget struct {
	WindowPID   uint32
	PartyLANPID uint32
}

type Target struct {
	ProcessID      uint32
	Mode           windowsaudio.CaptureMode
	Identity       string
	IdentityStable bool
	Reason         string
}

type Snapshot struct {
	Processes     []ProcessInfo
	AudioSessions []AudioSession
}

type SnapshotSource func() (Snapshot, error)

type Resolver struct {
	source SnapshotSource
}

func NewResolver() *Resolver {
	return NewResolverWithSource(collectSnapshot)
}

func NewResolverWithSource(source SnapshotSource) *Resolver {
	return &Resolver{source: source}
}

func (r *Resolver) ResolveWindow(input WindowTarget) (Target, error) {
	if r == nil || r.source == nil {
		return Target{}, ErrSnapshotUnavailable
	}
	snapshot, err := r.source()
	if err != nil {
		return Target{}, err
	}
	return ResolveWindow(input, snapshot.Processes, snapshot.AudioSessions)
}

func ResolveWindow(input WindowTarget, processes []ProcessInfo, sessions []AudioSession) (Target, error) {
	if input.WindowPID == 0 || input.PartyLANPID == 0 || input.WindowPID == input.PartyLANPID {
		return Target{}, ErrUnsafeTarget
	}
	byPID := make(map[uint32]ProcessInfo, len(processes))
	for _, process := range processes {
		if process.PID != 0 {
			byPID[process.PID] = process
		}
	}
	window, ok := byPID[input.WindowPID]
	if !ok {
		return Target{}, fmt.Errorf("%w: window process %d is unavailable", ErrUnsafeTarget, input.WindowPID)
	}
	if window.SessionID == 0 || normalizeExecutablePath(window) == "" || blockedProcess(window) {
		return Target{}, fmt.Errorf("%w: window process %d has no safe application identity", ErrUnsafeTarget, input.WindowPID)
	}
	if cycle := treeHasCycle(input.WindowPID, byPID); cycle {
		return Target{}, ErrInvalidProcessTree
	}
	if unsafe, invalid := overlapsPartyLAN(input.WindowPID, input.PartyLANPID, byPID); invalid {
		return Target{}, ErrInvalidProcessTree
	} else if unsafe {
		return Target{}, ErrUnsafeTarget
	}

	identity := applicationIdentity(window)
	windowAudio, foreignWindowAudio := classifyAudioSessionsInTree(input.WindowPID, window, sessions, byPID)
	if foreignWindowAudio {
		return Target{}, fmt.Errorf("%w: window process tree contains foreign active audio", ErrUnsafeTarget)
	}
	if windowAudio {
		return makeTarget(window, identity, ReasonWindowAudio), nil
	}

	ancestors, cycle := ancestorChain(input.WindowPID, byPID)
	if cycle {
		return Target{}, ErrInvalidProcessTree
	}
	best := window
	branch := window
	bestReason := ReasonWindowProcess
	for _, candidate := range ancestors {
		if !safeApplicationAncestor(branch, candidate, input.PartyLANPID, byPID) {
			break
		}
		matchingAudio, foreignAudio := classifyAudioSessionsInTree(candidate.PID, window, sessions, byPID)
		if foreignAudio {
			break
		}
		best = candidate
		branch = candidate
		bestReason = ReasonApplicationRoot
		if matchingAudio {
			bestReason = ReasonSiblingAudio
			break
		}
	}
	return makeTarget(best, identity, bestReason), nil
}

func makeTarget(process ProcessInfo, identity, reason string) Target {
	return Target{
		ProcessID:      process.PID,
		Mode:           windowsaudio.CaptureIncludeProcessTree,
		Identity:       identity,
		IdentityStable: identity != "",
		Reason:         reason,
	}
}

func ancestorChain(windowPID uint32, byPID map[uint32]ProcessInfo) ([]ProcessInfo, bool) {
	ancestors := make([]ProcessInfo, 0, 8)
	visited := make(map[uint32]struct{}, 8)
	current := windowPID
	for len(ancestors) < 128 {
		process, ok := byPID[current]
		if !ok || process.ParentPID == 0 {
			return ancestors, false
		}
		parent := process.ParentPID
		if _, seen := visited[parent]; seen {
			return nil, true
		}
		visited[parent] = struct{}{}
		parentProcess, ok := byPID[parent]
		if !ok {
			return ancestors, false
		}
		ancestors = append(ancestors, parentProcess)
		current = parent
	}
	return nil, true
}

func safeApplicationAncestor(window, candidate ProcessInfo, partyLANPID uint32, byPID map[uint32]ProcessInfo) bool {
	if candidate.PID == 0 || candidate.SessionID == 0 || window.SessionID == 0 || candidate.SessionID != window.SessionID {
		return false
	}
	if blockedProcess(candidate) || !sameApplicationFamily(window, candidate) {
		return false
	}
	unsafe, invalid := overlapsPartyLAN(candidate.PID, partyLANPID, byPID)
	return !unsafe && !invalid
}

func classifyAudioSessionsInTree(rootPID uint32, window ProcessInfo, sessions []AudioSession, byPID map[uint32]ProcessInfo) (bool, bool) {
	if rootPID == 0 || window.SessionID == 0 {
		return false, false
	}
	matching := false
	foreign := false
	for _, session := range sessions {
		if session.PID == 0 {
			continue
		}
		process, ok := byPID[session.PID]
		if !ok || process.SessionID != window.SessionID {
			continue
		}
		contains, known, cycle := treeContains(session.PID, rootPID, byPID)
		if contains && known && !cycle {
			if sameApplicationFamily(window, process) {
				matching = true
			} else {
				foreign = true
			}
		}
	}
	return matching, foreign
}

func sameApplicationFamily(window, candidate ProcessInfo) bool {
	if window.SessionID == 0 || candidate.SessionID == 0 || window.SessionID != candidate.SessionID {
		return false
	}
	windowPath := normalizeExecutablePath(window)
	candidatePath := normalizeExecutablePath(candidate)
	if windowPath == "" || candidatePath == "" {
		return false
	}
	if windowPath == candidatePath {
		return true
	}
	if blockedProcess(candidate) || blockedProcess(window) {
		return false
	}
	return installDirectory(windowPath) == installDirectory(candidatePath)
}

func applicationIdentity(process ProcessInfo) string {
	return normalizeExecutablePath(process)
}

func normalizeExecutablePath(process ProcessInfo) string {
	path := strings.TrimSpace(process.ExecutablePath)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "/", "\\")
	path = filepath.Clean(path)
	return strings.ToLower(strings.TrimRight(path, "\\"))
}

func installDirectory(path string) string {
	index := strings.LastIndex(path, "\\")
	if index < 0 {
		return ""
	}
	return path[:index]
}

func blockedProcess(process ProcessInfo) bool {
	path := normalizeExecutablePath(process)
	if path == "" {
		path = strings.ToLower(strings.TrimSpace(process.ExecutableName))
	}
	base := path
	if index := strings.LastIndex(base, "\\"); index >= 0 {
		base = base[index+1:]
	}
	if index := strings.LastIndex(base, "."); index > 0 {
		base = base[:index]
	}
	_, blocked := blockedProcessNames[base]
	return blocked
}

var blockedProcessNames = map[string]struct{}{
	"applicationframehost": {},
	"audiodg":              {},
	"conhost":              {},
	"cmd":                  {},
	"cscript":              {},
	"csrss":                {},
	"dwm":                  {},
	"explorer":             {},
	"fontdrvhost":          {},
	"lsass":                {},
	"mshta":                {},
	"msedgewebview2":       {},
	"partyLAN":             {},
	"partylan":             {},
	"powershell":           {},
	"pwsh":                 {},
	"registry":             {},
	"runtimebroker":        {},
	"rundll32":             {},
	"searchhost":           {},
	"services":             {},
	"settingSyncHost":      {},
	"settingsynchost":      {},
	"shellexperiencehost":  {},
	"smss":                 {},
	"svchost":              {},
	"system":               {},
	"systemsettings":       {},
	"taskhostw":            {},
	"wscript":              {},
}

func overlapsPartyLAN(candidatePID, partyLANPID uint32, byPID map[uint32]ProcessInfo) (bool, bool) {
	left, leftKnown, leftCycle := treeContains(candidatePID, partyLANPID, byPID)
	right, rightKnown, rightCycle := treeContains(partyLANPID, candidatePID, byPID)
	if leftCycle || rightCycle {
		return false, true
	}
	if !leftKnown || !rightKnown {
		return false, true
	}
	return left || right, false
}

func treeHasCycle(start uint32, byPID map[uint32]ProcessInfo) bool {
	_, _, cycle := treeContains(start, 0, byPID)
	return cycle
}

func treeContains(descendantPID, rootPID uint32, byPID map[uint32]ProcessInfo) (bool, bool, bool) {
	if descendantPID == 0 {
		return false, false, false
	}
	visited := make(map[uint32]struct{}, 16)
	current := descendantPID
	for len(visited) < 128 {
		if rootPID != 0 && current == rootPID {
			return true, true, false
		}
		if _, seen := visited[current]; seen {
			return false, false, true
		}
		visited[current] = struct{}{}
		process, ok := byPID[current]
		if !ok {
			return false, false, false
		}
		if process.ParentPID == 0 {
			return false, true, false
		}
		current = process.ParentPID
	}
	return false, false, true
}
