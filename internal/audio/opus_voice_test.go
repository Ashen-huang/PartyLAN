package audio

import (
	"errors"
	"math"
	"testing"
)

type fakeNativeDecoder struct {
	closed bool
}

func (d *fakeNativeDecoder) Decode(_ []byte, frameSize int, _ bool) ([]int16, error) {
	if d.closed {
		return nil, errors.New("closed")
	}
	return make([]int16, frameSize), nil
}

func (d *fakeNativeDecoder) Close() { d.closed = true }

func TestVoiceOpusEncoderConfiguration(t *testing.T) {
	config := VoiceOpusEncoderConfig()
	if config.SampleRate != 48000 || config.Channels != 1 || config.FrameMillis != 20 ||
		config.Application != OpusApplicationVoIP || config.Bitrate != 24000 ||
		!config.DTX || !config.InbandFEC || config.PacketLossPerc != 10 {
		t.Fatalf("voice opus config = %#v", config)
	}
}

func TestOpusDecoderUsesMonoTwentyMillisecondFramesAndRejectsAfterClose(t *testing.T) {
	native := &fakeNativeDecoder{}
	decoder := newOpusDecoderWithNative(OpusDecoderConfig{}, native)
	samples, err := decoder.Decode([]byte{1})
	if err != nil || len(samples) != 960 {
		t.Fatalf("Decode samples=%d err=%v", len(samples), err)
	}
	concealed, err := decoder.ConcealLoss()
	if err != nil || len(concealed) != 960 {
		t.Fatalf("ConcealLoss samples=%d err=%v", len(concealed), err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := decoder.Decode([]byte{1}); err == nil {
		t.Fatal("decode after close succeeded")
	}
}

func TestVoiceMixerSupportsNineteenStreamsClipsAndMutesPlayback(t *testing.T) {
	mixer := NewVoiceMixer()
	for index := 0; index < MaxVoiceRemoteStreams; index++ {
		id := string(rune('a' + index))
		if err := mixer.AddStream(id); err != nil {
			t.Fatalf("AddStream %d: %v", index, err)
		}
		frame := make([]int16, 960)
		frame[0] = 4000
		if err := mixer.PushFrame(id, frame); err != nil {
			t.Fatalf("PushFrame %d: %v", index, err)
		}
	}
	if err := mixer.AddStream("overflow"); !errors.Is(err, ErrVoiceMixerFull) {
		t.Fatalf("overflow error = %v", err)
	}
	mixed, err := mixer.Mix()
	if err != nil || len(mixed) != 960 || mixed[0] != 32767 {
		t.Fatalf("mixed[0]=%d len=%d err=%v", mixed[0], len(mixed), err)
	}
	mixer.SetMuted(true)
	frame := make([]int16, 960)
	frame[0] = 12000
	if err := mixer.PushFrame("a", frame); err != nil {
		t.Fatalf("push muted frame: %v", err)
	}
	muted, err := mixer.Mix()
	if err != nil || muted[0] != 0 {
		t.Fatalf("muted mix first=%d err=%v", muted[0], err)
	}
	mixer.Close()
	if _, err := mixer.Mix(); !errors.Is(err, ErrVoiceMixerClosed) {
		t.Fatalf("mix after close error = %v", err)
	}
}

func TestVoiceOpusRoundTripFECAndPacketLossConcealmentWhenRuntimeAvailable(t *testing.T) {
	capability := QueryOpusCapability()
	if !capability.Available {
		t.Skipf("opus runtime unavailable: %s", capability.Reason)
	}
	encoder, err := NewOpusEncoder(VoiceOpusEncoderConfig())
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer encoder.Close()
	decoder, err := NewOpusDecoder(OpusDecoderConfig{})
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer decoder.Close()

	frame := make([]int16, VoiceFrameSamples)
	for index := range frame {
		frame[index] = int16(math.Sin(float64(index)*0.08) * 12000)
	}
	packets, err := encoder.EncodePCM16(frame)
	if err != nil || len(packets) != 1 {
		t.Fatalf("EncodePCM16 packets=%d err=%v", len(packets), err)
	}
	decoded, err := decoder.Decode(packets[0])
	if err != nil || len(decoded) != VoiceFrameSamples {
		t.Fatalf("Decode samples=%d err=%v", len(decoded), err)
	}
	concealed, err := decoder.ConcealLoss()
	if err != nil || len(concealed) != VoiceFrameSamples {
		t.Fatalf("ConcealLoss samples=%d err=%v", len(concealed), err)
	}
	packets, err = encoder.EncodePCM16(frame)
	if err != nil || len(packets) != 1 {
		t.Fatalf("second EncodePCM16 packets=%d err=%v", len(packets), err)
	}
	recovered, err := decoder.DecodeFEC(packets[0])
	if err != nil || len(recovered) != VoiceFrameSamples {
		t.Fatalf("DecodeFEC samples=%d err=%v", len(recovered), err)
	}
}
