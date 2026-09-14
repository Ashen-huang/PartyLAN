package capturehealth

import "testing"

func TestBlackPrimaryRequiresNonBlackBackupBeforeFallback(t *testing.T) {
	monitor := NewMonitor()
	black := Probe{Valid: true, Black: true, Fingerprint: 1}
	nonBlack := Probe{Valid: true, Black: false, Fingerprint: 2}

	for index := 0; index < 2; index++ {
		if action := monitor.ObservePrimary(black, nonBlack, false); action != ActionNone {
			t.Fatalf("fallback triggered too early at %d: %s", index, action)
		}
	}
	if action := monitor.ObservePrimary(black, nonBlack, false); action != ActionUseBackup {
		t.Fatalf("action = %s, want backup after three corroborated black frames", action)
	}
}

func TestStaticUnavailablePrimaryDoesNotFallbackUntilBackupChanges(t *testing.T) {
	monitor := NewMonitor()
	unavailable := Probe{}
	static := Probe{Valid: true, Fingerprint: 10}
	for index := 0; index < 6; index++ {
		if action := monitor.ObservePrimary(unavailable, static, false); action != ActionNone {
			t.Fatalf("static desktop triggered fallback: %s", action)
		}
	}
	changed := Probe{Valid: true, Fingerprint: 11}
	if action := monitor.ObservePrimary(unavailable, changed, false); action != ActionUseBackup {
		t.Fatalf("action = %s, want fallback after sustained unavailability plus backup change", action)
	}
}

func TestFatalPrimaryUsesWorkingBackupImmediately(t *testing.T) {
	monitor := NewMonitor()
	if action := monitor.ObservePrimary(Probe{}, Probe{Valid: true, Fingerprint: 7}, true); action != ActionUseBackup {
		t.Fatalf("fatal primary action = %s", action)
	}
}

func TestBackupNeedsThreeHealthyPrimaryProbesToRecover(t *testing.T) {
	monitor := NewMonitor()
	monitor.usingBackup = true
	healthy := Probe{Valid: true, Black: false, Fingerprint: 3}
	for index := 0; index < 2; index++ {
		if action := monitor.ObserveRecovery(healthy, healthy); action != ActionNone {
			t.Fatalf("primary recovered too early at %d: %s", index, action)
		}
	}
	if action := monitor.ObserveRecovery(healthy, healthy); action != ActionUsePrimary {
		t.Fatalf("recovery action = %s, want primary", action)
	}
}

func TestProbeFrameDetectsNearBlackWithoutScanningEveryPixel(t *testing.T) {
	black := make([]byte, 64*64*4)
	for index := 3; index < len(black); index += 4 {
		black[index] = 255
	}
	if probe := ProbeFrame(black, 64, 64); !probe.Valid || !probe.Black {
		t.Fatalf("black probe = %#v", probe)
	}
	bright := append([]byte(nil), black...)
	for index := 0; index < len(bright)/4; index++ {
		bright[index*4] = 220
		bright[index*4+1] = 220
		bright[index*4+2] = 220
	}
	if probe := ProbeFrame(bright, 64, 64); !probe.Valid || probe.Black {
		t.Fatalf("bright probe = %#v", probe)
	}
}
