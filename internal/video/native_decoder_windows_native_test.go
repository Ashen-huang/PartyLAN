//go:build windows && cgo && partylan_native_media

package video

import (
	"errors"
	"strings"
	"testing"

	"PartyLAN/internal/native/windowsmedia"
)

type fakeMediaBridge struct {
	capturerHandle uintptr
	captureStatus  windowsmedia.Status
	captureFrame   windowsmedia.CapturedFrame
	encoderHandle  uintptr
	decoderHandle  uintptr
	reason         string
	decodeStatus   windowsmedia.Status
	decodeFrame    windowsmedia.CapturedFrame

	createEncoderCalls    int
	encoderConfigureCalls int
	encoderCloseCalls     int
	destroyEncoderCalls   int
	createDecoderCalls    int
	configureCalls        int
	decodeCalls           int
	closeCalls            int
	destroyCalls          int

	lastConfig          windowsmedia.DecoderConfig
	encoderConfigs      []windowsmedia.EncoderConfig
	encoderStatuses     []windowsmedia.Status
	lastDecodeHandle    uintptr
	lastDecodeData      []byte
	lastDecodeTimestamp int64
}

func (b *fakeMediaBridge) QueryCapability() windowsmedia.Capability {
	return windowsmedia.Capability{}
}

func (b *fakeMediaBridge) LastErrorReason() string {
	return b.reason
}

func (b *fakeMediaBridge) CreateCapturer() uintptr {
	return b.capturerHandle
}

func (b *fakeMediaBridge) CreateEncoder() uintptr {
	b.createEncoderCalls++
	return b.encoderHandle
}

func (b *fakeMediaBridge) CreateDecoder() uintptr {
	b.createDecoderCalls++
	return b.decoderHandle
}

func (b *fakeMediaBridge) CapturerStart(handle uintptr, displayIndex int) windowsmedia.Status {
	return windowsmedia.StatusOK
}

func (b *fakeMediaBridge) CapturerGrabFrame(handle uintptr, timeoutMs int) (windowsmedia.CapturedFrame, windowsmedia.Status) {
	return b.captureFrame, b.captureStatus
}

func (b *fakeMediaBridge) CapturerStop(handle uintptr) {}

func (b *fakeMediaBridge) EncoderConfigure(handle uintptr, config windowsmedia.EncoderConfig) windowsmedia.Status {
	b.encoderConfigs = append(b.encoderConfigs, config)
	index := b.encoderConfigureCalls
	b.encoderConfigureCalls++
	if index < len(b.encoderStatuses) {
		return b.encoderStatuses[index]
	}
	return windowsmedia.StatusUnavailable
}

func (b *fakeMediaBridge) EncoderEncodeRGBA(handle uintptr, rgba []byte, timestampMicros int64) (windowsmedia.EncodedPacket, windowsmedia.Status) {
	return windowsmedia.EncodedPacket{}, windowsmedia.StatusUnavailable
}

func (b *fakeMediaBridge) EncoderEncodeFromCapturer(encoderHandle uintptr, capturerHandle uintptr, timeoutMs int) (windowsmedia.EncodedPacket, windowsmedia.Status) {
	return windowsmedia.EncodedPacket{}, windowsmedia.StatusUnavailable
}

func (b *fakeMediaBridge) EncoderRequestKeyFrame(handle uintptr) windowsmedia.Status {
	return windowsmedia.StatusUnavailable
}

func (b *fakeMediaBridge) EncoderClose(handle uintptr) {
	b.encoderCloseCalls++
}

func (b *fakeMediaBridge) DecoderConfigure(handle uintptr, config windowsmedia.DecoderConfig) windowsmedia.Status {
	b.configureCalls++
	b.lastConfig = config
	return windowsmedia.StatusOK
}

func (b *fakeMediaBridge) DecoderDecodeAnnexB(handle uintptr, data []byte, timestampMicros int64) (windowsmedia.CapturedFrame, windowsmedia.Status) {
	b.decodeCalls++
	b.lastDecodeHandle = handle
	b.lastDecodeData = append([]byte(nil), data...)
	b.lastDecodeTimestamp = timestampMicros
	return b.decodeFrame, b.decodeStatus
}

func (b *fakeMediaBridge) DecoderReleaseFrame(handle uintptr, frame *windowsmedia.CapturedFrame) {}

func (b *fakeMediaBridge) DecoderClose(handle uintptr) {
	b.closeCalls++
}

func (b *fakeMediaBridge) DestroyCapturer(handle uintptr) {}

func (b *fakeMediaBridge) DestroyEncoder(handle uintptr) {
	b.destroyEncoderCalls++
}

func (b *fakeMediaBridge) DestroyDecoder(handle uintptr) {
	b.destroyCalls++
}

func TestNativeDecoderConfiguresAndDecodesAnnexB(t *testing.T) {
	bridge := &fakeMediaBridge{
		decoderHandle: 42,
		decodeFrame: windowsmedia.CapturedFrame{
			Data:            []byte{1, 2, 3, 255, 4, 5, 6, 255},
			Width:           2,
			Height:          1,
			TimestampMicros: 123,
		},
	}
	decoder := newNativeDecoderWithBridge(bridge)
	decoder.SetDimensions(2, 1)

	packet := []byte{0, 0, 0, 1, 0x65}
	frame, err := decoder.Decode(packet)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}

	if bridge.createDecoderCalls != 1 {
		t.Fatalf("CreateDecoder calls = %d, want 1", bridge.createDecoderCalls)
	}
	if bridge.configureCalls != 1 {
		t.Fatalf("DecoderConfigure calls = %d, want 1", bridge.configureCalls)
	}
	if bridge.lastConfig.Width != 2 || bridge.lastConfig.Height != 1 {
		t.Fatalf("DecoderConfigure dimensions = %dx%d, want 2x1", bridge.lastConfig.Width, bridge.lastConfig.Height)
	}
	if bridge.decodeCalls != 1 || bridge.lastDecodeHandle != 42 {
		t.Fatalf("DecoderDecodeAnnexB calls/handle = %d/%d, want 1/42", bridge.decodeCalls, bridge.lastDecodeHandle)
	}
	if string(bridge.lastDecodeData) != string(packet) {
		t.Fatalf("DecoderDecodeAnnexB data = %v, want %v", bridge.lastDecodeData, packet)
	}
	if bridge.lastDecodeTimestamp <= 0 {
		t.Fatal("DecoderDecodeAnnexB timestamp was not set")
	}
	if frame.Width != 2 || frame.Height != 1 || frame.Timestamp != 123 || len(frame.Data) != 8 {
		t.Fatalf("decoded frame = %+v len=%d, want 2x1 ts=123 len=8", frame, len(frame.Data))
	}
}

func TestNativeCapturerMapsFrameTimeoutToTransientError(t *testing.T) {
	bridge := &fakeMediaBridge{
		capturerHandle: 42,
		captureStatus:  windowsmedia.StatusUnavailable,
	}
	capturer := newNativeCapturerWithBridge(bridge)
	if err := capturer.Start(CaptureConfig{DisplayIndex: 0, FrameRate: 30}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer capturer.Stop()
	_, err := capturer.GetFrame()
	if !errors.Is(err, ErrCaptureFrameUnavailable) {
		t.Fatalf("GetFrame error = %v, want transient frame-unavailable error", err)
	}
}

func TestNativeDecoderRequiresDimensionsBeforeCreatingHandle(t *testing.T) {
	bridge := &fakeMediaBridge{decoderHandle: 42}
	decoder := newNativeDecoderWithBridge(bridge)

	_, err := decoder.Decode([]byte{0, 0, 0, 1, 0x65})
	if err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("Decode error = %v, want dimensions error", err)
	}
	if bridge.createDecoderCalls != 0 {
		t.Fatalf("CreateDecoder calls = %d, want 0", bridge.createDecoderCalls)
	}
}

func TestNativeDecoderReconfiguresAfterDimensionChange(t *testing.T) {
	bridge := &fakeMediaBridge{
		decoderHandle: 42,
		decodeFrame: windowsmedia.CapturedFrame{
			Data:            []byte{1, 2, 3, 255},
			Width:           1,
			Height:          1,
			TimestampMicros: 10,
		},
	}
	decoder := newNativeDecoderWithBridge(bridge)
	decoder.SetDimensions(1, 1)
	if _, err := decoder.Decode([]byte{0, 0, 0, 1, 0x65}); err != nil {
		t.Fatalf("first Decode returned error: %v", err)
	}

	decoder.SetDimensions(2, 1)
	bridge.decodeFrame = windowsmedia.CapturedFrame{
		Data:            []byte{1, 2, 3, 255, 4, 5, 6, 255},
		Width:           2,
		Height:          1,
		TimestampMicros: 20,
	}
	if _, err := decoder.Decode([]byte{0, 0, 0, 1, 0x41}); err != nil {
		t.Fatalf("second Decode returned error: %v", err)
	}

	if bridge.createDecoderCalls != 1 {
		t.Fatalf("CreateDecoder calls = %d, want 1", bridge.createDecoderCalls)
	}
	if bridge.configureCalls != 2 {
		t.Fatalf("DecoderConfigure calls = %d, want 2", bridge.configureCalls)
	}
	if bridge.closeCalls != 1 {
		t.Fatalf("DecoderClose calls after resize = %d, want 1", bridge.closeCalls)
	}
	if bridge.lastConfig.Width != 2 || bridge.lastConfig.Height != 1 {
		t.Fatalf("last DecoderConfigure dimensions = %dx%d, want 2x1", bridge.lastConfig.Width, bridge.lastConfig.Height)
	}
}

func TestNativeEncoderRetriesInvalidInitializeWithFreshCompatibilitySession(t *testing.T) {
	bridge := &fakeMediaBridge{
		encoderHandle: 42,
		reason:        "nvEncInitializeEncoder failed: 8",
		encoderStatuses: []windowsmedia.Status{
			windowsmedia.StatusRetryable,
			windowsmedia.StatusOK,
		},
	}
	encoder := newNativeEncoderWithBridge(bridge)
	defer encoder.Close()

	err := encoder.Configure(EncoderConfig{
		Codec: CodecH264, Bitrate: 8000, Width: 1920, Height: 1080, FrameRate: 60,
	})
	if err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if bridge.createEncoderCalls != 2 || bridge.encoderConfigureCalls != 2 {
		t.Fatalf("encoder create/configure calls = %d/%d, want 2/2", bridge.createEncoderCalls, bridge.encoderConfigureCalls)
	}
	if bridge.encoderCloseCalls != 1 || bridge.destroyEncoderCalls != 1 {
		t.Fatalf("failed session close/destroy calls = %d/%d, want 1/1", bridge.encoderCloseCalls, bridge.destroyEncoderCalls)
	}
	if len(bridge.encoderConfigs) != 2 || bridge.encoderConfigs[0].CompatibilityMode || !bridge.encoderConfigs[1].CompatibilityMode {
		t.Fatalf("compatibility modes = %+v, want false then true", bridge.encoderConfigs)
	}
}

func TestNativeEncoderDoesNotRetryUnsupportedGPU(t *testing.T) {
	bridge := &fakeMediaBridge{
		encoderHandle:   42,
		reason:          "NVENC runtime not found",
		encoderStatuses: []windowsmedia.Status{windowsmedia.StatusUnsupportedGPU},
	}
	encoder := newNativeEncoderWithBridge(bridge)
	defer encoder.Close()

	err := encoder.Configure(EncoderConfig{
		Codec: CodecH264, Bitrate: 8000, Width: 1920, Height: 1080, FrameRate: 60,
	})
	if !errors.Is(err, ErrUnsupportedHostGPU) {
		t.Fatalf("Configure error = %v, want unsupported GPU", err)
	}
	if bridge.createEncoderCalls != 1 || bridge.encoderConfigureCalls != 1 {
		t.Fatalf("encoder create/configure calls = %d/%d, want 1/1", bridge.createEncoderCalls, bridge.encoderConfigureCalls)
	}
}
