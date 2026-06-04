package audio

import (
	"math"
	"testing"
)

func TestPCMEncoderDecoder_RoundTrip(t *testing.T) {
	enc := NewPCMEncoder()
	dec := NewPCMDecoder()

	original := &AudioFrame{
		Samples:    []float32{0.1, -0.5, 0.8, -0.2, 0.0},
		Timestamp:  12345,
		Channels:   MonoChannels,
		SampleRate: SampleRate,
	}

	encoded, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(decoded.Samples) != len(original.Samples) {
		t.Fatalf("sample count mismatch: %d vs %d", len(decoded.Samples), len(original.Samples))
	}

	for i, s := range original.Samples {
		if math.Abs(float64(decoded.Samples[i]-s)) > 0.0001 {
			t.Errorf("sample[%d]: expected %f, got %f", i, s, decoded.Samples[i])
		}
	}

	if decoded.SampleRate != SampleRate {
		t.Errorf("expected sample rate %d, got %d", SampleRate, decoded.SampleRate)
	}
}

func TestPCMDecoder_InvalidData(t *testing.T) {
	dec := NewPCMDecoder()

	_, err := dec.Decode([]byte{0x01}) // too short
	if err == nil {
		t.Error("expected error for short data")
	}

	_, err = dec.Decode(nil)
	if err == nil {
		t.Error("expected error for nil data")
	}
}

func TestPCM16BytesToSamples(t *testing.T) {
	samples, err := PCM16BytesToSamples([]byte{0x01, 0x00, 0xff, 0xff})
	if err != nil {
		t.Fatalf("PCM16BytesToSamples: %v", err)
	}
	if len(samples) != 2 || samples[0] != 1 || samples[1] != -1 {
		t.Fatalf("unexpected samples: %#v", samples)
	}

	if _, err := PCM16BytesToSamples([]byte{0x01}); err == nil {
		t.Fatal("expected odd-length PCM16 data to fail")
	}
}

func TestOpusEncoderConfigurationValidation(t *testing.T) {
	if _, err := NewOpusEncoder(OpusEncoderConfig{Channels: 3}); err == nil {
		t.Fatal("expected invalid channel count to fail")
	}
	if _, err := NewOpusEncoder(OpusEncoderConfig{FrameMillis: 7}); err == nil {
		t.Fatal("expected invalid frame size to fail")
	}
}

func TestOpusEncoderEncodesWhenRuntimeAvailable(t *testing.T) {
	capability := QueryOpusCapability()
	if !capability.Available {
		t.Skipf("opus encoder runtime unavailable: %s", capability.Reason)
	}

	encoder, err := NewOpusEncoder(OpusEncoderConfig{})
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer encoder.Close()

	frameValues := DefaultOpusSampleRate * DefaultOpusFrameMillis / 1000 * DefaultOpusChannels
	packets, err := encoder.EncodePCM16(make([]int16, frameValues))
	if err != nil {
		t.Fatalf("EncodePCM16: %v", err)
	}
	if len(packets) != 1 || len(packets[0]) == 0 {
		t.Fatalf("expected one non-empty opus packet, got %#v", packets)
	}
}

func TestMockCapturer(t *testing.T) {
	c := NewMockCapturer()

	// Not started
	_, err := c.GetFrame()
	if err == nil {
		t.Error("expected error before start")
	}

	if err := c.Start(SourceMicrophone, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	frame, err := c.GetFrame()
	if err != nil {
		t.Fatalf("GetFrame: %v", err)
	}

	if frame.SampleRate != SampleRate {
		t.Errorf("expected sample rate %d, got %d", SampleRate, frame.SampleRate)
	}

	expectedSamples := SampleRate * FrameSizeMs / 1000
	if len(frame.Samples) != expectedSamples {
		t.Errorf("expected %d samples, got %d", expectedSamples, len(frame.Samples))
	}

	c.Stop()
}

func TestSineCapturer(t *testing.T) {
	c := NewSineCapturer(440.0) // A4

	if err := c.Start(SourceMicrophone, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	frame, err := c.GetFrame()
	if err != nil {
		t.Fatalf("GetFrame: %v", err)
	}

	// Check that samples are non-zero (sine wave)
	hasNonZero := false
	for _, s := range frame.Samples {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("expected non-zero samples from sine capturer")
	}

	// Check amplitude is within expected range
	for _, s := range frame.Samples {
		if math.Abs(float64(s)) > 0.5 {
			t.Errorf("sample amplitude %f exceeds expected range", s)
		}
	}

	c.Stop()
}

func TestSimpleMixer(t *testing.T) {
	m := NewSimpleMixer()
	dec := NewPCMDecoder()

	m.AddStream("stream1", dec)
	m.AddStream("stream2", dec)

	samplesPerFrame := SampleRate * FrameSizeMs / 1000 // 960

	// Push frames with correct size
	samples1 := make([]float32, samplesPerFrame)
	for i := range samples1 {
		samples1[i] = 0.3
	}
	samples2 := make([]float32, samplesPerFrame)
	for i := range samples2 {
		samples2[i] = 0.2
	}

	frame1 := &AudioFrame{
		Samples:    samples1,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}
	frame2 := &AudioFrame{
		Samples:    samples2,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}

	m.PushFrame("stream1", frame1)
	m.PushFrame("stream2", frame2)

	mixed, err := m.Mix()
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}

	// Should be sum of both streams (0.3 + 0.2 = 0.5)
	for i, s := range mixed.Samples {
		expected := float32(0.5)
		if math.Abs(float64(s-expected)) > 0.01 {
			t.Errorf("mixed sample[%d]: expected %f, got %f", i, expected, s)
			break
		}
	}

	// Test volume control
	m.SetVolume("stream1", 0.5)

	// Push new frames (Mix() consumes them)
	frame1 = &AudioFrame{
		Samples:    samples1,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}
	frame2 = &AudioFrame{
		Samples:    samples2,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}
	m.PushFrame("stream1", frame1)
	m.PushFrame("stream2", frame2)

	mixed, _ = m.Mix()
	for i, s := range mixed.Samples {
		expected := float32(0.35) // 0.3*0.5 + 0.2
		if math.Abs(float64(s-expected)) > 0.01 {
			t.Errorf("mixed with volume[%d]: expected %f, got %f", i, expected, s)
			break
		}
	}

	m.RemoveStream("stream1")
	m.RemoveStream("stream2")
}

func TestSimpleMixer_SoftClipping(t *testing.T) {
	m := NewSimpleMixer()
	dec := NewPCMDecoder()

	m.AddStream("loud", dec)

	samplesPerFrame := SampleRate * FrameSizeMs / 1000
	samples := make([]float32, samplesPerFrame)
	samples[0] = 1.5
	samples[1] = -1.8
	samples[2] = 0.5

	frame := &AudioFrame{
		Samples:    samples,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}
	m.PushFrame("loud", frame)

	mixed, _ := m.Mix()

	if mixed.Samples[0] != 1.0 {
		t.Errorf("expected soft clip to 1.0, got %f", mixed.Samples[0])
	}
	if mixed.Samples[1] != -1.0 {
		t.Errorf("expected soft clip to -1.0, got %f", mixed.Samples[1])
	}
	if mixed.Samples[2] != 0.5 {
		t.Errorf("expected 0.5 (no clipping), got %f", mixed.Samples[2])
	}
}

func BenchmarkPCMEncode(b *testing.B) {
	enc := NewPCMEncoder()
	samples := make([]float32, 960) // 20ms at 48kHz
	for i := range samples {
		samples[i] = float32(math.Sin(float64(i) * 0.1))
	}
	frame := &AudioFrame{
		Samples:    samples,
		SampleRate: SampleRate,
		Channels:   MonoChannels,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Encode(frame)
	}
}
