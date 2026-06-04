package voice

import (
	"testing"
)

func TestVAD_Silence(t *testing.T) {
	vad := NewVAD(0) // use default threshold

	silence := make([]float32, 960) // 20ms at 48kHz

	result := vad.Process(silence)
	if result {
		t.Error("silence should not trigger VAD")
	}
	if vad.IsActive() {
		t.Error("VAD should not be active for silence")
	}
}

func TestVAD_Speech(t *testing.T) {
	vad := NewVAD(0)

	// Generate a "speech-like" signal (high amplitude)
	speech := make([]float32, 960)
	for i := range speech {
		speech[i] = 0.5 // well above threshold
	}

	result := vad.Process(speech)
	if !result {
		t.Error("speech should trigger VAD")
	}
	if !vad.IsActive() {
		t.Error("VAD should be active for speech")
	}
}

func TestVAD_HoldOver(t *testing.T) {
	vad := NewVAD(0)
	holdFrames := ActiveHoldMs / 20 // 15 frames

	// Send one speech frame
	speech := make([]float32, 960)
	for i := range speech {
		speech[i] = 0.5
	}
	vad.Process(speech)

	// Send silence frames - should stay active for holdFrames
	silence := make([]float32, 960)
	for i := 0; i < holdFrames; i++ {
		vad.Process(silence)
		if !vad.IsActive() {
			t.Errorf("VAD should still be active at frame %d (hold-over)", i)
		}
	}

	// One more silence frame should deactivate
	vad.Process(silence)
	if vad.IsActive() {
		t.Error("VAD should be inactive after hold-over period")
	}
}

func TestVAD_CustomThreshold(t *testing.T) {
	vad := NewVAD(0.1) // higher threshold

	// Signal just below threshold
	low := make([]float32, 960)
	for i := range low {
		low[i] = 0.05
	}

	if vad.Process(low) {
		t.Error("signal below threshold should not trigger VAD")
	}

	// Signal above threshold
	high := make([]float32, 960)
	for i := range high {
		high[i] = 0.2
	}

	if !vad.Process(high) {
		t.Error("signal above threshold should trigger VAD")
	}
}

func TestComputeRMS(t *testing.T) {
	tests := []struct {
		name    string
		samples []float32
		wantMin float64
		wantMax float64
	}{
		{"empty", nil, 0, 0},
		{"silence", []float32{0, 0, 0}, 0, 0},
		{"ones", []float32{1, 1, 1}, 1.0, 1.0},
		{"negative", []float32{-1, -1, -1}, 1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rms := computeRMS(tt.samples)
			if rms < tt.wantMin-0.001 || rms > tt.wantMax+0.001 {
				t.Errorf("computeRMS(%s) = %f, want [%f, %f]", tt.name, rms, tt.wantMin, tt.wantMax)
			}
		})
	}
}
