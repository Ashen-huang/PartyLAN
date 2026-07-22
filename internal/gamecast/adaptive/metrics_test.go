package adaptive

import (
	"testing"
	"time"
)

func TestLocalMetricsSeparatesFreshCaptureFromRepeatedSends(t *testing.T) {
	start := time.Unix(5000, 0)
	metrics := NewLocalMetrics(start)
	for index := 0; index < 60; index++ {
		at := start.Add(time.Duration(index) * time.Second / 30)
		if index < 20 {
			metrics.RecordCapture(int64(index+1), at)
		}
		metrics.RecordEncode(10*time.Millisecond, 4000, index >= 20, true)
	}

	sample, ok := metrics.Snapshot(start.Add(2*time.Second), Profile{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 6000})
	if !ok {
		t.Fatal("expected two-second local sample")
	}
	if sample.FreshFPS < 9.9 || sample.FreshFPS > 10.1 || !sample.FreshFPSValid {
		t.Fatalf("fresh capture fps = %f valid=%t, want about 10", sample.FreshFPS, sample.FreshFPSValid)
	}
	if sample.EncodedFPS < 29.9 || sample.EncodedFPS > 30.1 {
		t.Fatalf("encoded fps = %f, want about 30", sample.EncodedFPS)
	}
	if sample.RepeatedRatio < 0.66 || sample.RepeatedRatio > 0.67 {
		t.Fatalf("repeated ratio = %f, want about 0.667", sample.RepeatedRatio)
	}
}

func TestLocalMetricsUsesP95EncodeBudgetAndResetsWindow(t *testing.T) {
	start := time.Unix(6000, 0)
	metrics := NewLocalMetrics(start)
	for index := 0; index < 20; index++ {
		duration := 10 * time.Millisecond
		if index >= 18 {
			duration = 30 * time.Millisecond
		}
		metrics.RecordEncode(duration, 1000, false, true)
	}
	sample, ok := metrics.Snapshot(start.Add(2*time.Second), Profile{Width: 1280, Height: 720, FPS: 30, BitrateKbps: 3500})
	if !ok {
		t.Fatal("expected local sample")
	}
	if sample.EncodeBudgetRatio < 0.89 || sample.EncodeBudgetRatio > 0.91 {
		t.Fatalf("encode budget ratio = %f, want p95 30ms / 33.3ms", sample.EncodeBudgetRatio)
	}
	if _, ok := metrics.Snapshot(start.Add(3*time.Second), Profile{FPS: 30}); ok {
		t.Fatal("window should reset after snapshot")
	}
}

func TestLocalMetricsDoesNotTreatSparseStaticUpdatesAsFreshFPSPressure(t *testing.T) {
	start := time.Unix(6500, 0)
	metrics := NewLocalMetrics(start)
	metrics.RecordCapture(1, start)
	metrics.RecordCapture(2, start.Add(time.Second))
	for index := 0; index < 60; index++ {
		metrics.RecordEncode(5*time.Millisecond, 1000, index >= 2, true)
	}
	sample, ok := metrics.Snapshot(start.Add(2*time.Second), Profile{Width: 1920, Height: 1080, FPS: 30, BitrateKbps: 6000})
	if !ok {
		t.Fatal("expected local sample")
	}
	if sample.FreshFPSValid {
		t.Fatalf("sparse static updates unexpectedly valid at %.1f fps", sample.FreshFPS)
	}
	if isLocalBad(sample, Profile{FPS: 30}) {
		t.Fatal("static updates with healthy encoder should not cause local pressure")
	}
}

func TestControllerRejectDecisionRestoresOldProfileAndFreezesAfterTwoFailures(t *testing.T) {
	start := time.Unix(7000, 0)
	controller := NewController(Profile{Width: 1920, Height: 1080, FPS: 60, BitrateKbps: 8000}, start)
	bad := LocalSample{Valid: true, EncodedFPS: 20, EncodeBudgetRatio: 1}
	var decision *Decision
	for index := 1; index <= 5; index++ {
		at := start.Add(time.Duration(index*2) * time.Second)
		bad.At = at
		decision = controller.Observe(at, &bad, nil)
	}
	if decision == nil || controller.Current().FPS != 45 {
		t.Fatalf("decision=%#v current=%#v", decision, controller.Current())
	}
	controller.Reject(*decision, start.Add(10*time.Second), "configure-failed")
	if controller.Current().FPS != 60 {
		t.Fatalf("current after reject = %#v, want original", controller.Current())
	}

	controller.cooldownUntil = start.Add(11 * time.Second)
	for index := 1; index <= 5; index++ {
		at := start.Add(time.Duration(11+index*2) * time.Second)
		bad.At = at
		decision = controller.Observe(at, &bad, nil)
	}
	if decision == nil {
		t.Fatal("expected second downshift decision")
	}
	failedAt := start.Add(21 * time.Second)
	controller.Reject(*decision, failedAt, "idr-failed")
	status := controller.Status(failedAt)
	if status.State != "frozen" || !status.CooldownUntil.Equal(failedAt.Add(30*time.Second)) {
		t.Fatalf("status after second failure = %#v", status)
	}
}
