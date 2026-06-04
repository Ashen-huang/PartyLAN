package video

import (
	"testing"
)

func TestSoftwareEncoderDecoder_RoundTrip(t *testing.T) {
	enc := NewSoftwareEncoder()
	dec := NewSoftwareDecoder()

	width, height := 1920, 1080
	cfg := EncoderConfig{
		Codec:     CodecH264,
		Bitrate:   2000,
		Width:     width,
		Height:    height,
		FrameRate: 30,
	}

	if err := enc.Configure(cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	dec.SetDimensions(width, height)

	// Create a test frame (RGBA)
	frameData := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		frameData[i*4+0] = byte(i % 256) // R
		frameData[i*4+1] = byte((i * 2) % 256) // G
		frameData[i*4+2] = byte((i * 3) % 256) // B
		frameData[i*4+3] = 255           // A
	}

	frame := &Frame{
		Data:      frameData,
		Timestamp: 1000000,
		Width:     width,
		Height:    height,
	}

	// Encode
	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	// Decode
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Width != width {
		t.Errorf("expected width %d, got %d", width, decoded.Width)
	}
	if decoded.Height != height {
		t.Errorf("expected height %d, got %d", height, decoded.Height)
	}
	if len(decoded.Data) != width*height*4 {
		t.Errorf("expected decoded data length %d, got %d", width*height*4, len(decoded.Data))
	}
}

func TestSoftwareEncoder_KeyFrameInterval(t *testing.T) {
	enc := NewSoftwareEncoder()

	width, height := 32, 32
	cfg := EncoderConfig{
		Codec:     CodecH264,
		Bitrate:   2000,
		Width:     width,
		Height:    height,
		FrameRate: 30,
	}

	if err := enc.Configure(cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	frameData := make([]byte, width*height*4)
	for i := range frameData {
		frameData[i] = byte(i % 256)
	}

	// Encode multiple frames
	var keyFrames, deltaFrames int
	for i := 0; i < 120; i++ {
		frame := &Frame{
			Data:      frameData,
			Timestamp: int64(i * 33333),
			Width:     width,
			Height:    height,
		}

		encoded, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode[%d]: %v", i, err)
		}

		if len(encoded) > 0 {
			// Check NAL type (first byte after start code)
			if encoded[4]&0x1F == nalTypeKey&0x1F {
				keyFrames++
			} else {
				deltaFrames++
			}
		}
	}

	if keyFrames == 0 {
		t.Error("expected at least one key frame")
	}
	t.Logf("key frames: %d, delta frames: %d", keyFrames, deltaFrames)
}

func TestResizeFrame(t *testing.T) {
	srcW, srcH := 100, 100
	dstW, dstH := 50, 50

	src := make([]byte, srcW*srcH*4)
	for i := range src {
		src[i] = byte(i % 256)
	}

	dst := ResizeFrame(src, srcW, srcH, dstW, dstH)
	if len(dst) != dstW*dstH*4 {
		t.Errorf("expected size %d, got %d", dstW*dstH*4, len(dst))
	}
}

func TestListDisplays(t *testing.T) {
	displays := ListDisplays()
	if len(displays) == 0 {
		t.Skip("no displays available (headless environment)")
	}

	for i, d := range displays {
		if d.Dx() <= 0 || d.Dy() <= 0 {
			t.Errorf("display %d has invalid dimensions: %dx%d", i, d.Dx(), d.Dy())
		}
		t.Logf("display %d: %dx%d", i, d.Dx(), d.Dy())
	}
}
