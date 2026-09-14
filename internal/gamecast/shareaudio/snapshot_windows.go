//go:build windows

package shareaudio

import (
	"fmt"
	"strings"

	"PartyLAN/internal/native/windowsaudio"

	ps "github.com/mitchellh/go-ps"
	"golang.org/x/sys/windows"
)

const processImagePathBufferSize = 32768

func collectSnapshot() (Snapshot, error) {
	processes, err := ps.Processes()
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: enumerate processes: %v", ErrSnapshotUnavailable, err)
	}
	processSnapshot := buildProcessSnapshot(processes, queryProcessIdentity)

	sources, err := windowsaudio.ListActiveAudioSources()
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: enumerate active audio sessions: %v", ErrSnapshotUnavailable, err)
	}
	return Snapshot{Processes: processSnapshot, AudioSessions: buildAudioSessionSnapshot(sources)}, nil
}

func buildAudioSessionSnapshot(sources []windowsaudio.AudioSource) []AudioSession {
	audioSessions := make([]AudioSession, 0, len(sources))
	seen := make(map[uint32]struct{}, len(sources))
	for _, source := range sources {
		if !source.Active || source.ProcessID == 0 {
			continue
		}
		if _, exists := seen[source.ProcessID]; exists {
			continue
		}
		seen[source.ProcessID] = struct{}{}
		audioSessions = append(audioSessions, AudioSession{PID: source.ProcessID})
	}
	return audioSessions
}

type processIdentityQuery func(uint32) (string, uint32)

func buildProcessSnapshot(processes []ps.Process, query processIdentityQuery) []ProcessInfo {
	processSnapshot := make([]ProcessInfo, 0, len(processes))
	for _, process := range processes {
		if process == nil || process.Pid() <= 0 {
			continue
		}
		pid := uint32(process.Pid())
		path := ""
		sessionID := uint32(0)
		if query != nil {
			path, sessionID = query(pid)
		}
		parentPID := uint32(0)
		if process.PPid() > 0 {
			parentPID = uint32(process.PPid())
		}
		processSnapshot = append(processSnapshot, ProcessInfo{
			PID:            pid,
			ParentPID:      parentPID,
			SessionID:      sessionID,
			ExecutableName: strings.TrimSpace(process.Executable()),
			ExecutablePath: path,
		})
	}
	return processSnapshot
}

func queryProcessIdentity(pid uint32) (string, uint32) {
	var sessionID uint32
	_ = windows.ProcessIdToSessionId(pid, &sessionID)

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", sessionID
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, processImagePathBufferSize)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil || size == 0 {
		return "", sessionID
	}
	return windows.UTF16ToString(buffer[:size]), sessionID
}
