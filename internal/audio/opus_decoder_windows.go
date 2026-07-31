//go:build windows && cgo

package audio

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>

typedef struct OpusDecoder OpusDecoder;
typedef int16_t opus_int16;
typedef int32_t opus_int32;

typedef int (*partylan_decoder_get_size_fn)(int channels);
typedef int (*partylan_decoder_init_fn)(OpusDecoder* st, opus_int32 Fs, int channels);
typedef int (*partylan_decode_fn)(OpusDecoder* st, const unsigned char* data, opus_int32 len, opus_int16* pcm, int frame_size, int decode_fec);
typedef const char* (*partylan_decoder_strerror_fn)(int error);

typedef struct PartyLANDecoderAPI {
	HMODULE lib;
	partylan_decoder_get_size_fn decoder_get_size;
	partylan_decoder_init_fn decoder_init;
	partylan_decode_fn decode;
	partylan_decoder_strerror_fn strerror_fn;
} PartyLANDecoderAPI;

typedef struct PartyLANOpusDecoder {
	unsigned char* state;
	int channels;
} PartyLANOpusDecoder;

static PartyLANDecoderAPI PARTYLAN_DECODER_API;
static char PARTYLAN_DECODER_REASON[256];

static void partylan_decoder_set_reason(const char* reason) {
	if (reason == NULL) {
		PARTYLAN_DECODER_REASON[0] = '\0';
		return;
	}
	strncpy(PARTYLAN_DECODER_REASON, reason, sizeof(PARTYLAN_DECODER_REASON) - 1);
	PARTYLAN_DECODER_REASON[sizeof(PARTYLAN_DECODER_REASON) - 1] = '\0';
}

static int partylan_decoder_load(void) {
	if (PARTYLAN_DECODER_API.lib != NULL) {
		return 1;
	}
	HMODULE lib = LoadLibraryW(L"opus.dll");
	if (lib == NULL) {
		partylan_decoder_set_reason("opus.dll not found; place opus.dll next to PartyLAN.exe");
		return 0;
	}
	PARTYLAN_DECODER_API.decoder_get_size = (partylan_decoder_get_size_fn)GetProcAddress(lib, "opus_decoder_get_size");
	PARTYLAN_DECODER_API.decoder_init = (partylan_decoder_init_fn)GetProcAddress(lib, "opus_decoder_init");
	PARTYLAN_DECODER_API.decode = (partylan_decode_fn)GetProcAddress(lib, "opus_decode");
	PARTYLAN_DECODER_API.strerror_fn = (partylan_decoder_strerror_fn)GetProcAddress(lib, "opus_strerror");
	if (PARTYLAN_DECODER_API.decoder_get_size == NULL ||
		PARTYLAN_DECODER_API.decoder_init == NULL ||
		PARTYLAN_DECODER_API.decode == NULL ||
		PARTYLAN_DECODER_API.strerror_fn == NULL) {
		FreeLibrary(lib);
		memset(&PARTYLAN_DECODER_API, 0, sizeof(PARTYLAN_DECODER_API));
		partylan_decoder_set_reason("opus.dll does not export decoder symbols");
		return 0;
	}
	PARTYLAN_DECODER_API.lib = lib;
	partylan_decoder_set_reason("opus.dll decoder ready");
	return 1;
}

const char* PartyLAN_OpusDecoderLastReason(void) {
	return PARTYLAN_DECODER_REASON;
}

void* PartyLAN_CreateOpusDecoder(uint32_t sample_rate, uint32_t channels) {
	if (!partylan_decoder_load() || channels != 1 || sample_rate == 0) {
		return NULL;
	}
	int size = PARTYLAN_DECODER_API.decoder_get_size((int)channels);
	if (size <= 0) {
		partylan_decoder_set_reason("opus_decoder_get_size returned an invalid size");
		return NULL;
	}
	PartyLANOpusDecoder* decoder = (PartyLANOpusDecoder*)calloc(1, sizeof(PartyLANOpusDecoder));
	if (decoder == NULL) {
		partylan_decoder_set_reason("failed to allocate opus decoder");
		return NULL;
	}
	decoder->state = (unsigned char*)calloc(1, (size_t)size);
	if (decoder->state == NULL) {
		free(decoder);
		partylan_decoder_set_reason("failed to allocate opus decoder state");
		return NULL;
	}
	int err = PARTYLAN_DECODER_API.decoder_init((OpusDecoder*)decoder->state, (opus_int32)sample_rate, (int)channels);
	if (err != 0) {
		partylan_decoder_set_reason(PARTYLAN_DECODER_API.strerror_fn(err));
		free(decoder->state);
		free(decoder);
		return NULL;
	}
	decoder->channels = (int)channels;
	return decoder;
}

int PartyLAN_DecodeOpus(void* handle, const uint8_t* packet, uint32_t packet_len, uint32_t frame_size, int decode_fec, int16_t** out_samples, uint32_t* out_len) {
	PartyLANOpusDecoder* decoder = (PartyLANOpusDecoder*)handle;
	if (decoder == NULL || decoder->state == NULL || frame_size == 0 || out_samples == NULL || out_len == NULL) {
		partylan_decoder_set_reason("opus decoder input is invalid");
		return 0;
	}
	*out_samples = NULL;
	*out_len = 0;
	int16_t* samples = (int16_t*)calloc((size_t)frame_size * (size_t)decoder->channels, sizeof(int16_t));
	if (samples == NULL) {
		partylan_decoder_set_reason("failed to allocate decoded samples");
		return 0;
	}
	int decoded = PARTYLAN_DECODER_API.decode(
		(OpusDecoder*)decoder->state,
		packet_len == 0 ? NULL : packet,
		(opus_int32)packet_len,
		samples,
		(int)frame_size,
		decode_fec ? 1 : 0
	);
	if (decoded < 0) {
		partylan_decoder_set_reason(PARTYLAN_DECODER_API.strerror_fn(decoded));
		free(samples);
		return 0;
	}
	*out_samples = samples;
	*out_len = (uint32_t)(decoded * decoder->channels);
	return 1;
}

void PartyLAN_ReleaseDecodedSamples(int16_t* samples) {
	if (samples != NULL) {
		free(samples);
	}
}

void PartyLAN_DestroyOpusDecoder(void* handle) {
	PartyLANOpusDecoder* decoder = (PartyLANOpusDecoder*)handle;
	if (decoder == NULL) {
		return;
	}
	free(decoder->state);
	free(decoder);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeOpusDecoderHandle struct {
	handle uintptr
}

func newNativeOpusDecoder(config OpusDecoderConfig) (nativeOpusDecoder, error) {
	handle := uintptr(C.PartyLAN_CreateOpusDecoder(C.uint32_t(config.SampleRate), C.uint32_t(config.Channels)))
	if handle == 0 {
		return nil, fmt.Errorf("create opus decoder: %s", C.GoString(C.PartyLAN_OpusDecoderLastReason()))
	}
	return &nativeOpusDecoderHandle{handle: handle}, nil
}

func (d *nativeOpusDecoderHandle) Decode(packet []byte, frameSize int, decodeFEC bool) ([]int16, error) {
	if d == nil || d.handle == 0 {
		return nil, fmt.Errorf("opus decoder is closed")
	}
	var packetData *C.uint8_t
	if len(packet) > 0 {
		packetData = (*C.uint8_t)(unsafe.Pointer(&packet[0]))
	}
	var samples *C.int16_t
	var sampleCount C.uint32_t
	ok := C.PartyLAN_DecodeOpus(
		unsafe.Pointer(d.handle),
		packetData,
		C.uint32_t(len(packet)),
		C.uint32_t(frameSize),
		C.int(boolInt(decodeFEC)),
		&samples,
		&sampleCount,
	) != 0
	if !ok {
		return nil, fmt.Errorf("decode opus packet: %s", C.GoString(C.PartyLAN_OpusDecoderLastReason()))
	}
	decoded := append([]int16(nil), unsafe.Slice((*int16)(unsafe.Pointer(samples)), int(sampleCount))...)
	C.PartyLAN_ReleaseDecodedSamples(samples)
	return decoded, nil
}

func (d *nativeOpusDecoderHandle) Close() {
	if d == nil || d.handle == 0 {
		return
	}
	C.PartyLAN_DestroyOpusDecoder(unsafe.Pointer(d.handle))
	d.handle = 0
}
