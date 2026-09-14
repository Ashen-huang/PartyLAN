//go:build windows

package shareaudio

import (
	"testing"

	"PartyLAN/internal/native/windowsaudio"

	ps "github.com/mitchellh/go-ps"
)

func TestBuildAudioSessionSnapshotKeepsOnlyUniqueActiveProcesses(t *testing.T) {
	sessions := buildAudioSessionSnapshot([]windowsaudio.AudioSource{
		{ProcessID: 100, Active: true},
		{ProcessID: 100, Active: true},
		{ProcessID: 200, Active: false},
		{ProcessID: 0, Active: true},
		{ProcessID: 300, Active: true},
	})
	if len(sessions) != 2 || sessions[0].PID != 100 || sessions[1].PID != 300 {
		t.Fatalf("audio sessions = %#v, want unique active PIDs 100 and 300", sessions)
	}
}

func TestBuildProcessSnapshotPreservesProcessesWithPartialIdentity(t *testing.T) {
	processes := []ps.Process{
		snapshotTestProcess{pid: 100, parentPID: 10, executable: "player.exe"},
		snapshotTestProcess{pid: 200, parentPID: -1, executable: "protected.exe"},
		snapshotTestProcess{pid: 0, parentPID: 0, executable: "invalid.exe"},
		nil,
	}
	queries := 0
	snapshot := buildProcessSnapshot(processes, func(pid uint32) (string, uint32) {
		queries++
		if pid == 100 {
			return `C:\Apps\Player\player.exe`, 3
		}
		return "", 3
	})

	if queries != 2 {
		t.Fatalf("identity queries = %d, want 2 valid process IDs", queries)
	}
	if len(snapshot) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(snapshot))
	}
	if snapshot[0].PID != 100 || snapshot[0].ParentPID != 10 || snapshot[0].SessionID != 3 || snapshot[0].ExecutablePath == "" {
		t.Fatalf("complete process snapshot = %#v", snapshot[0])
	}
	if snapshot[1].PID != 200 || snapshot[1].ParentPID != 0 || snapshot[1].ExecutableName != "protected.exe" {
		t.Fatalf("partial process snapshot = %#v", snapshot[1])
	}
	if snapshot[1].ExecutablePath != "" || snapshot[1].SessionID != 3 {
		t.Fatalf("partial identity should preserve available fields: %#v", snapshot[1])
	}
}

type snapshotTestProcess struct {
	pid        int
	parentPID  int
	executable string
}

func (p snapshotTestProcess) Pid() int           { return p.pid }
func (p snapshotTestProcess) PPid() int          { return p.parentPID }
func (p snapshotTestProcess) Executable() string { return p.executable }
