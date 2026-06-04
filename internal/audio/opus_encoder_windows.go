//go:build windows && cgo

package audio

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>

#define PARTYLAN_OPUS_APPLICATION_AUDIO 2049

typedef struct OpusEncoder OpusEncoder;
typedef int16_t opus_int16;
typedef int32_t opus_int32;

typedef int (*partylan_opus_encoder_get_size_fn)(int channels);
typedef int (*partylan_opus_encoder_init_fn)(OpusEncoder* st, opus_int32 Fs, int channels, int application);
typedef opus_int32 (*partylan_opus_encode_fn)(OpusEncoder* st, const opus_int16* pcm, int frame_size, unsigned char* data, opus_int32 max_data_bytes);
typedef const char* (*partylan_opus_strerror_fn)(int error);

typedef struct PartyLANOpusAPI {
	HMODULE lib;
	partylan_opus_encoder_get_size_fn encoder_get_size;
	partylan_opus_encoder_init_fn encoder_init;
	partylan_opus_encode_fn encode;
	partylan_opus_strerror_fn strerror_fn;
} PartyLANOpusAPI;

typedef struct PartyLANOpusEncoder {
	unsigned char* state;
	int sample_rate;
	int channels;
	int max_packet_bytes;
} PartyLANOpusEncoder;

static PartyLANOpusAPI PARTYLAN_OPUS_API;
static char PARTYLAN_OPUS_REASON[256];

static void partylan_opus_set_reason(const char* reason) {
	if (reason == NULL) {
		PARTYLAN_OPUS_REASON[0] = '\0';
		return;
	}
	strncpy(PARTYLAN_OPUS_REASON, reason, sizeof(PARTYLAN_OPUS_REASON) - 1);
	PARTYLAN_OPUS_REASON[sizeof(PARTYLAN_OPUS_REASON) - 1] = '\0';
}

static int partylan_opus_load(void) {
	if (PARTYLAN_OPUS_API.lib != NULL) {
		return 1;
	}

	HMODULE lib = LoadLibraryW(L"opus.dll");
	if (lib == NULL) {
		partylan_opus_set_reason("opus.dll not found; place a libopus-compatible opus.dll next to PartyLAN.exe");
		return 0;
	}

	PARTYLAN_OPUS_API.encoder_get_size = (partylan_opus_encoder_get_size_fn)GetProcAddress(lib, "opus_encoder_get_size");
	PARTYLAN_OPUS_API.encoder_init = (partylan_opus_encoder_init_fn)GetProcAddress(lib, "opus_encoder_init");
	PARTYLAN_OPUS_API.encode = (partylan_opus_encode_fn)GetProcAddress(lib, "opus_encode");
	PARTYLAN_OPUS_API.strerror_fn = (partylan_opus_strerror_fn)GetProcAddress(lib, "opus_strerror");

	if (PARTYLAN_OPUS_API.encoder_get_size == NULL ||
		PARTYLAN_OPUS_API.encoder_init == NULL ||
		PARTYLAN_OPUS_API.encode == NULL ||
		PARTYLAN_OPUS_API.strerror_fn == NULL) {
		FreeLibrary(lib);
		memset(&PARTYLAN_OPUS_API, 0, sizeof(PARTYLAN_OPUS_API));
		partylan_opus_set_reason("opus.dll does not export the required libopus encoder symbols");
		return 0;
	}

	PARTYLAN_OPUS_API.lib = lib;
	partylan_opus_set_reason("opus.dll encoder ready");
	return 1;
}

static const char* partylan_opus_error(int code) {
	if (PARTYLAN_OPUS_API.strerror_fn == NULL) {
		return "unknown opus error";
	}
	return PARTYLAN_OPUS_API.strerror_fn(code);
}

int PartyLAN_OpusQueryCapability(void) {
	return partylan_opus_load();
}

const char* PartyLAN_OpusLastReason(void) {
	return PARTYLAN_OPUS_REASON;
}

void* PartyLAN_CreateOpusEncoder(uint32_t sample_rate, uint32_t channels, uint32_t max_packet_bytes) {
	PartyLANOpusEncoder* encoder;
	int size;
	int err;

	if (!partylan_opus_load()) {
		return NULL;
	}
	if (channels != 1 && channels != 2) {
		partylan_opus_set_reason("opus encoder requires mono or stereo input");
		return NULL;
	}
	if (sample_rate == 0 || max_packet_bytes == 0) {
		partylan_opus_set_reason("opus encoder sample rate and packet size are required");
		return NULL;
	}

	size = PARTYLAN_OPUS_API.encoder_get_size((int)channels);
	if (size <= 0) {
		partylan_opus_set_reason("opus_encoder_get_size returned an invalid size");
		return NULL;
	}

	encoder = (PartyLANOpusEncoder*)calloc(1, sizeof(PartyLANOpusEncoder));
	if (encoder == NULL) {
		partylan_opus_set_reason("failed to allocate opus encoder");
		return NULL;
	}
	encoder->state = (unsigned char*)calloc(1, (size_t)size);
	if (encoder->state == NULL) {
		free(encoder);
		partylan_opus_set_reason("failed to allocate opus encoder state");
		return NULL;
	}

	err = PARTYLAN_OPUS_API.encoder_init((OpusEncoder*)encoder->state, (opus_int32)sample_rate, (int)channels, PARTYLAN_OPUS_APPLICATION_AUDIO);
	if (err != 0) {
		const char* opus_reason = partylan_opus_error(err);
		partylan_opus_set_reason(opus_reason);
		free(encoder->state);
		free(encoder);
		return NULL;
	}

	encoder->sample_rate = (int)sample_rate;
	encoder->channels = (int)channels;
	encoder->max_packet_bytes = (int)max_packet_bytes;
	partylan_opus_set_reason("opus encoder created");
	return encoder;
}

int PartyLAN_OpusEncodePCM16(void* handle, const int16_t* pcm, uint32_t sample_values, uint8_t** out_data, uint32_t* out_len) {
	PartyLANOpusEncoder* encoder = (PartyLANOpusEncoder*)handle;
	uint8_t* packet;
	int frame_size;
	int written;

	if (out_data == NULL || out_len == NULL) {
		partylan_opus_set_reason("opus output pointers are null");
		return 0;
	}
	*out_data = NULL;
	*out_len = 0;

	if (encoder == NULL || encoder->state == NULL || pcm == NULL || sample_values == 0) {
		partylan_opus_set_reason("opus encoder input is invalid");
		return 0;
	}
	if ((sample_values % (uint32_t)encoder->channels) != 0) {
		partylan_opus_set_reason("opus input sample count must be divisible by channels");
		return 0;
	}

	packet = (uint8_t*)malloc((size_t)encoder->max_packet_bytes);
	if (packet == NULL) {
		partylan_opus_set_reason("failed to allocate opus packet");
		return 0;
	}

	frame_size = (int)(sample_values / (uint32_t)encoder->channels);
	written = (int)PARTYLAN_OPUS_API.encode((OpusEncoder*)encoder->state, (const opus_int16*)pcm, frame_size, packet, (opus_int32)encoder->max_packet_bytes);
	if (written < 0) {
		const char* opus_reason = partylan_opus_error(written);
		partylan_opus_set_reason(opus_reason);
		free(packet);
		return 0;
	}

	*out_data = packet;
	*out_len = (uint32_t)written;
	partylan_opus_set_reason("opus packet encoded");
	return 1;
}

void PartyLAN_OpusReleasePacket(uint8_t* data) {
	if (data != NULL) {
		free(data);
	}
}

void PartyLAN_DestroyOpusEncoder(void* handle) {
	PartyLANOpusEncoder* encoder = (PartyLANOpusEncoder*)handle;
	if (encoder == NULL) {
		return;
	}
	if (encoder->state != NULL) {
		free(encoder->state);
	}
	free(encoder);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeOpusEncoderHandle struct {
	handle uintptr
}

func QueryOpusCapability() OpusCapability {
	ok := C.PartyLAN_OpusQueryCapability() != 0
	return OpusCapability{
		Available: ok,
		Reason:    cOpusString(C.PartyLAN_OpusLastReason()),
	}
}

func newNativeOpusEncoder(config OpusEncoderConfig) (nativeOpusEncoder, error) {
	handle := uintptr(C.PartyLAN_CreateOpusEncoder(
		C.uint32_t(config.SampleRate),
		C.uint32_t(config.Channels),
		C.uint32_t(config.MaxPacketBytes),
	))
	if handle == 0 {
		return nil, fmt.Errorf("create opus encoder: %s", cOpusString(C.PartyLAN_OpusLastReason()))
	}
	return &nativeOpusEncoderHandle{handle: handle}, nil
}

func (e *nativeOpusEncoderHandle) EncodePCM16(samples []int16) ([]byte, error) {
	if e == nil || e.handle == 0 {
		return nil, fmt.Errorf("opus encoder is closed")
	}
	if len(samples) == 0 {
		return nil, nil
	}

	var data *C.uint8_t
	var dataLen C.uint32_t
	ok := C.PartyLAN_OpusEncodePCM16(
		unsafe.Pointer(e.handle),
		(*C.int16_t)(unsafe.Pointer(&samples[0])),
		C.uint32_t(len(samples)),
		&data,
		&dataLen,
	) != 0
	if !ok {
		return nil, fmt.Errorf("encode opus packet: %s", cOpusString(C.PartyLAN_OpusLastReason()))
	}

	packet := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))
	C.PartyLAN_OpusReleasePacket(data)
	return packet, nil
}

func (e *nativeOpusEncoderHandle) Close() {
	if e == nil || e.handle == 0 {
		return
	}
	C.PartyLAN_DestroyOpusEncoder(unsafe.Pointer(e.handle))
	e.handle = 0
}

func cOpusString(ptr *C.char) string {
	if ptr == nil {
		return ""
	}
	return C.GoString(ptr)
}
