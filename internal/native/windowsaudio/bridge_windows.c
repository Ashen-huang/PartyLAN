#include "bridge.h"

#define COBJMACROS
#include <windows.h>
#include <initguid.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <audiopolicy.h>
#include <objbase.h>
#include <objidl.h>
#include <propidl.h>
#include <shellapi.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#define PARTYLAN_AUDIO_DEFAULT_SAMPLE_RATE 48000u
#define PARTYLAN_AUDIO_DEFAULT_CHANNELS 2u
#define PARTYLAN_AUDIO_DEFAULT_FRAME_MS 20u
#define PARTYLAN_AUDIO_BITS_PER_SAMPLE 16u
#define PARTYLAN_AUDIO_MIN_PROCESS_LOOPBACK_BUILD 20438u
#define PARTYLAN_VIRTUAL_AUDIO_DEVICE_PROCESS_LOOPBACK L"VAD\\Process_Loopback"

typedef HRESULT (WINAPI* PartyLANActivateAudioInterfaceAsyncFn)(
    LPCWSTR path,
    REFIID riid,
    PROPVARIANT* params,
    IActivateAudioInterfaceCompletionHandler* done_handler,
    IActivateAudioInterfaceAsyncOperation** op);

typedef enum PartyLANProcessLoopbackMode {
    PARTYLAN_PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE = 0,
    PARTYLAN_PROCESS_LOOPBACK_MODE_EXCLUDE_TARGET_PROCESS_TREE = 1
} PartyLANProcessLoopbackMode;

typedef enum PartyLANAudioClientActivationType {
    PARTYLAN_AUDIOCLIENT_ACTIVATION_TYPE_DEFAULT = 0,
    PARTYLAN_AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK = 1
} PartyLANAudioClientActivationType;

typedef struct PartyLANAudioClientActivationParams {
    PartyLANAudioClientActivationType ActivationType;
    union {
        struct {
            DWORD TargetProcessId;
            PartyLANProcessLoopbackMode ProcessLoopbackMode;
        } ProcessLoopbackParams;
    };
} PartyLANAudioClientActivationParams;

typedef struct PartyLANAudioSession {
    PartyLANAudioSessionConfig config;
    WAVEFORMATEX format;
    IAudioClient* audio_client;
    IAudioCaptureClient* capture_client;
    HANDLE sample_ready;
    UINT32 buffer_frames;
    int configured;
    int started;
    int com_initialized;
} PartyLANAudioSession;

typedef struct PartyLANActivationHandler {
    IActivateAudioInterfaceCompletionHandler iface;
    LONG ref_count;
    HANDLE completed;
    HRESULT activate_result;
    HRESULT callback_result;
    IAudioClient* audio_client;
} PartyLANActivationHandler;

static char PARTYLAN_AUDIO_REASON[256];

static void partylan_audio_set_reason(const char* reason) {
    if (reason == NULL) {
        PARTYLAN_AUDIO_REASON[0] = '\0';
        return;
    }
    strncpy(PARTYLAN_AUDIO_REASON, reason, sizeof(PARTYLAN_AUDIO_REASON) - 1);
    PARTYLAN_AUDIO_REASON[sizeof(PARTYLAN_AUDIO_REASON) - 1] = '\0';
}

static void partylan_audio_set_hresult_reason(const char* step, HRESULT hr) {
    char buf[256];
    if (step == NULL) {
        step = "windows audio";
    }
    snprintf(buf, sizeof(buf), "%s failed: 0x%08lx", step, (unsigned long)hr);
    partylan_audio_set_reason(buf);
}

static PartyLANAudioStatus partylan_audio_status_with_reason(PartyLANAudioStatus status, const char* reason) {
    partylan_audio_set_reason(reason);
    return status;
}

static PartyLANAudioStatus partylan_audio_status_from_hresult(const char* step, HRESULT hr) {
    partylan_audio_set_hresult_reason(step, hr);
    return PARTYLAN_AUDIO_STATUS_UNAVAILABLE;
}

static uint64_t partylan_audio_now_us(void) {
    FILETIME ft;
    ULARGE_INTEGER value;
    GetSystemTimeAsFileTime(&ft);
    value.LowPart = ft.dwLowDateTime;
    value.HighPart = ft.dwHighDateTime;
    return value.QuadPart / 10u;
}

static int partylan_audio_os_build(DWORD* build_out) {
    typedef LONG (WINAPI* RtlGetVersionFn)(PRTL_OSVERSIONINFOW);
    HMODULE ntdll = GetModuleHandleW(L"ntdll.dll");
    RtlGetVersionFn rtl_get_version;
    RTL_OSVERSIONINFOW version;

    if (build_out == NULL || ntdll == NULL) {
        return 0;
    }

    rtl_get_version = (RtlGetVersionFn)GetProcAddress(ntdll, "RtlGetVersion");
    if (rtl_get_version == NULL) {
        return 0;
    }

    ZeroMemory(&version, sizeof(version));
    version.dwOSVersionInfoSize = sizeof(version);
    if (rtl_get_version(&version) != 0) {
        return 0;
    }

    *build_out = version.dwBuildNumber;
    return version.dwMajorVersion >= 10;
}

static PartyLANActivateAudioInterfaceAsyncFn partylan_audio_activate_async_fn(void) {
    HMODULE mmdevapi = GetModuleHandleW(L"Mmdevapi.dll");
    if (mmdevapi == NULL) {
        mmdevapi = LoadLibraryW(L"Mmdevapi.dll");
    }
    if (mmdevapi == NULL) {
        return NULL;
    }
    return (PartyLANActivateAudioInterfaceAsyncFn)GetProcAddress(mmdevapi, "ActivateAudioInterfaceAsync");
}

static int partylan_audio_process_loopback_available(void) {
    DWORD build = 0;
    if (!partylan_audio_os_build(&build)) {
        partylan_audio_set_reason("unable to determine Windows build for process loopback audio");
        return 0;
    }
    if (build < PARTYLAN_AUDIO_MIN_PROCESS_LOOPBACK_BUILD) {
        char buf[256];
        snprintf(buf, sizeof(buf), "Windows build %lu does not support process loopback audio; build %u or newer is required",
            (unsigned long)build, PARTYLAN_AUDIO_MIN_PROCESS_LOOPBACK_BUILD);
        partylan_audio_set_reason(buf);
        return 0;
    }
    if (partylan_audio_activate_async_fn() == NULL) {
        partylan_audio_set_reason("ActivateAudioInterfaceAsync is unavailable from Mmdevapi.dll");
        return 0;
    }
    partylan_audio_set_reason("process loopback capture bridge ready");
    return 1;
}

static char* partylan_audio_strdup(const char* value) {
    size_t len;
    char* out;
    if (value == NULL) {
        return NULL;
    }
    len = strlen(value);
    out = (char*)malloc(len + 1);
    if (out == NULL) {
        return NULL;
    }
    memcpy(out, value, len + 1);
    return out;
}

static char* partylan_audio_wide_to_utf8(const wchar_t* value) {
    int len;
    char* out;
    if (value == NULL || value[0] == L'\0') {
        return NULL;
    }
    len = WideCharToMultiByte(CP_UTF8, 0, value, -1, NULL, 0, NULL, NULL);
    if (len <= 0) {
        return NULL;
    }
    out = (char*)malloc((size_t)len);
    if (out == NULL) {
        return NULL;
    }
    if (WideCharToMultiByte(CP_UTF8, 0, value, -1, out, len, NULL, NULL) <= 0) {
        free(out);
        return NULL;
    }
    return out;
}

static const wchar_t* partylan_audio_wbasename(const wchar_t* path) {
    const wchar_t* slash;
    const wchar_t* backslash;
    if (path == NULL || path[0] == L'\0') {
        return L"";
    }
    slash = wcsrchr(path, L'/');
    backslash = wcsrchr(path, L'\\');
    if (slash == NULL || (backslash != NULL && backslash > slash)) {
        slash = backslash;
    }
    return slash == NULL ? path : slash + 1;
}

static int partylan_audio_process_path(DWORD pid, wchar_t* path, DWORD path_len) {
    HANDLE process;
    DWORD size;
    if (path == NULL || path_len == 0) {
        return 0;
    }
    path[0] = L'\0';
    process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
    if (process == NULL) {
        return 0;
    }
    size = path_len;
    if (!QueryFullProcessImageNameW(process, 0, path, &size)) {
        CloseHandle(process);
        path[0] = L'\0';
        return 0;
    }
    path[path_len - 1] = L'\0';
    CloseHandle(process);
    return 1;
}

static char* partylan_audio_process_name_from_path(const wchar_t* path, DWORD pid) {
    const wchar_t* base = partylan_audio_wbasename(path);
    char* out;
    char fallback[64];
    if (base != NULL && base[0] != L'\0') {
        out = partylan_audio_wide_to_utf8(base);
        if (out != NULL) {
            return out;
        }
    }
    snprintf(fallback, sizeof(fallback), "Process %lu", (unsigned long)pid);
    return partylan_audio_strdup(fallback);
}

static char* partylan_audio_display_name_from_path(const wchar_t* path, const wchar_t* session_name, DWORD pid) {
    const wchar_t* base;
    wchar_t name[MAX_PATH];
    wchar_t* dot;
    char* out;
    char fallback[64];

    out = partylan_audio_wide_to_utf8(session_name);
    if (out != NULL) {
        return out;
    }

    base = partylan_audio_wbasename(path);
    if (base != NULL && base[0] != L'\0') {
        wcsncpy(name, base, MAX_PATH - 1);
        name[MAX_PATH - 1] = L'\0';
        dot = wcsrchr(name, L'.');
        if (dot != NULL && dot != name) {
            *dot = L'\0';
        }
        out = partylan_audio_wide_to_utf8(name);
        if (out != NULL) {
            return out;
        }
    }

    snprintf(fallback, sizeof(fallback), "Process %lu", (unsigned long)pid);
    return partylan_audio_strdup(fallback);
}

static void partylan_audio_put16(uint8_t* out, uint16_t value) {
    out[0] = (uint8_t)(value & 0xffu);
    out[1] = (uint8_t)((value >> 8) & 0xffu);
}

static void partylan_audio_put32(uint8_t* out, uint32_t value) {
    out[0] = (uint8_t)(value & 0xffu);
    out[1] = (uint8_t)((value >> 8) & 0xffu);
    out[2] = (uint8_t)((value >> 16) & 0xffu);
    out[3] = (uint8_t)((value >> 24) & 0xffu);
}

static char* partylan_audio_base64_encode(const uint8_t* data, size_t len) {
    static const char table[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    size_t out_len = ((len + 2u) / 3u) * 4u;
    char* out = (char*)malloc(out_len + 1u);
    size_t i;
    size_t j = 0;
    if (out == NULL) {
        return NULL;
    }
    for (i = 0; i < len; i += 3u) {
        uint32_t octet_a = data[i];
        uint32_t octet_b = (i + 1u < len) ? data[i + 1u] : 0u;
        uint32_t octet_c = (i + 2u < len) ? data[i + 2u] : 0u;
        uint32_t triple = (octet_a << 16u) | (octet_b << 8u) | octet_c;

        out[j++] = table[(triple >> 18u) & 0x3fu];
        out[j++] = table[(triple >> 12u) & 0x3fu];
        out[j++] = (i + 1u < len) ? table[(triple >> 6u) & 0x3fu] : '=';
        out[j++] = (i + 2u < len) ? table[triple & 0x3fu] : '=';
    }
    out[j] = '\0';
    return out;
}

static char* partylan_audio_icon_data_url_from_path(const wchar_t* path) {
    const int icon_size = 32;
    const uint32_t header_size = 14u + 40u;
    const uint32_t image_size = (uint32_t)(icon_size * icon_size * 4);
    const uint32_t file_size = header_size + image_size;
    SHFILEINFOW file_info;
    HDC dc = NULL;
    HBITMAP bitmap = NULL;
    HGDIOBJ old_bitmap = NULL;
    BITMAPINFO bmi;
    void* pixels = NULL;
    uint8_t* bmp = NULL;
    char* b64 = NULL;
    char* out = NULL;
    const char* prefix = "data:image/bmp;base64,";
    size_t prefix_len;
    size_t b64_len;

    if (path == NULL || path[0] == L'\0') {
        return NULL;
    }

    ZeroMemory(&file_info, sizeof(file_info));
    if (SHGetFileInfoW(path, 0, &file_info, sizeof(file_info), SHGFI_ICON | SHGFI_LARGEICON) == 0 || file_info.hIcon == NULL) {
        return NULL;
    }

    dc = CreateCompatibleDC(NULL);
    if (dc == NULL) {
        DestroyIcon(file_info.hIcon);
        return NULL;
    }

    ZeroMemory(&bmi, sizeof(bmi));
    bmi.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
    bmi.bmiHeader.biWidth = icon_size;
    bmi.bmiHeader.biHeight = -icon_size;
    bmi.bmiHeader.biPlanes = 1;
    bmi.bmiHeader.biBitCount = 32;
    bmi.bmiHeader.biCompression = BI_RGB;

    bitmap = CreateDIBSection(dc, &bmi, DIB_RGB_COLORS, &pixels, NULL, 0);
    if (bitmap == NULL || pixels == NULL) {
        DeleteDC(dc);
        DestroyIcon(file_info.hIcon);
        return NULL;
    }

    old_bitmap = SelectObject(dc, bitmap);
    ZeroMemory(pixels, image_size);
    DrawIconEx(dc, 0, 0, file_info.hIcon, icon_size, icon_size, 0, NULL, DI_NORMAL);

    bmp = (uint8_t*)malloc(file_size);
    if (bmp != NULL) {
        ZeroMemory(bmp, file_size);
        bmp[0] = 'B';
        bmp[1] = 'M';
        partylan_audio_put32(bmp + 2, file_size);
        partylan_audio_put32(bmp + 10, header_size);
        partylan_audio_put32(bmp + 14, 40u);
        partylan_audio_put32(bmp + 18, (uint32_t)icon_size);
        partylan_audio_put32(bmp + 22, (uint32_t)(int32_t)(-icon_size));
        partylan_audio_put16(bmp + 26, 1u);
        partylan_audio_put16(bmp + 28, 32u);
        partylan_audio_put32(bmp + 34, image_size);
        memcpy(bmp + header_size, pixels, image_size);
        b64 = partylan_audio_base64_encode(bmp, file_size);
    }

    if (b64 != NULL) {
        prefix_len = strlen(prefix);
        b64_len = strlen(b64);
        out = (char*)malloc(prefix_len + b64_len + 1u);
        if (out != NULL) {
            memcpy(out, prefix, prefix_len);
            memcpy(out + prefix_len, b64, b64_len + 1u);
        }
    }

    if (old_bitmap != NULL) {
        SelectObject(dc, old_bitmap);
    }
    if (bitmap != NULL) {
        DeleteObject(bitmap);
    }
    if (dc != NULL) {
        DeleteDC(dc);
    }
    DestroyIcon(file_info.hIcon);
    free(bmp);
    free(b64);
    return out;
}

static void partylan_audio_free_source(PartyLANAudioSource* source) {
    if (source == NULL) {
        return;
    }
    free(source->name);
    free(source->display_name);
    free(source->icon_data_url);
    ZeroMemory(source, sizeof(*source));
}

static int partylan_audio_append_source(PartyLANAudioSourceList* list, PartyLANAudioSource source) {
    PartyLANAudioSource* next;
    uint32_t i;
    if (list == NULL || source.process_id == 0 || source.name == NULL) {
        partylan_audio_free_source(&source);
        return 0;
    }
    for (i = 0; i < list->count; i++) {
        if (list->items[i].process_id == source.process_id) {
            partylan_audio_free_source(&source);
            return 1;
        }
    }
    next = (PartyLANAudioSource*)realloc(list->items, sizeof(PartyLANAudioSource) * (list->count + 1u));
    if (next == NULL) {
        partylan_audio_free_source(&source);
        return 0;
    }
    list->items = next;
    list->items[list->count] = source;
    list->count++;
    return 1;
}

static HRESULT STDMETHODCALLTYPE partylan_activation_query_interface(
    IActivateAudioInterfaceCompletionHandler* This,
    REFIID riid,
    void** ppvObject) {
    if (ppvObject == NULL) {
        return E_POINTER;
    }
    *ppvObject = NULL;

    if (IsEqualGUID(riid, &IID_IUnknown) ||
        IsEqualGUID(riid, &IID_IActivateAudioInterfaceCompletionHandler) ||
        IsEqualGUID(riid, &IID_IAgileObject)) {
        *ppvObject = This;
        IActivateAudioInterfaceCompletionHandler_AddRef(This);
        return S_OK;
    }
    return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE partylan_activation_add_ref(IActivateAudioInterfaceCompletionHandler* This) {
    PartyLANActivationHandler* handler = (PartyLANActivationHandler*)This;
    return (ULONG)InterlockedIncrement(&handler->ref_count);
}

static ULONG STDMETHODCALLTYPE partylan_activation_release(IActivateAudioInterfaceCompletionHandler* This) {
    PartyLANActivationHandler* handler = (PartyLANActivationHandler*)This;
    LONG refs = InterlockedDecrement(&handler->ref_count);
    if (refs == 0) {
        if (handler->audio_client != NULL) {
            IAudioClient_Release(handler->audio_client);
            handler->audio_client = NULL;
        }
        if (handler->completed != NULL) {
            CloseHandle(handler->completed);
            handler->completed = NULL;
        }
        free(handler);
    }
    return (ULONG)refs;
}

static HRESULT STDMETHODCALLTYPE partylan_activation_completed(
    IActivateAudioInterfaceCompletionHandler* This,
    IActivateAudioInterfaceAsyncOperation* operation) {
    PartyLANActivationHandler* handler = (PartyLANActivationHandler*)This;
    IUnknown* audio_unknown = NULL;
    HRESULT operation_result = E_UNEXPECTED;
    HRESULT hr;

    handler->callback_result = E_UNEXPECTED;
    handler->activate_result = E_UNEXPECTED;

    if (operation == NULL) {
        SetEvent(handler->completed);
        return S_OK;
    }

    hr = IActivateAudioInterfaceAsyncOperation_GetActivateResult(operation, &operation_result, &audio_unknown);
    handler->callback_result = hr;
    handler->activate_result = operation_result;
    if (SUCCEEDED(hr) && SUCCEEDED(operation_result) && audio_unknown != NULL) {
        hr = IUnknown_QueryInterface(audio_unknown, &IID_IAudioClient, (void**)&handler->audio_client);
        handler->callback_result = hr;
    }
    if (audio_unknown != NULL) {
        IUnknown_Release(audio_unknown);
    }

    SetEvent(handler->completed);
    return S_OK;
}

static IActivateAudioInterfaceCompletionHandlerVtbl PARTYLAN_ACTIVATION_HANDLER_VTBL = {
    partylan_activation_query_interface,
    partylan_activation_add_ref,
    partylan_activation_release,
    partylan_activation_completed,
};

static PartyLANActivationHandler* partylan_activation_handler_create(void) {
    PartyLANActivationHandler* handler = (PartyLANActivationHandler*)calloc(1, sizeof(PartyLANActivationHandler));
    if (handler == NULL) {
        return NULL;
    }

    handler->iface.lpVtbl = &PARTYLAN_ACTIVATION_HANDLER_VTBL;
    handler->ref_count = 1;
    handler->completed = CreateEventW(NULL, FALSE, FALSE, NULL);
    handler->activate_result = E_UNEXPECTED;
    handler->callback_result = E_UNEXPECTED;
    if (handler->completed == NULL) {
        free(handler);
        return NULL;
    }
    return handler;
}

static void partylan_audio_reset_runtime(PartyLANAudioSession* session) {
    if (session == NULL) {
        return;
    }

    if (session->started && session->audio_client != NULL) {
        IAudioClient_Stop(session->audio_client);
    }
    session->started = 0;

    if (session->capture_client != NULL) {
        IAudioCaptureClient_Release(session->capture_client);
        session->capture_client = NULL;
    }
    if (session->audio_client != NULL) {
        IAudioClient_Release(session->audio_client);
        session->audio_client = NULL;
    }
    if (session->sample_ready != NULL) {
        CloseHandle(session->sample_ready);
        session->sample_ready = NULL;
    }
    if (session->com_initialized) {
        CoUninitialize();
        session->com_initialized = 0;
    }
    session->buffer_frames = 0;
}

static void partylan_audio_apply_format(PartyLANAudioSession* session) {
    WAVEFORMATEX* format = &session->format;
    ZeroMemory(format, sizeof(*format));
    format->wFormatTag = WAVE_FORMAT_PCM;
    format->nChannels = (WORD)session->config.channels;
    format->nSamplesPerSec = session->config.sample_rate;
    format->wBitsPerSample = PARTYLAN_AUDIO_BITS_PER_SAMPLE;
    format->nBlockAlign = (WORD)((format->nChannels * format->wBitsPerSample) / 8u);
    format->nAvgBytesPerSec = format->nSamplesPerSec * format->nBlockAlign;
    format->cbSize = 0;
}

static PartyLANAudioStatus partylan_audio_activate_session(PartyLANAudioSession* session) {
    PartyLANActivateAudioInterfaceAsyncFn activate_fn = partylan_audio_activate_async_fn();
    PartyLANActivationHandler* handler = NULL;
    IActivateAudioInterfaceAsyncOperation* async_op = NULL;
    PartyLANAudioClientActivationParams activation_params;
    PROPVARIANT prop;
    DWORD wait_result;
    HRESULT hr;

    if (activate_fn == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "ActivateAudioInterfaceAsync is unavailable from Mmdevapi.dll");
    }

    handler = partylan_activation_handler_create();
    if (handler == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "failed to create audio activation completion handler");
    }

    ZeroMemory(&activation_params, sizeof(activation_params));
    activation_params.ActivationType = PARTYLAN_AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK;
    activation_params.ProcessLoopbackParams.TargetProcessId = session->config.target_process_id;
    activation_params.ProcessLoopbackParams.ProcessLoopbackMode =
        session->config.capture_mode == PARTYLAN_AUDIO_CAPTURE_EXCLUDE_PROCESS_TREE
            ? PARTYLAN_PROCESS_LOOPBACK_MODE_EXCLUDE_TARGET_PROCESS_TREE
            : PARTYLAN_PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE;

    ZeroMemory(&prop, sizeof(prop));
    prop.vt = VT_BLOB;
    prop.blob.cbSize = sizeof(activation_params);
    prop.blob.pBlobData = (BYTE*)&activation_params;

    hr = activate_fn(PARTYLAN_VIRTUAL_AUDIO_DEVICE_PROCESS_LOOPBACK, &IID_IAudioClient, &prop, &handler->iface, &async_op);
    if (FAILED(hr)) {
        IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
        return partylan_audio_status_from_hresult("ActivateAudioInterfaceAsync", hr);
    }

    wait_result = WaitForSingleObject(handler->completed, 10000);
    if (async_op != NULL) {
        IActivateAudioInterfaceAsyncOperation_Release(async_op);
    }
    if (wait_result != WAIT_OBJECT_0) {
        IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "timed out waiting for process loopback activation");
    }
    if (FAILED(handler->callback_result)) {
        hr = handler->callback_result;
        IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
        return partylan_audio_status_from_hresult("process loopback activation callback", hr);
    }
    if (FAILED(handler->activate_result)) {
        hr = handler->activate_result;
        IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
        return partylan_audio_status_from_hresult("process loopback activation result", hr);
    }
    if (handler->audio_client == NULL) {
        IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "process loopback activation returned no IAudioClient");
    }

    session->audio_client = handler->audio_client;
    handler->audio_client = NULL;
    IActivateAudioInterfaceCompletionHandler_Release(&handler->iface);
    return PARTYLAN_AUDIO_STATUS_OK;
}

static PartyLANAudioStatus partylan_audio_activate_default_render_session(PartyLANAudioSession* session) {
    IMMDeviceEnumerator* device_enumerator = NULL;
    IMMDevice* device = NULL;
    HRESULT hr;

    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL, &IID_IMMDeviceEnumerator, (void**)&device_enumerator);
    if (FAILED(hr) || device_enumerator == NULL) {
        return partylan_audio_status_from_hresult("create audio device enumerator", hr);
    }

    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(device_enumerator, eRender, eConsole, &device);
    if (FAILED(hr) || device == NULL) {
        IMMDeviceEnumerator_Release(device_enumerator);
        return partylan_audio_status_from_hresult("get default render audio endpoint", hr);
    }

    hr = IMMDevice_Activate(device, &IID_IAudioClient, CLSCTX_ALL, NULL, (void**)&session->audio_client);
    IMMDevice_Release(device);
    IMMDeviceEnumerator_Release(device_enumerator);
    if (FAILED(hr) || session->audio_client == NULL) {
        return partylan_audio_status_from_hresult("activate default render audio client", hr);
    }
    return PARTYLAN_AUDIO_STATUS_OK;
}

PartyLANAudioStatus PartyLAN_QueryAudioCapability(PartyLANAudioCapability* capability) {
    if (capability == NULL) {
        return PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR;
    }

    capability->supports_process_capture = partylan_audio_process_loopback_available();
    capability->reason = PARTYLAN_AUDIO_REASON;
    return capability->supports_process_capture ? PARTYLAN_AUDIO_STATUS_OK : PARTYLAN_AUDIO_STATUS_UNAVAILABLE;
}

const char* PartyLAN_GetLastAudioReason(void) {
    return PARTYLAN_AUDIO_REASON;
}

PartyLANAudioStatus PartyLAN_ListAudioSources(PartyLANAudioSourceList* sources) {
    IMMDeviceEnumerator* device_enumerator = NULL;
    IMMDevice* device = NULL;
    IAudioSessionManager2* session_manager = NULL;
    IAudioSessionEnumerator* session_enumerator = NULL;
    HRESULT hr;
    int session_count = 0;
    int i;
    int com_initialized = 0;

    if (sources == NULL) {
        return PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR;
    }
    ZeroMemory(sources, sizeof(*sources));

    hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (SUCCEEDED(hr)) {
        com_initialized = 1;
    } else if (hr != RPC_E_CHANGED_MODE) {
        return partylan_audio_status_from_hresult("CoInitializeEx for audio session enumeration", hr);
    }

    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL, &IID_IMMDeviceEnumerator, (void**)&device_enumerator);
    if (FAILED(hr) || device_enumerator == NULL) {
        partylan_audio_status_from_hresult("create audio device enumerator", hr);
        goto cleanup;
    }

    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(device_enumerator, eRender, eConsole, &device);
    if (FAILED(hr) || device == NULL) {
        partylan_audio_status_from_hresult("get default render audio endpoint", hr);
        goto cleanup;
    }

    hr = IMMDevice_Activate(device, &IID_IAudioSessionManager2, CLSCTX_ALL, NULL, (void**)&session_manager);
    if (FAILED(hr) || session_manager == NULL) {
        partylan_audio_status_from_hresult("activate audio session manager", hr);
        goto cleanup;
    }

    hr = IAudioSessionManager2_GetSessionEnumerator(session_manager, &session_enumerator);
    if (FAILED(hr) || session_enumerator == NULL) {
        partylan_audio_status_from_hresult("get audio session enumerator", hr);
        goto cleanup;
    }

    hr = IAudioSessionEnumerator_GetCount(session_enumerator, &session_count);
    if (FAILED(hr)) {
        partylan_audio_status_from_hresult("get audio session count", hr);
        goto cleanup;
    }

    for (i = 0; i < session_count; i++) {
        IAudioSessionControl* session = NULL;
        IAudioSessionControl2* session2 = NULL;
        AudioSessionState state = AudioSessionStateExpired;
        DWORD pid = 0;
        wchar_t process_path[32768];
        LPWSTR session_display_name = NULL;
        PartyLANAudioSource source;

        ZeroMemory(&source, sizeof(source));
        process_path[0] = L'\0';

        if (FAILED(IAudioSessionEnumerator_GetSession(session_enumerator, i, &session)) || session == NULL) {
            continue;
        }
        if (FAILED(IAudioSessionControl_GetState(session, &state)) || state != AudioSessionStateActive) {
            IAudioSessionControl_Release(session);
            continue;
        }
        if (FAILED(IAudioSessionControl_QueryInterface(session, &IID_IAudioSessionControl2, (void**)&session2)) || session2 == NULL) {
            IAudioSessionControl_Release(session);
            continue;
        }
        if (IAudioSessionControl2_IsSystemSoundsSession(session2) == S_OK) {
            IAudioSessionControl2_Release(session2);
            IAudioSessionControl_Release(session);
            continue;
        }
        if (FAILED(IAudioSessionControl2_GetProcessId(session2, &pid)) || pid == 0) {
            IAudioSessionControl2_Release(session2);
            IAudioSessionControl_Release(session);
            continue;
        }

        (void)partylan_audio_process_path(pid, process_path, (DWORD)(sizeof(process_path) / sizeof(process_path[0])));
        (void)IAudioSessionControl2_GetDisplayName(session2, &session_display_name);

        source.process_id = (uint32_t)pid;
        source.active = 1;
        source.name = partylan_audio_process_name_from_path(process_path, pid);
        source.display_name = partylan_audio_display_name_from_path(process_path, session_display_name, pid);
        source.icon_data_url = partylan_audio_icon_data_url_from_path(process_path);

        if (!partylan_audio_append_source(sources, source)) {
            if (session_display_name != NULL) {
                CoTaskMemFree(session_display_name);
            }
            IAudioSessionControl2_Release(session2);
            IAudioSessionControl_Release(session);
            partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "failed to append audio session source");
            goto cleanup;
        }

        if (session_display_name != NULL) {
            CoTaskMemFree(session_display_name);
        }
        IAudioSessionControl2_Release(session2);
        IAudioSessionControl_Release(session);
    }

    partylan_audio_set_reason("active audio sessions enumerated");

cleanup:
    if (session_enumerator != NULL) {
        IAudioSessionEnumerator_Release(session_enumerator);
    }
    if (session_manager != NULL) {
        IAudioSessionManager2_Release(session_manager);
    }
    if (device != NULL) {
        IMMDevice_Release(device);
    }
    if (device_enumerator != NULL) {
        IMMDeviceEnumerator_Release(device_enumerator);
    }
    if (com_initialized) {
        CoUninitialize();
    }
    sources->reason = PARTYLAN_AUDIO_REASON;
    return sources->reason != NULL && strstr(sources->reason, "failed") != NULL
        ? PARTYLAN_AUDIO_STATUS_UNAVAILABLE
        : PARTYLAN_AUDIO_STATUS_OK;
}

void PartyLAN_FreeAudioSourceList(PartyLANAudioSourceList* sources) {
    uint32_t i;
    if (sources == NULL) {
        return;
    }
    for (i = 0; i < sources->count; i++) {
        partylan_audio_free_source(&sources->items[i]);
    }
    free(sources->items);
    ZeroMemory(sources, sizeof(*sources));
}

void* PartyLAN_CreateAudioSession(void) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)calloc(1, sizeof(PartyLANAudioSession));
    if (session == NULL) {
        partylan_audio_set_reason("failed to allocate audio session");
        return NULL;
    }
    return session;
}

PartyLANAudioStatus PartyLAN_ConfigureAudioSession(void* handle, const PartyLANAudioSessionConfig* config) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)handle;
    PartyLANAudioSessionConfig next;

    if (session == NULL || config == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session/config is null");
    }
    if (session->started) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session must be stopped before reconfiguration");
    }
    if (config->target_process_id == 0 && config->capture_mode != PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "target process id is required for process loopback audio");
    }
    if (config->capture_mode != PARTYLAN_AUDIO_CAPTURE_INCLUDE_PROCESS_TREE &&
        config->capture_mode != PARTYLAN_AUDIO_CAPTURE_EXCLUDE_PROCESS_TREE &&
        config->capture_mode != PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "invalid process loopback capture mode");
    }

    next = *config;
    if (next.sample_rate == 0) {
        next.sample_rate = PARTYLAN_AUDIO_DEFAULT_SAMPLE_RATE;
    }
    if (next.channels == 0) {
        next.channels = PARTYLAN_AUDIO_DEFAULT_CHANNELS;
    }
    if (next.frame_ms == 0) {
        next.frame_ms = PARTYLAN_AUDIO_DEFAULT_FRAME_MS;
    }
    if (next.channels != 1 && next.channels != 2) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio capture currently supports mono or stereo PCM");
    }

    session->config = next;
    session->configured = 1;
    partylan_audio_apply_format(session);
    partylan_audio_set_reason("audio session configured");
    return PARTYLAN_AUDIO_STATUS_OK;
}

PartyLANAudioStatus PartyLAN_StartAudioSession(void* handle) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)handle;
    HRESULT hr;

    if (session == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session is null");
    }
    if (!session->configured) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session is not configured");
    }
    if (session->started) {
        return PARTYLAN_AUDIO_STATUS_OK;
    }
    if (session->config.capture_mode != PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX &&
        !partylan_audio_process_loopback_available()) {
        return PARTYLAN_AUDIO_STATUS_UNAVAILABLE;
    }

    hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) {
        return partylan_audio_status_from_hresult("CoInitializeEx", hr);
    }
    if (hr == RPC_E_CHANGED_MODE) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE,
            "COM is already initialized in an incompatible apartment; start audio capture from a dedicated MTA thread");
    }
    session->com_initialized = 1;

    if ((session->config.capture_mode == PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX
            ? partylan_audio_activate_default_render_session(session)
            : partylan_audio_activate_session(session)) != PARTYLAN_AUDIO_STATUS_OK) {
        partylan_audio_reset_runtime(session);
        return PARTYLAN_AUDIO_STATUS_UNAVAILABLE;
    }

    session->sample_ready = CreateEventW(NULL, FALSE, FALSE, NULL);
    if (session->sample_ready == NULL) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "failed to create audio sample event");
    }

    hr = IAudioClient_Initialize(
        session->audio_client,
        AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_EVENTCALLBACK | AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM,
        0,
        0,
        &session->format,
        NULL);
    if (FAILED(hr)) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_from_hresult("IAudioClient Initialize", hr);
    }

    hr = IAudioClient_GetBufferSize(session->audio_client, &session->buffer_frames);
    if (FAILED(hr)) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_from_hresult("IAudioClient GetBufferSize", hr);
    }

    hr = IAudioClient_GetService(session->audio_client, &IID_IAudioCaptureClient, (void**)&session->capture_client);
    if (FAILED(hr)) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_from_hresult("IAudioClient GetService(IAudioCaptureClient)", hr);
    }

    hr = IAudioClient_SetEventHandle(session->audio_client, session->sample_ready);
    if (FAILED(hr)) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_from_hresult("IAudioClient SetEventHandle", hr);
    }

    hr = IAudioClient_Start(session->audio_client);
    if (FAILED(hr)) {
        partylan_audio_reset_runtime(session);
        return partylan_audio_status_from_hresult("IAudioClient Start", hr);
    }

    session->started = 1;
    if (session->config.capture_mode == PARTYLAN_AUDIO_CAPTURE_SYSTEM_MIX) {
        partylan_audio_set_reason("system loopback capture started");
    } else {
        partylan_audio_set_reason("process loopback capture started");
    }
    return PARTYLAN_AUDIO_STATUS_OK;
}

PartyLANAudioStatus PartyLAN_ReadAudioFrame(void* handle, int timeout_ms, PartyLANAudioFrame* frame) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)handle;
    DWORD wait_ms;
    DWORD wait_result;
    UINT32 frames_available = 0;
    BYTE* data = NULL;
    DWORD flags = 0;
    UINT64 device_position = 0;
    UINT64 qpc_position = 0;
    uint32_t bytes_to_copy;
    HRESULT hr;

    if (frame != NULL) {
        ZeroMemory(frame, sizeof(*frame));
    }
    if (session == NULL || frame == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session/frame is null");
    }
    if (!session->started || session->capture_client == NULL) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "audio session is not started");
    }

    wait_ms = timeout_ms < 0 ? INFINITE : (DWORD)timeout_ms;
    wait_result = WaitForSingleObject(session->sample_ready, wait_ms);
    if (wait_result == WAIT_TIMEOUT) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "audio capture timeout");
    }
    if (wait_result != WAIT_OBJECT_0) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "audio sample wait failed");
    }

    hr = IAudioCaptureClient_GetNextPacketSize(session->capture_client, &frames_available);
    if (FAILED(hr)) {
        return partylan_audio_status_from_hresult("IAudioCaptureClient GetNextPacketSize", hr);
    }
    if (frames_available == 0) {
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_UNAVAILABLE, "audio capture buffer empty");
    }

    hr = IAudioCaptureClient_GetBuffer(session->capture_client, &data, &frames_available, &flags, &device_position, &qpc_position);
    if (FAILED(hr)) {
        return partylan_audio_status_from_hresult("IAudioCaptureClient GetBuffer", hr);
    }

    bytes_to_copy = frames_available * session->format.nBlockAlign;
    frame->data = (uint8_t*)malloc(bytes_to_copy);
    if (frame->data == NULL) {
        IAudioCaptureClient_ReleaseBuffer(session->capture_client, frames_available);
        return partylan_audio_status_with_reason(PARTYLAN_AUDIO_STATUS_INTERNAL_ERROR, "failed to allocate audio frame");
    }

    if ((flags & AUDCLNT_BUFFERFLAGS_SILENT) != 0 || data == NULL) {
        ZeroMemory(frame->data, bytes_to_copy);
    } else {
        memcpy(frame->data, data, bytes_to_copy);
    }
    frame->data_len = bytes_to_copy;
    frame->channels = session->format.nChannels;
    frame->sample_rate = session->format.nSamplesPerSec;
    frame->timestamp_us = qpc_position == 0 ? (int64_t)partylan_audio_now_us() : (int64_t)(qpc_position / 10u);

    IAudioCaptureClient_ReleaseBuffer(session->capture_client, frames_available);
    partylan_audio_set_reason("audio frame captured");
    return PARTYLAN_AUDIO_STATUS_OK;
}

void PartyLAN_ReleaseAudioFrame(void* handle, PartyLANAudioFrame* frame) {
    (void)handle;
    if (frame == NULL) {
        return;
    }
    if (frame->data != NULL) {
        free(frame->data);
    }
    ZeroMemory(frame, sizeof(*frame));
}

void PartyLAN_StopAudioSession(void* handle) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)handle;
    partylan_audio_reset_runtime(session);
    partylan_audio_set_reason("audio session stopped");
}

void PartyLAN_DestroyAudioSession(void* handle) {
    PartyLANAudioSession* session = (PartyLANAudioSession*)handle;
    if (session == NULL) {
        return;
    }
    partylan_audio_reset_runtime(session);
    free(session);
}
