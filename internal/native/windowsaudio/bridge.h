#ifndef PARTYLAN_WINDOWS_AUDIO_BRIDGE_H
#define PARTYLAN_WINDOWS_AUDIO_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef enum PartyLANAudioStatus {
    PARTYLAN_AUDIO_STATUS_OK = 0,
    PARTYLAN_AUDIO_STATUS_UNAVAILABLE = 1,
    PARTYLAN_AUDIO_STATUS_NOT_IMPLEMENTED = 2,
    PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR = 3
} PartyLANAudioStatus;

typedef struct PartyLANAudioCapability {
    int supports_process_capture;
    const char* reason;
} PartyLANAudioCapability;

typedef enum PartyLANAudioCaptureMode {
    PARTYLAN_AUDIO_CAPTURE_INCLUDE_PROCESS_TREE = 0,
    PARTYLAN_AUDIO_CAPTURE_EXCLUDE_PROCESS_TREE = 1,
    PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX = 2
} PartyLANAudioCaptureMode;

typedef struct PartyLANAudioSessionConfig {
    uint32_t target_process_id;
    uint32_t capture_mode;
    uint32_t sample_rate;
    uint32_t channels;
    uint32_t frame_ms;
} PartyLANAudioSessionConfig;

typedef struct PartyLANAudioFrame {
    uint8_t* data;
    uint32_t data_len;
    uint32_t channels;
    uint32_t sample_rate;
    int64_t timestamp_us;
} PartyLANAudioFrame;

typedef struct PartyLANAudioSource {
    uint32_t process_id;
    int active;
    char* name;
    char* display_name;
    char* icon_data_url;
} PartyLANAudioSource;

typedef struct PartyLANAudioSourceList {
    PartyLANAudioSource* items;
    uint32_t count;
    const char* reason;
} PartyLANAudioSourceList;

PartyLANAudioStatus PartyLAN_QueryAudioCapability(PartyLANAudioCapability* capability);
const char* PartyLAN_GetLastAudioReason(void);

PartyLANAudioStatus PartyLAN_ListAudioSources(PartyLANAudioSourceList* sources);
void PartyLAN_FreeAudioSourceList(PartyLANAudioSourceList* sources);

void* PartyLAN_CreateAudioSession(void);
PartyLANAudioStatus PartyLAN_ConfigureAudioSession(void* handle, const PartyLANAudioSessionConfig* config);
PartyLANAudioStatus PartyLAN_StartAudioSession(void* handle);
PartyLANAudioStatus PartyLAN_ReadAudioFrame(void* handle, int timeout_ms, PartyLANAudioFrame* frame);
void PartyLAN_ReleaseAudioFrame(void* handle, PartyLANAudioFrame* frame);
void PartyLAN_StopAudioSession(void* handle);
void PartyLAN_DestroyAudioSession(void* handle);

#ifdef __cplusplus
}
#endif

#endif
