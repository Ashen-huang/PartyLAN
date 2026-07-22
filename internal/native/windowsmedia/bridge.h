#ifndef PARTYLAN_WINDOWS_MEDIA_BRIDGE_H
#define PARTYLAN_WINDOWS_MEDIA_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef enum PartyLANMediaStatus {
    PARTYLAN_MEDIA_STATUS_OK = 0,
    PARTYLAN_MEDIA_STATUS_UNAVAILABLE = 1,
    PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU = 2,
    PARTYLAN_MEDIA_STATUS_NOT_IMPLEMENTED = 3,
    PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR = 4
} PartyLANMediaStatus;

typedef struct PartyLANMediaCapability {
    int supports_host;
    int supports_view;
    const char* adapter_name;
    const char* driver_name;
    const char* reason;
} PartyLANMediaCapability;

typedef struct PartyLANMediaFrame {
    uint8_t* data;
    uint32_t data_len;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
    int64_t timestamp_us;
} PartyLANMediaFrame;

typedef struct PartyLANMediaEncoderConfig {
    uint32_t width;
    uint32_t height;
    uint32_t fps;
    uint32_t bitrate_kbps;
    uint32_t pixel_format; /* 0 = RGBA, 1 = BGRA */
    uint64_t capturer_handle; /* optional PartyLANCapturer* for native direct path */
} PartyLANMediaEncoderConfig;

typedef struct PartyLANMediaDecoderConfig {
    uint32_t width;
    uint32_t height;
} PartyLANMediaDecoderConfig;

typedef struct PartyLANMediaPacket {
    uint8_t* data;
    uint32_t data_len;
    int64_t timestamp_us;
} PartyLANMediaPacket;

typedef enum PartyLANWindowCaptureStatus {
    PARTYLAN_WINDOW_CAPTURE_STATUS_OK = 0,
    PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE = 1,
    PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED = 2,
    PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE = 3,
    PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR = 4
} PartyLANWindowCaptureStatus;

typedef struct PartyLANWindowCaptureFrame {
    uint8_t* data;
    uint32_t data_len;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
    int64_t timestamp_us;
} PartyLANWindowCaptureFrame;

PartyLANMediaStatus PartyLAN_QueryMediaCapability(PartyLANMediaCapability* capability);
const char* PartyLAN_GetLastMediaReason(void);

void* PartyLAN_CreateCapturer(void);
void* PartyLAN_CreateEncoder(void);
void* PartyLAN_CreateDecoder(void);

PartyLANMediaStatus PartyLAN_CapturerStart(void* handle, int display_index);
PartyLANMediaStatus PartyLAN_CapturerGrabFrame(void* handle, int timeout_ms, PartyLANMediaFrame* frame);
void PartyLAN_CapturerReleaseFrame(void* handle, PartyLANMediaFrame* frame);
void PartyLAN_CapturerStop(void* handle);

PartyLANMediaStatus PartyLAN_EncoderConfigure(void* handle, const PartyLANMediaEncoderConfig* config);
PartyLANMediaStatus PartyLAN_EncoderEncodeRGBA(void* handle, const uint8_t* rgba, uint32_t rgba_len, int64_t timestamp_us, PartyLANMediaPacket* packet);
PartyLANMediaStatus PartyLAN_EncoderEncodeFromCapturer(void* encoder_handle, void* capturer_handle, int timeout_ms, PartyLANMediaPacket* packet);
PartyLANMediaStatus PartyLAN_EncoderRequestKeyFrame(void* handle);
void PartyLAN_EncoderReleasePacket(void* handle, PartyLANMediaPacket* packet);
void PartyLAN_EncoderClose(void* handle);

PartyLANMediaStatus PartyLAN_DecoderConfigure(void* handle, const PartyLANMediaDecoderConfig* config);
PartyLANMediaStatus PartyLAN_DecoderDecodeAnnexB(void* handle, const uint8_t* data, uint32_t data_len, int64_t timestamp_us, PartyLANMediaFrame* frame);
void PartyLAN_DecoderReleaseFrame(void* handle, PartyLANMediaFrame* frame);
void PartyLAN_DecoderClose(void* handle);

void PartyLAN_DestroyCapturer(void* handle);
void PartyLAN_DestroyEncoder(void* handle);
void PartyLAN_DestroyDecoder(void* handle);

void* PartyLAN_CreateWindowCapturer(void);
PartyLANWindowCaptureStatus PartyLAN_WindowCapturerStart(void* handle, uint64_t window_handle);
PartyLANWindowCaptureStatus PartyLAN_WindowCapturerBounds(uint64_t window_handle, uint32_t* width, uint32_t* height);
PartyLANWindowCaptureStatus PartyLAN_WindowCapturerGrabFrame(void* handle, int timeout_ms, PartyLANWindowCaptureFrame* frame);
void PartyLAN_WindowCapturerReleaseFrame(void* handle, PartyLANWindowCaptureFrame* frame);
void PartyLAN_WindowCapturerStop(void* handle);
void PartyLAN_DestroyWindowCapturer(void* handle);
const char* PartyLAN_GetWindowCaptureReason(void* handle);

#ifdef __cplusplus
}
#endif

#endif
