package shareaudio

import (
	"errors"
	"testing"

	"PartyLAN/internal/native/windowsaudio"
)

func TestResolveWindowUsesWindowTreeWhenItContainsActiveAudio(t *testing.T) {
	processes := []ProcessInfo{
		process(10, 0, 1, `C:\Windows\explorer.exe`),
		process(100, 10, 1, `C:\Apps\Player\player.exe`),
		process(110, 100, 1, `C:\Apps\Player\player.exe`),
		process(111, 110, 1, `C:\Apps\Player\audio.exe`),
		process(500, 10, 1, `C:\PartyLAN\PartyLAN.exe`),
	}

	target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 111}})
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	if target.ProcessID != 110 || target.Mode != windowsaudio.CaptureIncludeProcessTree {
		t.Fatalf("target = %#v, want window process tree 110", target)
	}
	if target.Identity == "" {
		t.Fatal("expected stable application identity")
	}
}

func TestResolveWindowUsesNearestSafeApplicationRootForSiblingAudio(t *testing.T) {
	processes := []ProcessInfo{
		process(10, 0, 1, `C:\Windows\explorer.exe`),
		process(100, 10, 1, `C:\Apps\Browser\browser.exe`),
		process(110, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(120, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(500, 10, 1, `C:\PartyLAN\PartyLAN.exe`),
	}

	target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	if target.ProcessID != 100 {
		t.Fatalf("target PID = %d, want nearest shared application root 100", target.ProcessID)
	}
	if target.Reason != ReasonSiblingAudio {
		t.Fatalf("target reason = %q, want %q", target.Reason, ReasonSiblingAudio)
	}
}

func TestResolveWindowUsesOneApplicationRootForMultipleAudioSessions(t *testing.T) {
	processes := []ProcessInfo{
		process(100, 0, 1, `C:\Apps\Browser\browser.exe`),
		process(110, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(120, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(130, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
	}

	target, err := ResolveWindow(
		WindowTarget{WindowPID: 110, PartyLANPID: 500},
		processes,
		[]AudioSession{{PID: 120}, {PID: 130}},
	)
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	if target.ProcessID != 100 {
		t.Fatalf("target PID = %d, want common application root 100", target.ProcessID)
	}
}

func TestResolveWindowUsesHighestSafeApplicationRootBeforeAudioStarts(t *testing.T) {
	processes := []ProcessInfo{
		process(10, 0, 1, `C:\Windows\explorer.exe`),
		process(100, 10, 1, `C:\Apps\Browser\browser.exe`),
		process(105, 100, 1, `C:\Apps\Browser\browser.exe`),
		process(110, 105, 1, `C:\Apps\Browser\browser.exe`),
		process(500, 10, 1, `C:\PartyLAN\PartyLAN.exe`),
	}

	target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	if target.ProcessID != 100 || target.Reason != ReasonApplicationRoot {
		t.Fatalf("target = %#v, want highest safe application root 100", target)
	}
}

func TestResolveWindowAllowsDifferentExecutablesOnlyInsideSameApplicationDirectory(t *testing.T) {
	t.Run("same directory", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, `C:\Games\Example\launcher.exe`),
			process(110, 100, 1, `C:\Games\Example\game.exe`),
			process(120, 100, 1, `C:\Games\Example\audio-helper.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}

		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 100 {
			t.Fatalf("target PID = %d, want same-directory application root 100", target.ProcessID)
		}
	})

	t.Run("different directory", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, `C:\Launchers\launcher.exe`),
			process(110, 100, 1, `C:\Games\Example\game.exe`),
			process(120, 100, 1, `C:\Launchers\audio-helper.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}

		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 110 || target.Reason != ReasonWindowProcess {
			t.Fatalf("target = %#v, want safe window-only fallback", target)
		}
	})
}

func TestResolveWindowDoesNotCrossSystemOrSessionBoundaries(t *testing.T) {
	tests := map[string][]ProcessInfo{
		"explorer": {
			process(10, 0, 1, `C:\Windows\explorer.exe`),
			process(110, 10, 1, `C:\Apps\Player\player.exe`),
			process(120, 10, 1, `C:\Apps\Player\player.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		},
		"other session": {
			process(100, 0, 2, `C:\Apps\Player\player.exe`),
			process(110, 100, 1, `C:\Apps\Player\player.exe`),
			process(120, 100, 2, `C:\Apps\Player\player.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		},
	}

	for name, processes := range tests {
		t.Run(name, func(t *testing.T) {
			target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
			if err != nil {
				t.Fatalf("ResolveWindow: %v", err)
			}
			if target.ProcessID != 110 {
				t.Fatalf("target PID = %d, want window-only fallback 110", target.ProcessID)
			}
		})
	}
}

func TestResolveWindowDoesNotPromoteAcrossGenericLauncherOrForeignAudio(t *testing.T) {
	t.Run("generic launcher", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, `C:\Apps\Tool\pwsh.exe`),
			process(110, 100, 1, `C:\Apps\Tool\tool.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 110 || target.Reason != ReasonWindowProcess {
			t.Fatalf("target = %#v, want window-only target below generic launcher", target)
		}
	})

	t.Run("foreign active sibling", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, `C:\Apps\Browser\browser.exe`),
			process(110, 100, 1, `C:\Apps\Browser\browser.exe`),
			process(120, 100, 1, `C:\Other\music.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 110 || target.Reason != ReasonWindowProcess {
			t.Fatalf("target = %#v, want window-only target excluding foreign sibling audio", target)
		}
	})
}

func TestResolveWindowRejectsPartyLANTreeOverlap(t *testing.T) {
	t.Run("window belongs to PartyLAN", func(t *testing.T) {
		processes := []ProcessInfo{
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
			process(510, 500, 1, `C:\PartyLAN\msedgewebview2.exe`),
		}
		_, err := ResolveWindow(WindowTarget{WindowPID: 510, PartyLANPID: 500}, processes, nil)
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("error = %v, want ErrUnsafeTarget", err)
		}
	})

	t.Run("PartyLAN belongs to capture target", func(t *testing.T) {
		processes := []ProcessInfo{
			process(110, 0, 1, `C:\Apps\Host\host.exe`),
			process(500, 110, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		_, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("error = %v, want ErrUnsafeTarget", err)
		}
	})

	t.Run("unsafe shared ancestor falls back to disjoint window", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, `C:\Apps\Browser\browser.exe`),
			process(110, 100, 1, `C:\Apps\Browser\browser.exe`),
			process(120, 100, 1, `C:\Apps\Browser\browser.exe`),
			process(500, 100, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, []AudioSession{{PID: 120}})
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 110 {
			t.Fatalf("target PID = %d, want disjoint window PID 110", target.ProcessID)
		}
	})
}

func TestResolveWindowHandlesUnknownPathsAndRejectsCycles(t *testing.T) {
	t.Run("unknown window path disables audio target", func(t *testing.T) {
		processes := []ProcessInfo{
			process(110, 100, 1, ""),
			process(100, 0, 1, `C:\Apps\Player\player.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		_, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("error = %v, want ErrUnsafeTarget for unknown window identity", err)
		}
	})

	t.Run("blocked window process disables audio target", func(t *testing.T) {
		processes := []ProcessInfo{
			process(110, 10, 1, `C:\Windows\System32\RuntimeBroker.exe`),
			process(10, 0, 1, `C:\Windows\explorer.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		_, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("error = %v, want ErrUnsafeTarget for blocked window process", err)
		}
	})

	t.Run("unknown parent path", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 0, 1, ""),
			process(110, 100, 1, `C:\Apps\Player\player.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		target, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if err != nil {
			t.Fatalf("ResolveWindow: %v", err)
		}
		if target.ProcessID != 110 {
			t.Fatalf("target PID = %d, want window-only fallback 110", target.ProcessID)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		processes := []ProcessInfo{
			process(100, 110, 1, `C:\Apps\Player\player.exe`),
			process(110, 100, 1, `C:\Apps\Player\player.exe`),
			process(500, 0, 1, `C:\PartyLAN\PartyLAN.exe`),
		}
		_, err := ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}, processes, nil)
		if !errors.Is(err, ErrInvalidProcessTree) {
			t.Fatalf("error = %v, want ErrInvalidProcessTree", err)
		}
	})
}

func TestResolverRefreshesProcessAndAudioSnapshotsForEachWindowResolution(t *testing.T) {
	reads := 0
	resolver := NewResolverWithSource(func() (Snapshot, error) {
		reads++
		return Snapshot{
			Processes: []ProcessInfo{
				process(100, 0, 1, `C:\Apps\Player\player.exe`),
				process(110, 100, 1, `C:\Apps\Player\player.exe`),
			},
		}, nil
	})

	for range 2 {
		if _, err := resolver.ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}); err == nil {
			// The fake snapshot deliberately omits PartyLAN; the resolver must
			// still report an unsafe target rather than inventing a tree boundary.
			t.Fatal("expected missing PartyLAN process to remain unsafe")
		}
	}
	if reads != 2 {
		t.Fatalf("snapshot reads = %d, want one fresh read per resolution", reads)
	}
}

func TestResolverPropagatesUnavailableSnapshot(t *testing.T) {
	resolver := NewResolverWithSource(func() (Snapshot, error) {
		return Snapshot{}, ErrSnapshotUnavailable
	})
	if _, err := resolver.ResolveWindow(WindowTarget{WindowPID: 110, PartyLANPID: 500}); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("error = %v, want ErrSnapshotUnavailable", err)
	}
}

func process(pid, parentPID, sessionID uint32, executablePath string) ProcessInfo {
	return ProcessInfo{
		PID:            pid,
		ParentPID:      parentPID,
		SessionID:      sessionID,
		ExecutablePath: executablePath,
	}
}
