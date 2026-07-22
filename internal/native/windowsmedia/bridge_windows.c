#include "bridge.h"

#include <d3d11.h>
#include <dxgi.h>
#include <dxgi1_2.h>
#include <mfapi.h>
#include <mferror.h>
#include <mfidl.h>
#include <mfobjects.h>
#include <mftransform.h>
#include <objbase.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>
#include <wmcodecdsp.h>
#include "../../../third_party/nvcodec/nvEncodeAPI.h"

typedef NVENCSTATUS (NVENCAPI* PartyLANNvEncodeApiCreateInstanceFn)(NV_ENCODE_API_FUNCTION_LIST* functionList);
typedef NVENCSTATUS (NVENCAPI* PartyLANNvEncodeApiGetMaxSupportedVersionFn)(uint32_t* version);

static char PARTYLAN_REASON[256];
static char PARTYLAN_ADAPTER[256];
static char PARTYLAN_DRIVER[128];

typedef struct PartyLANCapturer {
    ID3D11Device* device;
    ID3D11DeviceContext* context;
    IDXGIOutputDuplication* duplication;
    ID3D11Texture2D* staging;
    UINT width;
    UINT height;
    UINT stride;
    int started;
} PartyLANCapturer;

typedef struct PartyLANEncoder {
    PartyLANMediaEncoderConfig config;
    int configured;
    HMODULE nvenc_lib;
    NV_ENCODE_API_FUNCTION_LIST api;
    uint32_t api_version;
    int api_ready;
    ID3D11Device* d3d_device;
    ID3D11DeviceContext* d3d_context;
    void* session;
    NV_ENC_INPUT_PTR input_buffer;
    NV_ENC_OUTPUT_PTR bitstream_buffer;
    ID3D11Texture2D* input_texture;
    NV_ENC_REGISTERED_PTR registered_input;
    NV_ENC_BUFFER_FORMAT input_format;
    uint32_t frame_index;
    int force_idr;
} PartyLANEncoder;

typedef struct PartyLANDecoder {
    IMFTransform* transform;
    IMFMediaType* input_type;
    IMFMediaType* output_type;
    UINT32 width;
    UINT32 height;
    int configured;
    int mf_started;
    int com_initialized;
} PartyLANDecoder;

static void partylan_reset_strings(void) {
    PARTYLAN_REASON[0] = '\0';
    PARTYLAN_ADAPTER[0] = '\0';
    PARTYLAN_DRIVER[0] = '\0';
}

static void partylan_set_reason(const char* reason) {
    if (reason == NULL) {
        PARTYLAN_REASON[0] = '\0';
        return;
    }
    strncpy(PARTYLAN_REASON, reason, sizeof(PARTYLAN_REASON) - 1);
    PARTYLAN_REASON[sizeof(PARTYLAN_REASON) - 1] = '\0';
}

static int partylan_wide_to_utf8(const WCHAR* src, char* dst, int dst_len) {
    if (src == NULL || dst == NULL || dst_len <= 0) {
        return 0;
    }
    int written = WideCharToMultiByte(CP_UTF8, 0, src, -1, dst, dst_len, NULL, NULL);
    if (written <= 0) {
        dst[0] = '\0';
        return 0;
    }
    return 1;
}

static int partylan_detect_primary_adapter(void) {
    IDXGIFactory1* factory = NULL;
    IDXGIAdapter1* adapter = NULL;
    DXGI_ADAPTER_DESC1 desc;
    HRESULT hr;

    hr = CreateDXGIFactory1(&IID_IDXGIFactory1, (void**)&factory);
    if (FAILED(hr) || factory == NULL) {
        partylan_set_reason("CreateDXGIFactory1 failed");
        return 0;
    }

    hr = factory->lpVtbl->EnumAdapters1(factory, 0, &adapter);
    if (FAILED(hr) || adapter == NULL) {
        factory->lpVtbl->Release(factory);
        partylan_set_reason("no DXGI adapter available");
        return 0;
    }

    hr = adapter->lpVtbl->GetDesc1(adapter, &desc);
    if (FAILED(hr)) {
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("GetDesc1 failed");
        return 0;
    }

    if (!partylan_wide_to_utf8(desc.Description, PARTYLAN_ADAPTER, (int)sizeof(PARTYLAN_ADAPTER))) {
        strncpy(PARTYLAN_ADAPTER, "Unknown adapter", sizeof(PARTYLAN_ADAPTER) - 1);
        PARTYLAN_ADAPTER[sizeof(PARTYLAN_ADAPTER) - 1] = '\0';
    }

    snprintf(PARTYLAN_DRIVER, sizeof(PARTYLAN_DRIVER), "vendor=0x%04x device=0x%04x",
        (unsigned int)desc.VendorId, (unsigned int)desc.DeviceId);

    adapter->lpVtbl->Release(adapter);
    factory->lpVtbl->Release(factory);
    return 1;
}

static int partylan_detect_nvenc(void) {
    HMODULE lib = LoadLibraryA("nvEncodeAPI64.dll");
    if (lib == NULL) {
        lib = LoadLibraryA("nvEncodeAPI.dll");
    }
    if (lib == NULL) {
        return 0;
    }

    FARPROC symbol = GetProcAddress(lib, "NvEncodeAPICreateInstance");
    FreeLibrary(lib);
    return symbol != NULL;
}

static void partylan_set_nv_reason(const char* step, NVENCSTATUS status) {
    char reason_buf[256];
    if (step == NULL) {
        step = "nvenc";
    }
    snprintf(reason_buf, sizeof(reason_buf), "%s failed: %d", step, (int)status);
    partylan_set_reason(reason_buf);
}

static void partylan_set_hresult_reason(const char* step, HRESULT hr) {
    char reason_buf[256];
    if (step == NULL) {
        step = "media foundation";
    }
    snprintf(reason_buf, sizeof(reason_buf), "%s failed: 0x%08lx", step, (unsigned long)hr);
    partylan_set_reason(reason_buf);
}

static HRESULT partylan_mf_set_attribute_pair(IMFAttributes* attrs, REFGUID key, UINT32 high, UINT32 low) {
    UINT64 value;
    if (attrs == NULL || key == NULL) {
        return E_INVALIDARG;
    }
    value = ((UINT64)high << 32) | (UINT64)low;
    return attrs->lpVtbl->SetUINT64(attrs, key, value);
}

static uint8_t partylan_clamp_u8(int value) {
    if (value < 0) {
        return 0;
    }
    if (value > 255) {
        return 255;
    }
    return (uint8_t)value;
}

static void partylan_nv12_to_rgba(const uint8_t* src, LONG pitch, uint32_t width, uint32_t height, uint8_t* dst) {
    uint32_t x;
    uint32_t y;
    const uint8_t* y_plane;
    const uint8_t* uv_plane;

    if (src == NULL || dst == NULL || width == 0 || height == 0) {
        return;
    }

    if (pitch == 0) {
        pitch = (LONG)width;
    }
    if (pitch < 0) {
        pitch = -pitch;
    }

    y_plane = src;
    uv_plane = src + ((size_t)pitch * height);

    for (y = 0; y < height; ++y) {
        const uint8_t* y_row = y_plane + ((size_t)y * pitch);
        const uint8_t* uv_row = uv_plane + ((size_t)(y / 2) * pitch);
        uint8_t* dst_row = dst + ((size_t)y * width * 4);
        for (x = 0; x < width; ++x) {
            int yy = (int)y_row[x];
            int u = (int)uv_row[(x / 2) * 2] - 128;
            int v = (int)uv_row[(x / 2) * 2 + 1] - 128;
            int c = yy - 16;
            int r;
            int g;
            int b;
            if (c < 0) {
                c = 0;
            }
            r = (298 * c + 409 * v + 128) >> 8;
            g = (298 * c - 100 * u - 208 * v + 128) >> 8;
            b = (298 * c + 516 * u + 128) >> 8;
            dst_row[x * 4 + 0] = partylan_clamp_u8(r);
            dst_row[x * 4 + 1] = partylan_clamp_u8(g);
            dst_row[x * 4 + 2] = partylan_clamp_u8(b);
            dst_row[x * 4 + 3] = 255;
        }
    }
}

static uint32_t partylan_nvenc_struct_version(uint32_t api_version, uint32_t ver) {
    return api_version | (ver << 16) | (0x7u << 28);
}

static uint32_t partylan_nvenc_struct_version_ex(uint32_t api_version, uint32_t ver) {
    return partylan_nvenc_struct_version(api_version, ver) | (1u << 31);
}

static uint32_t partylan_nvenc_api_major(uint32_t api_version) {
    return api_version & 0x00ffffffu;
}

static uint32_t partylan_nvenc_api_minor(uint32_t api_version) {
    return api_version >> 24;
}

static uint32_t partylan_nvenc_max_supported_major(uint32_t max_supported_version) {
    return max_supported_version >> 4;
}

static uint32_t partylan_nvenc_max_supported_minor(uint32_t max_supported_version) {
    return max_supported_version & 0x0fu;
}

static int partylan_nvenc_api_version_less(uint32_t lhs, uint32_t rhs) {
    uint32_t lhs_major = partylan_nvenc_api_major(lhs);
    uint32_t rhs_major = partylan_nvenc_api_major(rhs);

    if (lhs_major != rhs_major) {
        return lhs_major < rhs_major;
    }
    return partylan_nvenc_api_minor(lhs) < partylan_nvenc_api_minor(rhs);
}

static void partylan_append_api_version_reason(char* reason_buf, size_t reason_buf_len, uint32_t api_version, uint32_t max_supported_version) {
    uint32_t api_major = partylan_nvenc_api_major(api_version);
    uint32_t api_minor = partylan_nvenc_api_minor(api_version);
    uint32_t max_major = partylan_nvenc_max_supported_major(max_supported_version);
    uint32_t max_minor = partylan_nvenc_max_supported_minor(max_supported_version);
    size_t used;

    if (reason_buf == NULL || reason_buf_len == 0) {
        return;
    }

    used = strlen(reason_buf);
    if (used >= reason_buf_len - 1) {
        return;
    }

    snprintf(
        reason_buf + used,
        reason_buf_len - used,
        " (api=%u.%u max_supported=%u.%u)",
        api_major,
        api_minor,
        max_major,
        max_minor);
}

static uint32_t partylan_pack_max_supported_api_version(uint32_t max_supported_version) {
    uint32_t major = partylan_nvenc_max_supported_major(max_supported_version);
    uint32_t minor = partylan_nvenc_max_supported_minor(max_supported_version);
    return major | (minor << 24);
}

static uint32_t partylan_encoder_keyframe_interval(const PartyLANEncoder* encoder) {
    if (encoder == NULL || encoder->config.fps == 0) {
        return 60;
    }
    return encoder->config.fps * 2U;
}

static int partylan_encoder_should_emit_idr(const PartyLANEncoder* encoder, uint32_t frame_idx) {
    uint32_t interval = partylan_encoder_keyframe_interval(encoder);
    if (encoder == NULL) {
        return 0;
    }
    if (encoder->force_idr) {
        return 1;
    }
    return interval > 0 && frame_idx > 0 && (frame_idx % interval) == 0;
}

static void partylan_encoder_cleanup_runtime(PartyLANEncoder* encoder) {
    if (encoder == NULL) {
        return;
    }

    if (encoder->api_ready && encoder->session != NULL) {
        if (encoder->registered_input != NULL && encoder->api.nvEncUnregisterResource != NULL) {
            encoder->api.nvEncUnregisterResource(encoder->session, encoder->registered_input);
        }
        if (encoder->bitstream_buffer != NULL && encoder->api.nvEncDestroyBitstreamBuffer != NULL) {
            encoder->api.nvEncDestroyBitstreamBuffer(encoder->session, encoder->bitstream_buffer);
        }
        if (encoder->input_buffer != NULL && encoder->api.nvEncDestroyInputBuffer != NULL) {
            encoder->api.nvEncDestroyInputBuffer(encoder->session, encoder->input_buffer);
        }
        if (encoder->api.nvEncDestroyEncoder != NULL) {
            encoder->api.nvEncDestroyEncoder(encoder->session);
        }
    }

    encoder->bitstream_buffer = NULL;
    encoder->input_buffer = NULL;
    encoder->registered_input = NULL;
    if (encoder->input_texture != NULL) {
        encoder->input_texture->lpVtbl->Release(encoder->input_texture);
        encoder->input_texture = NULL;
    }
    encoder->session = NULL;
    encoder->configured = 0;
    encoder->api_version = 0;
    encoder->frame_index = 0;
    encoder->force_idr = 0;
    encoder->input_format = NV_ENC_BUFFER_FORMAT_ABGR;

    if (encoder->d3d_context != NULL) {
        encoder->d3d_context->lpVtbl->Release(encoder->d3d_context);
        encoder->d3d_context = NULL;
    }
    if (encoder->d3d_device != NULL) {
        encoder->d3d_device->lpVtbl->Release(encoder->d3d_device);
        encoder->d3d_device = NULL;
    }
}

static PartyLANMediaStatus partylan_encoder_collect_bitstream(PartyLANEncoder* encoder, int64_t fallback_timestamp_us, PartyLANMediaPacket* packet) {
    NV_ENC_LOCK_BITSTREAM lock_bitstream;
    NVENCSTATUS nv_status;
    uint8_t* out;

    if (encoder == NULL || packet == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    memset(&lock_bitstream, 0, sizeof(lock_bitstream));
    lock_bitstream.version = partylan_nvenc_struct_version_ex(encoder->api_version, 2);
    lock_bitstream.outputBitstream = encoder->bitstream_buffer;
    lock_bitstream.doNotWait = 0;

    nv_status = encoder->api.nvEncLockBitstream(encoder->session, &lock_bitstream);
    if (nv_status != NV_ENC_SUCCESS || lock_bitstream.bitstreamBufferPtr == NULL) {
        if (nv_status == NV_ENC_ERR_ENCODER_BUSY || nv_status == NV_ENC_ERR_LOCK_BUSY) {
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        partylan_set_nv_reason("nvEncLockBitstream", nv_status);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (lock_bitstream.bitstreamSizeInBytes == 0) {
        encoder->api.nvEncUnlockBitstream(encoder->session, encoder->bitstream_buffer);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    out = (uint8_t*)malloc(lock_bitstream.bitstreamSizeInBytes);
    if (out == NULL) {
        encoder->api.nvEncUnlockBitstream(encoder->session, encoder->bitstream_buffer);
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    memcpy(out, lock_bitstream.bitstreamBufferPtr, lock_bitstream.bitstreamSizeInBytes);

    nv_status = encoder->api.nvEncUnlockBitstream(encoder->session, encoder->bitstream_buffer);
    if (nv_status != NV_ENC_SUCCESS) {
        free(out);
        partylan_set_nv_reason("nvEncUnlockBitstream", nv_status);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    packet->data = out;
    packet->data_len = lock_bitstream.bitstreamSizeInBytes;
    packet->timestamp_us = lock_bitstream.outputTimeStamp != 0 ? (int64_t)lock_bitstream.outputTimeStamp : fallback_timestamp_us;
    return PARTYLAN_MEDIA_STATUS_OK;
}

static PartyLANMediaStatus partylan_encoder_prepare_direct_input(PartyLANEncoder* encoder, uint32_t width, uint32_t height) {
    D3D11_TEXTURE2D_DESC tex_desc;
    NV_ENC_REGISTER_RESOURCE reg;
    NVENCSTATUS nv_status;
    HRESULT hr;

    if (encoder == NULL || encoder->d3d_device == NULL || encoder->session == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    if (encoder->input_texture != NULL) {
        D3D11_TEXTURE2D_DESC existing;
        encoder->input_texture->lpVtbl->GetDesc(encoder->input_texture, &existing);
        if (existing.Width == width && existing.Height == height) {
            if (encoder->registered_input != NULL) {
                return PARTYLAN_MEDIA_STATUS_OK;
            }
        } else {
            if (encoder->registered_input != NULL && encoder->api.nvEncUnregisterResource != NULL) {
                encoder->api.nvEncUnregisterResource(encoder->session, encoder->registered_input);
                encoder->registered_input = NULL;
            }
            encoder->input_texture->lpVtbl->Release(encoder->input_texture);
            encoder->input_texture = NULL;
        }
    }

    if (encoder->input_texture == NULL) {
        ZeroMemory(&tex_desc, sizeof(tex_desc));
        tex_desc.Width = width;
        tex_desc.Height = height;
        tex_desc.MipLevels = 1;
        tex_desc.ArraySize = 1;
        tex_desc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        tex_desc.SampleDesc.Count = 1;
        tex_desc.Usage = D3D11_USAGE_DEFAULT;
        tex_desc.BindFlags = 0;
        tex_desc.CPUAccessFlags = 0;
        tex_desc.MiscFlags = 0;
        hr = encoder->d3d_device->lpVtbl->CreateTexture2D(encoder->d3d_device, &tex_desc, NULL, &encoder->input_texture);
        if (FAILED(hr) || encoder->input_texture == NULL) {
            partylan_set_reason("direct encode: CreateTexture2D failed");
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
    }

    if (encoder->registered_input == NULL) {
        memset(&reg, 0, sizeof(reg));
        reg.version = partylan_nvenc_struct_version(encoder->api_version, 5);
        reg.resourceType = NV_ENC_INPUT_RESOURCE_TYPE_DIRECTX;
        reg.width = width;
        reg.height = height;
        reg.pitch = 0;
        reg.subResourceIndex = 0;
        reg.resourceToRegister = encoder->input_texture;
        reg.bufferFormat = NV_ENC_BUFFER_FORMAT_ARGB;
        reg.bufferUsage = NV_ENC_INPUT_IMAGE;
        nv_status = encoder->api.nvEncRegisterResource(encoder->session, &reg);
        if (nv_status != NV_ENC_SUCCESS || reg.registeredResource == NULL) {
            partylan_set_nv_reason("nvEncRegisterResource", nv_status);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        encoder->registered_input = reg.registeredResource;
    }

    return PARTYLAN_MEDIA_STATUS_OK;
}

static void partylan_decoder_cleanup(PartyLANDecoder* decoder) {
    if (decoder == NULL) {
        return;
    }
    if (decoder->transform != NULL) {
        decoder->transform->lpVtbl->ProcessMessage(decoder->transform, MFT_MESSAGE_COMMAND_FLUSH, 0);
    }
    if (decoder->output_type != NULL) {
        decoder->output_type->lpVtbl->Release(decoder->output_type);
        decoder->output_type = NULL;
    }
    if (decoder->input_type != NULL) {
        decoder->input_type->lpVtbl->Release(decoder->input_type);
        decoder->input_type = NULL;
    }
    if (decoder->transform != NULL) {
        decoder->transform->lpVtbl->Release(decoder->transform);
        decoder->transform = NULL;
    }
    decoder->configured = 0;
    decoder->width = 0;
    decoder->height = 0;
}

static void partylan_decoder_shutdown_platform(PartyLANDecoder* decoder) {
    if (decoder == NULL) {
        return;
    }
    if (decoder->mf_started) {
        MFShutdown();
        decoder->mf_started = 0;
    }
    if (decoder->com_initialized) {
        CoUninitialize();
        decoder->com_initialized = 0;
    }
}

static PartyLANMediaStatus partylan_decoder_ensure_platform(PartyLANDecoder* decoder) {
    HRESULT hr;

    if (decoder == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    if (!decoder->com_initialized) {
        hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
        if (SUCCEEDED(hr)) {
            decoder->com_initialized = 1;
        } else if (hr != RPC_E_CHANGED_MODE) {
            partylan_set_hresult_reason("CoInitializeEx", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
    }

    if (!decoder->mf_started) {
        hr = MFStartup(MF_VERSION, MFSTARTUP_LITE);
        if (FAILED(hr)) {
            partylan_set_hresult_reason("MFStartup", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        decoder->mf_started = 1;
    }

    return PARTYLAN_MEDIA_STATUS_OK;
}

static int partylan_detect_h264_decoder(void) {
    PartyLANDecoder decoder;
    HRESULT hr;
    int available = 0;

    memset(&decoder, 0, sizeof(decoder));
    if (partylan_decoder_ensure_platform(&decoder) != PARTYLAN_MEDIA_STATUS_OK) {
        partylan_decoder_shutdown_platform(&decoder);
        return 0;
    }

    hr = CoCreateInstance(
        &CLSID_CMSH264DecoderMFT,
        NULL,
        CLSCTX_INPROC_SERVER,
        &IID_IMFTransform,
        (void**)&decoder.transform);
    if (SUCCEEDED(hr) && decoder.transform != NULL) {
        available = 1;
    }

    partylan_decoder_cleanup(&decoder);
    partylan_decoder_shutdown_platform(&decoder);
    return available;
}

static PartyLANMediaStatus partylan_decoder_set_output_type(PartyLANDecoder* decoder) {
    IMFMediaType* output_type = NULL;
    HRESULT hr;

    if (decoder == NULL || decoder->transform == NULL || decoder->width == 0 || decoder->height == 0) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    hr = MFCreateMediaType(&output_type);
    if (FAILED(hr) || output_type == NULL) {
        partylan_set_hresult_reason("MFCreateMediaType output", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = output_type->lpVtbl->SetGUID(output_type, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
    if (SUCCEEDED(hr)) {
        hr = output_type->lpVtbl->SetGUID(output_type, &MF_MT_SUBTYPE, &MFVideoFormat_NV12);
    }
    if (SUCCEEDED(hr)) {
        hr = partylan_mf_set_attribute_pair((IMFAttributes*)output_type, &MF_MT_FRAME_SIZE, decoder->width, decoder->height);
    }
    if (SUCCEEDED(hr)) {
        hr = partylan_mf_set_attribute_pair((IMFAttributes*)output_type, &MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
    }
    if (SUCCEEDED(hr)) {
        hr = decoder->transform->lpVtbl->SetOutputType(decoder->transform, 0, output_type, 0);
    }
    if (FAILED(hr)) {
        output_type->lpVtbl->Release(output_type);
        partylan_set_hresult_reason("decoder SetOutputType NV12", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    if (decoder->output_type != NULL) {
        decoder->output_type->lpVtbl->Release(decoder->output_type);
    }
    decoder->output_type = output_type;
    return PARTYLAN_MEDIA_STATUS_OK;
}

static PartyLANMediaStatus partylan_decoder_configure(PartyLANDecoder* decoder, const PartyLANMediaDecoderConfig* config) {
    IMFMediaType* input_type = NULL;
    HRESULT hr;
    PartyLANMediaStatus status;

    if (decoder == NULL || config == NULL || config->width == 0 || config->height == 0) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    partylan_decoder_cleanup(decoder);
    status = partylan_decoder_ensure_platform(decoder);
    if (status != PARTYLAN_MEDIA_STATUS_OK) {
        return status;
    }

    hr = CoCreateInstance(
        &CLSID_CMSH264DecoderMFT,
        NULL,
        CLSCTX_INPROC_SERVER,
        &IID_IMFTransform,
        (void**)&decoder->transform);
    if (FAILED(hr) || decoder->transform == NULL) {
        partylan_set_hresult_reason("CoCreateInstance H264 decoder", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = MFCreateMediaType(&input_type);
    if (FAILED(hr) || input_type == NULL) {
        partylan_set_hresult_reason("MFCreateMediaType input", hr);
        partylan_decoder_cleanup(decoder);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = input_type->lpVtbl->SetGUID(input_type, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
    if (SUCCEEDED(hr)) {
        hr = input_type->lpVtbl->SetGUID(input_type, &MF_MT_SUBTYPE, &MFVideoFormat_H264);
    }
    if (SUCCEEDED(hr)) {
        hr = partylan_mf_set_attribute_pair((IMFAttributes*)input_type, &MF_MT_FRAME_SIZE, config->width, config->height);
    }
    if (SUCCEEDED(hr)) {
        hr = partylan_mf_set_attribute_pair((IMFAttributes*)input_type, &MF_MT_FRAME_RATE, 30, 1);
    }
    if (SUCCEEDED(hr)) {
        hr = partylan_mf_set_attribute_pair((IMFAttributes*)input_type, &MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
    }
    if (SUCCEEDED(hr)) {
        hr = decoder->transform->lpVtbl->SetInputType(decoder->transform, 0, input_type, 0);
    }
    if (FAILED(hr)) {
        input_type->lpVtbl->Release(input_type);
        partylan_set_hresult_reason("decoder SetInputType H264", hr);
        partylan_decoder_cleanup(decoder);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    decoder->input_type = input_type;
    decoder->width = config->width;
    decoder->height = config->height;

    status = partylan_decoder_set_output_type(decoder);
    if (status != PARTYLAN_MEDIA_STATUS_OK) {
        partylan_decoder_cleanup(decoder);
        return status;
    }

    decoder->transform->lpVtbl->ProcessMessage(decoder->transform, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
    decoder->transform->lpVtbl->ProcessMessage(decoder->transform, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
    decoder->configured = 1;
    partylan_set_reason("Media Foundation H264 decoder configured");
    return PARTYLAN_MEDIA_STATUS_OK;
}

static PartyLANMediaStatus partylan_decoder_sample_to_rgba(PartyLANDecoder* decoder, IMFSample* sample, int64_t timestamp_us, PartyLANMediaFrame* frame) {
    IMFMediaBuffer* buffer = NULL;
    IMF2DBuffer* buffer2d = NULL;
    BYTE* src = NULL;
    BYTE* contiguous = NULL;
    LONG pitch = 0;
    DWORD max_len = 0;
    DWORD current_len = 0;
    HRESULT hr;
    uint8_t* out;
    uint64_t out_len_u64;

    if (decoder == NULL || sample == NULL || frame == NULL || decoder->width == 0 || decoder->height == 0) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    out_len_u64 = (uint64_t)decoder->width * (uint64_t)decoder->height * 4ULL;
    if (out_len_u64 == 0 || out_len_u64 > 0xffffffffULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    out = (uint8_t*)malloc((size_t)out_len_u64);
    if (out == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    hr = sample->lpVtbl->ConvertToContiguousBuffer(sample, &buffer);
    if (FAILED(hr) || buffer == NULL) {
        free(out);
        partylan_set_hresult_reason("decoder ConvertToContiguousBuffer", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = buffer->lpVtbl->QueryInterface(buffer, &IID_IMF2DBuffer, (void**)&buffer2d);
    if (SUCCEEDED(hr) && buffer2d != NULL) {
        hr = buffer2d->lpVtbl->Lock2D(buffer2d, &src, &pitch);
        if (FAILED(hr) || src == NULL) {
            buffer2d->lpVtbl->Release(buffer2d);
            buffer->lpVtbl->Release(buffer);
            free(out);
            partylan_set_hresult_reason("decoder Lock2D", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }

        partylan_nv12_to_rgba(src, pitch, decoder->width, decoder->height, out);
        buffer2d->lpVtbl->Unlock2D(buffer2d);
        buffer2d->lpVtbl->Release(buffer2d);
    } else {
        hr = buffer->lpVtbl->Lock(buffer, &contiguous, &max_len, &current_len);
        if (FAILED(hr) || contiguous == NULL) {
            buffer->lpVtbl->Release(buffer);
            free(out);
            partylan_set_hresult_reason("decoder buffer Lock", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }

        if (current_len < decoder->width * decoder->height * 3 / 2) {
            buffer->lpVtbl->Unlock(buffer);
            buffer->lpVtbl->Release(buffer);
            free(out);
            partylan_set_reason("decoder output buffer too small for NV12 frame");
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }

        (void)max_len;
        partylan_nv12_to_rgba(contiguous, (LONG)decoder->width, decoder->width, decoder->height, out);
        buffer->lpVtbl->Unlock(buffer);
    }

    buffer->lpVtbl->Release(buffer);

    frame->data = out;
    frame->data_len = (uint32_t)out_len_u64;
    frame->width = decoder->width;
    frame->height = decoder->height;
    frame->stride = decoder->width * 4;
    frame->timestamp_us = timestamp_us;
    return PARTYLAN_MEDIA_STATUS_OK;
}

PartyLANMediaStatus PartyLAN_QueryMediaCapability(PartyLANMediaCapability* capability) {
    int has_adapter;
    int has_nvenc;
    int has_decoder;

    if (capability == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    partylan_reset_strings();

    capability->supports_host = 0;
    capability->supports_view = 0;
    capability->adapter_name = PARTYLAN_ADAPTER;
    capability->driver_name = PARTYLAN_DRIVER;
    capability->reason = PARTYLAN_REASON;

    has_adapter = partylan_detect_primary_adapter();
    if (!has_adapter) {
        if (PARTYLAN_REASON[0] == '\0') {
            partylan_set_reason("DXGI adapter detection failed");
        }
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    has_decoder = partylan_detect_h264_decoder();
    capability->supports_view = has_decoder ? 1 : 0;

    has_nvenc = partylan_detect_nvenc();
    if (!has_nvenc) {
        capability->supports_host = 0;
        partylan_set_reason(has_decoder ?
            "H264 decode available; NVENC runtime (nvEncodeAPI) not found" :
            "DXGI capture available; NVENC runtime and H264 decoder unavailable");
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }

    capability->supports_host = 1;
    partylan_set_reason(has_decoder ?
        "DXGI capture, NVENC encode, and H264 decode available" :
        "DXGI capture and NVENC encode available; H264 decode unavailable");
    return PARTYLAN_MEDIA_STATUS_OK;
}

const char* PartyLAN_GetLastMediaReason(void) {
    return PARTYLAN_REASON;
}

void* PartyLAN_CreateCapturer(void) {
    PartyLANCapturer* capturer = (PartyLANCapturer*)calloc(1, sizeof(PartyLANCapturer));
    return capturer;
}

void* PartyLAN_CreateEncoder(void) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)calloc(1, sizeof(PartyLANEncoder));
    if (encoder != NULL) {
        memset(&encoder->api, 0, sizeof(encoder->api));
    }
    return encoder;
}

void* PartyLAN_CreateDecoder(void) {
    PartyLANDecoder* decoder = (PartyLANDecoder*)calloc(1, sizeof(PartyLANDecoder));
    return decoder;
}

static void partylan_capturer_cleanup(PartyLANCapturer* capturer) {
    if (capturer == NULL) {
        return;
    }

    if (capturer->staging != NULL) {
        capturer->staging->lpVtbl->Release(capturer->staging);
        capturer->staging = NULL;
    }
    if (capturer->duplication != NULL) {
        capturer->duplication->lpVtbl->Release(capturer->duplication);
        capturer->duplication = NULL;
    }
    if (capturer->context != NULL) {
        capturer->context->lpVtbl->Release(capturer->context);
        capturer->context = NULL;
    }
    if (capturer->device != NULL) {
        capturer->device->lpVtbl->Release(capturer->device);
        capturer->device = NULL;
    }
    capturer->width = 0;
    capturer->height = 0;
    capturer->stride = 0;
    capturer->started = 0;
}

PartyLANMediaStatus PartyLAN_CapturerStart(void* handle, int display_index) {
    IDXGIFactory1* factory = NULL;
    IDXGIAdapter1* adapter = NULL;
    IDXGIOutput* output = NULL;
    IDXGIOutput1* output1 = NULL;
    IDXGIOutputDuplication* duplication = NULL;
    ID3D11Device* device = NULL;
    ID3D11DeviceContext* context = NULL;
    ID3D11Texture2D* staging = NULL;
    DXGI_OUTDUPL_DESC dup_desc;
    D3D11_TEXTURE2D_DESC tex_desc;
    D3D_FEATURE_LEVEL feature_level;
    HRESULT hr;

    PartyLANCapturer* capturer = (PartyLANCapturer*)handle;
    if (capturer == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (display_index < 0) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    partylan_capturer_cleanup(capturer);

    hr = CreateDXGIFactory1(&IID_IDXGIFactory1, (void**)&factory);
    if (FAILED(hr) || factory == NULL) {
        partylan_set_reason("capturer: CreateDXGIFactory1 failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = factory->lpVtbl->EnumAdapters1(factory, 0, &adapter);
    if (FAILED(hr) || adapter == NULL) {
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: EnumAdapters1 failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = adapter->lpVtbl->EnumOutputs(adapter, (UINT)display_index, &output);
    if (FAILED(hr) || output == NULL) {
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: EnumOutputs failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = output->lpVtbl->QueryInterface(output, &IID_IDXGIOutput1, (void**)&output1);
    if (FAILED(hr) || output1 == NULL) {
        output->lpVtbl->Release(output);
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: QueryInterface IDXGIOutput1 failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = D3D11CreateDevice(
        (IDXGIAdapter*)adapter,
        D3D_DRIVER_TYPE_UNKNOWN,
        NULL,
        D3D11_CREATE_DEVICE_BGRA_SUPPORT,
        NULL,
        0,
        D3D11_SDK_VERSION,
        &device,
        &feature_level,
        &context);
    (void)feature_level;
    if (FAILED(hr) || device == NULL || context == NULL) {
        output1->lpVtbl->Release(output1);
        output->lpVtbl->Release(output);
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: D3D11CreateDevice failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    hr = output1->lpVtbl->DuplicateOutput(output1, (IUnknown*)device, &duplication);
    if (FAILED(hr) || duplication == NULL) {
        context->lpVtbl->Release(context);
        device->lpVtbl->Release(device);
        output1->lpVtbl->Release(output1);
        output->lpVtbl->Release(output);
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: DuplicateOutput failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    duplication->lpVtbl->GetDesc(duplication, &dup_desc);
    ZeroMemory(&tex_desc, sizeof(tex_desc));
    tex_desc.Width = dup_desc.ModeDesc.Width;
    tex_desc.Height = dup_desc.ModeDesc.Height;
    tex_desc.MipLevels = 1;
    tex_desc.ArraySize = 1;
    tex_desc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
    tex_desc.SampleDesc.Count = 1;
    tex_desc.Usage = D3D11_USAGE_STAGING;
    tex_desc.BindFlags = 0;
    tex_desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
    tex_desc.MiscFlags = 0;

    hr = device->lpVtbl->CreateTexture2D(device, &tex_desc, NULL, &staging);
    if (FAILED(hr) || staging == NULL) {
        duplication->lpVtbl->Release(duplication);
        context->lpVtbl->Release(context);
        device->lpVtbl->Release(device);
        output1->lpVtbl->Release(output1);
        output->lpVtbl->Release(output);
        adapter->lpVtbl->Release(adapter);
        factory->lpVtbl->Release(factory);
        partylan_set_reason("capturer: CreateTexture2D staging failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    capturer->device = device;
    capturer->context = context;
    capturer->duplication = duplication;
    capturer->staging = staging;
    capturer->width = tex_desc.Width;
    capturer->height = tex_desc.Height;
    capturer->stride = tex_desc.Width * 4;
    capturer->started = 1;

    output1->lpVtbl->Release(output1);
    output->lpVtbl->Release(output);
    adapter->lpVtbl->Release(adapter);
    factory->lpVtbl->Release(factory);
    return PARTYLAN_MEDIA_STATUS_OK;
}

PartyLANMediaStatus PartyLAN_CapturerGrabFrame(void* handle, int timeout_ms, PartyLANMediaFrame* frame) {
    PartyLANCapturer* capturer = (PartyLANCapturer*)handle;
    IDXGIResource* desktop_resource = NULL;
    ID3D11Texture2D* source_texture = NULL;
    DXGI_OUTDUPL_FRAME_INFO frame_info;
    D3D11_MAPPED_SUBRESOURCE mapped;
    HRESULT hr;

    if (capturer == NULL || frame == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (!capturer->started || capturer->duplication == NULL || capturer->context == NULL || capturer->staging == NULL) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    memset(frame, 0, sizeof(*frame));
    hr = capturer->duplication->lpVtbl->AcquireNextFrame(capturer->duplication, timeout_ms < 0 ? 16 : (UINT)timeout_ms, &frame_info, &desktop_resource);
    if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (FAILED(hr) || desktop_resource == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    hr = desktop_resource->lpVtbl->QueryInterface(desktop_resource, &IID_ID3D11Texture2D, (void**)&source_texture);
    if (FAILED(hr) || source_texture == NULL) {
        desktop_resource->lpVtbl->Release(desktop_resource);
        capturer->duplication->lpVtbl->ReleaseFrame(capturer->duplication);
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    capturer->context->lpVtbl->CopyResource(capturer->context, (ID3D11Resource*)capturer->staging, (ID3D11Resource*)source_texture);
    hr = capturer->context->lpVtbl->Map(capturer->context, (ID3D11Resource*)capturer->staging, 0, D3D11_MAP_READ, 0, &mapped);
    if (FAILED(hr)) {
        source_texture->lpVtbl->Release(source_texture);
        desktop_resource->lpVtbl->Release(desktop_resource);
        capturer->duplication->lpVtbl->ReleaseFrame(capturer->duplication);
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    {
        uint32_t row_bytes = capturer->width * 4;
        uint32_t copy_stride = (uint32_t)mapped.RowPitch;
        uint64_t total_len_u64 = (uint64_t)row_bytes * (uint64_t)capturer->height;
        uint8_t* out = (uint8_t*)malloc((size_t)total_len_u64);
        uint8_t* dst;
        uint8_t* src;
        UINT y;
        if (out == NULL) {
            capturer->context->lpVtbl->Unmap(capturer->context, (ID3D11Resource*)capturer->staging, 0);
            source_texture->lpVtbl->Release(source_texture);
            desktop_resource->lpVtbl->Release(desktop_resource);
            capturer->duplication->lpVtbl->ReleaseFrame(capturer->duplication);
            return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
        }

        dst = out;
        src = (uint8_t*)mapped.pData;
        for (y = 0; y < capturer->height; ++y) {
            memcpy(dst + ((size_t)y * row_bytes), src + ((size_t)y * copy_stride), row_bytes);
        }

        frame->data = out;
        frame->data_len = (uint32_t)total_len_u64;
        frame->width = capturer->width;
        frame->height = capturer->height;
        frame->stride = row_bytes;
        frame->timestamp_us = (int64_t)(GetTickCount64() * 1000);
    }

    capturer->context->lpVtbl->Unmap(capturer->context, (ID3D11Resource*)capturer->staging, 0);
    source_texture->lpVtbl->Release(source_texture);
    desktop_resource->lpVtbl->Release(desktop_resource);
    capturer->duplication->lpVtbl->ReleaseFrame(capturer->duplication);
    return PARTYLAN_MEDIA_STATUS_OK;
}

void PartyLAN_CapturerReleaseFrame(void* handle, PartyLANMediaFrame* frame) {
    (void)handle;
    if (frame == NULL) {
        return;
    }
    if (frame->data != NULL) {
        free(frame->data);
        frame->data = NULL;
    }
    frame->data_len = 0;
    frame->width = 0;
    frame->height = 0;
    frame->stride = 0;
    frame->timestamp_us = 0;
}

void PartyLAN_CapturerStop(void* handle) {
    PartyLANCapturer* capturer = (PartyLANCapturer*)handle;
    if (capturer == NULL) {
        return;
    }
    partylan_capturer_cleanup(capturer);
}

PartyLANMediaStatus PartyLAN_EncoderConfigure(void* handle, const PartyLANMediaEncoderConfig* config) {
    PartyLANNvEncodeApiCreateInstanceFn create_instance = NULL;
    PartyLANNvEncodeApiGetMaxSupportedVersionFn get_max_supported_version = NULL;
    ID3D11Device* d3d_device = NULL;
    ID3D11DeviceContext* d3d_context = NULL;
    D3D_FEATURE_LEVEL feature_level;
    HRESULT hr;
    NV_ENC_OPEN_ENCODE_SESSION_EX_PARAMS open_params;
    NV_ENC_PRESET_CONFIG preset_config;
    NV_ENC_CONFIG encode_config;
    NV_ENC_INITIALIZE_PARAMS init_params;
    NV_ENC_CREATE_INPUT_BUFFER input_buffer_params;
    NV_ENC_CREATE_BITSTREAM_BUFFER bitstream_buffer_params;
    NVENCSTATUS nv_status;
    uint32_t max_supported_version = 0;
    uint32_t max_supported_api_version = 0;
    uint32_t negotiated_api_version = NVENCAPI_VERSION;
    uint32_t bitrate_bps;
    uint32_t keyframe_interval;
    int preset_ready = 0;
    NV_ENC_BUFFER_FORMAT input_format = NV_ENC_BUFFER_FORMAT_ABGR;
    PartyLANCapturer* bound_capturer = NULL;
    PartyLANEncoder* encoder = (PartyLANEncoder*)handle;
    if (encoder == NULL || config == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (config->width == 0 || config->height == 0 || config->fps == 0) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (config->pixel_format == 1) {
        input_format = NV_ENC_BUFFER_FORMAT_ARGB;
    }
    if (config->capturer_handle != 0) {
        bound_capturer = (PartyLANCapturer*)(uintptr_t)config->capturer_handle;
        if (bound_capturer == NULL || !bound_capturer->started || bound_capturer->device == NULL || bound_capturer->context == NULL) {
            partylan_set_reason("direct encode: invalid capturer handle");
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
    }

    partylan_encoder_cleanup_runtime(encoder);

    if (!partylan_detect_nvenc()) {
        partylan_set_reason("NVENC runtime (nvEncodeAPI) not found");
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }

    if (encoder->nvenc_lib == NULL) {
        encoder->nvenc_lib = LoadLibraryA("nvEncodeAPI64.dll");
        if (encoder->nvenc_lib == NULL) {
            encoder->nvenc_lib = LoadLibraryA("nvEncodeAPI.dll");
        }
        if (encoder->nvenc_lib == NULL) {
            partylan_set_reason("LoadLibrary nvEncodeAPI failed");
            return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
        }
    }

    create_instance = (PartyLANNvEncodeApiCreateInstanceFn)GetProcAddress(encoder->nvenc_lib, "NvEncodeAPICreateInstance");
    if (create_instance == NULL) {
        partylan_set_reason("NvEncodeAPICreateInstance symbol missing");
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    get_max_supported_version = (PartyLANNvEncodeApiGetMaxSupportedVersionFn)GetProcAddress(encoder->nvenc_lib, "NvEncodeAPIGetMaxSupportedVersion");
    if (get_max_supported_version != NULL) {
        nv_status = get_max_supported_version(&max_supported_version);
        if (nv_status != NV_ENC_SUCCESS) {
            partylan_set_nv_reason("NvEncodeAPIGetMaxSupportedVersion", nv_status);
            return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
        }
        max_supported_api_version = partylan_pack_max_supported_api_version(max_supported_version);
        if (max_supported_api_version != 0 &&
            partylan_nvenc_api_version_less(max_supported_api_version, negotiated_api_version)) {
            negotiated_api_version = max_supported_api_version;
        }
    }
    encoder->api_version = negotiated_api_version;

    memset(&encoder->api, 0, sizeof(encoder->api));
    encoder->api.version = partylan_nvenc_struct_version(encoder->api_version, 2);
    nv_status = create_instance(&encoder->api);
    if (nv_status != NV_ENC_SUCCESS) {
        partylan_set_nv_reason("NvEncodeAPICreateInstance", nv_status);
        encoder->api_ready = 0;
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }

    if (encoder->api.nvEncOpenEncodeSessionEx == NULL ||
        encoder->api.nvEncInitializeEncoder == NULL ||
        encoder->api.nvEncCreateInputBuffer == NULL ||
        encoder->api.nvEncDestroyInputBuffer == NULL ||
        encoder->api.nvEncCreateBitstreamBuffer == NULL ||
        encoder->api.nvEncDestroyBitstreamBuffer == NULL ||
        encoder->api.nvEncEncodePicture == NULL ||
        encoder->api.nvEncLockBitstream == NULL ||
        encoder->api.nvEncUnlockBitstream == NULL ||
        encoder->api.nvEncLockInputBuffer == NULL ||
        encoder->api.nvEncUnlockInputBuffer == NULL ||
        encoder->api.nvEncDestroyEncoder == NULL) {
        partylan_set_reason("NVENC API table missing required functions");
        encoder->api_ready = 0;
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (bound_capturer != NULL &&
        (encoder->api.nvEncRegisterResource == NULL ||
         encoder->api.nvEncUnregisterResource == NULL ||
         encoder->api.nvEncMapInputResource == NULL ||
         encoder->api.nvEncUnmapInputResource == NULL)) {
        partylan_set_reason("NVENC API table missing direct resource functions");
        encoder->api_ready = 0;
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    encoder->api_ready = 1;

    if (bound_capturer != NULL) {
        d3d_device = bound_capturer->device;
        d3d_context = bound_capturer->context;
        d3d_device->lpVtbl->AddRef(d3d_device);
        d3d_context->lpVtbl->AddRef(d3d_context);
    } else {
        hr = D3D11CreateDevice(
            NULL,
            D3D_DRIVER_TYPE_HARDWARE,
            NULL,
            D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            NULL,
            0,
            D3D11_SDK_VERSION,
            &d3d_device,
            &feature_level,
            &d3d_context);
        (void)feature_level;
        if (FAILED(hr) || d3d_device == NULL || d3d_context == NULL) {
            partylan_set_reason("D3D11CreateDevice for NVENC failed");
            partylan_encoder_cleanup_runtime(encoder);
            return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
        }
    }

    memset(&open_params, 0, sizeof(open_params));
    open_params.version = partylan_nvenc_struct_version(encoder->api_version, 1);
    open_params.deviceType = NV_ENC_DEVICE_TYPE_DIRECTX;
    open_params.device = d3d_device;
    open_params.apiVersion = encoder->api_version;

    nv_status = encoder->api.nvEncOpenEncodeSessionEx(&open_params, &encoder->session);
    if (nv_status != NV_ENC_SUCCESS || encoder->session == NULL) {
        char reason_buf[256];
        snprintf(reason_buf, sizeof(reason_buf), "nvEncOpenEncodeSessionEx failed: %d", (int)nv_status);
        partylan_append_api_version_reason(reason_buf, sizeof(reason_buf), encoder->api_version, max_supported_version);
        partylan_set_reason(reason_buf);
        if (d3d_context != NULL) {
            d3d_context->lpVtbl->Release(d3d_context);
        }
        if (d3d_device != NULL) {
            d3d_device->lpVtbl->Release(d3d_device);
        }
        partylan_encoder_cleanup_runtime(encoder);
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }
    encoder->d3d_device = d3d_device;
    encoder->d3d_context = d3d_context;

    memset(&preset_config, 0, sizeof(preset_config));
    preset_config.version = partylan_nvenc_struct_version_ex(encoder->api_version, 5);
    preset_config.presetCfg.version = partylan_nvenc_struct_version_ex(encoder->api_version, 9);
    if (encoder->api.nvEncGetEncodePresetConfigEx != NULL) {
        nv_status = encoder->api.nvEncGetEncodePresetConfigEx(
            encoder->session,
            NV_ENC_CODEC_H264_GUID,
            NV_ENC_PRESET_P4_GUID,
            NV_ENC_TUNING_INFO_LOW_LATENCY,
            &preset_config);
        if (nv_status == NV_ENC_SUCCESS) {
            preset_ready = 1;
        }
    }
    if (!preset_ready && encoder->api.nvEncGetEncodePresetConfig != NULL) {
        nv_status = encoder->api.nvEncGetEncodePresetConfig(
            encoder->session,
            NV_ENC_CODEC_H264_GUID,
            NV_ENC_PRESET_P4_GUID,
            &preset_config);
        if (nv_status == NV_ENC_SUCCESS) {
            preset_ready = 1;
        }
    }
    if (!preset_ready) {
        partylan_set_reason("nvEncGetEncodePresetConfig failed");
        partylan_encoder_cleanup_runtime(encoder);
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }

    encode_config = preset_config.presetCfg;
    encode_config.version = partylan_nvenc_struct_version_ex(encoder->api_version, 9);
    keyframe_interval = config->fps > 0 ? config->fps * 2U : 60U;
    encode_config.gopLength = keyframe_interval;
    encode_config.frameIntervalP = 1;
    encode_config.profileGUID = NV_ENC_H264_PROFILE_BASELINE_GUID;
    encode_config.rcParams.version = partylan_nvenc_struct_version(encoder->api_version, 1);
    encode_config.rcParams.rateControlMode = NV_ENC_PARAMS_RC_CBR;
    bitrate_bps = config->bitrate_kbps > 0 ? config->bitrate_kbps * 1000U : 2000000U;
    encode_config.rcParams.averageBitRate = bitrate_bps;
    encode_config.rcParams.maxBitRate = bitrate_bps;
    encode_config.rcParams.vbvBufferSize = bitrate_bps;
    encode_config.rcParams.vbvInitialDelay = bitrate_bps / 2U;
    encode_config.rcParams.zeroReorderDelay = 1;
    encode_config.encodeCodecConfig.h264Config.idrPeriod = keyframe_interval;
    encode_config.encodeCodecConfig.h264Config.level = NV_ENC_LEVEL_AUTOSELECT;
    encode_config.encodeCodecConfig.h264Config.chromaFormatIDC = 1;
    encode_config.encodeCodecConfig.h264Config.disableSPSPPS = 0;
    encode_config.encodeCodecConfig.h264Config.outputAUD = 1;
    encode_config.encodeCodecConfig.h264Config.repeatSPSPPS = 1;

    memset(&init_params, 0, sizeof(init_params));
    init_params.version = partylan_nvenc_struct_version_ex(encoder->api_version, 7);
    init_params.encodeGUID = NV_ENC_CODEC_H264_GUID;
    init_params.presetGUID = NV_ENC_PRESET_P4_GUID;
    init_params.encodeWidth = config->width;
    init_params.encodeHeight = config->height;
    init_params.darWidth = config->width;
    init_params.darHeight = config->height;
    init_params.frameRateNum = config->fps;
    init_params.frameRateDen = 1;
    init_params.enableEncodeAsync = 0;
    init_params.enablePTD = 1;
    init_params.tuningInfo = NV_ENC_TUNING_INFO_LOW_LATENCY;
    init_params.encodeConfig = &encode_config;
    init_params.maxEncodeWidth = config->width;
    init_params.maxEncodeHeight = config->height;

    nv_status = encoder->api.nvEncInitializeEncoder(encoder->session, &init_params);
    if (nv_status != NV_ENC_SUCCESS) {
        partylan_set_nv_reason("nvEncInitializeEncoder", nv_status);
        partylan_encoder_cleanup_runtime(encoder);
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }

    memset(&input_buffer_params, 0, sizeof(input_buffer_params));
    input_buffer_params.version = partylan_nvenc_struct_version(encoder->api_version, 2);
    input_buffer_params.width = config->width;
    input_buffer_params.height = config->height;
    input_buffer_params.bufferFmt = input_format;

    nv_status = encoder->api.nvEncCreateInputBuffer(encoder->session, &input_buffer_params);
    if (nv_status != NV_ENC_SUCCESS || input_buffer_params.inputBuffer == NULL) {
        partylan_set_nv_reason("nvEncCreateInputBuffer", nv_status);
        partylan_encoder_cleanup_runtime(encoder);
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }
    encoder->input_buffer = input_buffer_params.inputBuffer;

    memset(&bitstream_buffer_params, 0, sizeof(bitstream_buffer_params));
    bitstream_buffer_params.version = partylan_nvenc_struct_version(encoder->api_version, 1);

    nv_status = encoder->api.nvEncCreateBitstreamBuffer(encoder->session, &bitstream_buffer_params);
    if (nv_status != NV_ENC_SUCCESS || bitstream_buffer_params.bitstreamBuffer == NULL) {
        partylan_set_nv_reason("nvEncCreateBitstreamBuffer", nv_status);
        partylan_encoder_cleanup_runtime(encoder);
        return PARTYLAN_MEDIA_STATUS_UNSUPPORTED_GPU;
    }
    encoder->bitstream_buffer = bitstream_buffer_params.bitstreamBuffer;

    encoder->config = *config;
    encoder->configured = 1;
    encoder->input_format = input_format;
    encoder->frame_index = 0;
    encoder->force_idr = 1;
    partylan_set_reason("NVENC encoder configured");
    return PARTYLAN_MEDIA_STATUS_OK;
}

PartyLANMediaStatus PartyLAN_EncoderEncodeRGBA(void* handle, const uint8_t* rgba, uint32_t rgba_len, int64_t timestamp_us, PartyLANMediaPacket* packet) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)handle;
    NV_ENC_LOCK_INPUT_BUFFER lock_input;
    NV_ENC_PIC_PARAMS pic_params;
    NVENCSTATUS nv_status;
    uint32_t expected_len;
    uint32_t frame_idx;
    uint32_t y;
    const uint8_t* src;
    uint8_t* dst;
    uint32_t src_pitch;
    if (encoder == NULL || packet == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (!encoder->configured || !encoder->api_ready || encoder->session == NULL || encoder->input_buffer == NULL || encoder->bitstream_buffer == NULL || rgba == NULL || rgba_len == 0) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    packet->data = NULL;
    packet->data_len = 0;
    packet->timestamp_us = 0;

    expected_len = encoder->config.width * encoder->config.height * 4U;
    if (rgba_len < expected_len || expected_len == 0) {
        partylan_set_reason("input frame buffer size mismatch");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    memset(&lock_input, 0, sizeof(lock_input));
    lock_input.version = partylan_nvenc_struct_version(encoder->api_version, 1);
    lock_input.inputBuffer = encoder->input_buffer;
    nv_status = encoder->api.nvEncLockInputBuffer(encoder->session, &lock_input);
    if (nv_status != NV_ENC_SUCCESS || lock_input.bufferDataPtr == NULL || lock_input.pitch == 0) {
        if (nv_status == NV_ENC_ERR_ENCODER_BUSY || nv_status == NV_ENC_ERR_LOCK_BUSY) {
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        partylan_set_nv_reason("nvEncLockInputBuffer", nv_status);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    src_pitch = encoder->config.width * 4U;
    src = rgba;
    dst = (uint8_t*)lock_input.bufferDataPtr;
    for (y = 0; y < encoder->config.height; ++y) {
        memcpy(dst + ((size_t)y * lock_input.pitch), src + ((size_t)y * src_pitch), src_pitch);
    }

    nv_status = encoder->api.nvEncUnlockInputBuffer(encoder->session, encoder->input_buffer);
    if (nv_status != NV_ENC_SUCCESS) {
        partylan_set_nv_reason("nvEncUnlockInputBuffer", nv_status);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    memset(&pic_params, 0, sizeof(pic_params));
    pic_params.version = partylan_nvenc_struct_version_ex(encoder->api_version, 7);
    pic_params.inputWidth = encoder->config.width;
    pic_params.inputHeight = encoder->config.height;
    pic_params.inputPitch = lock_input.pitch;
    frame_idx = encoder->frame_index++;
    pic_params.frameIdx = frame_idx;
    pic_params.inputTimeStamp = timestamp_us > 0 ? (uint64_t)timestamp_us : (uint64_t)0;
    pic_params.inputBuffer = encoder->input_buffer;
    pic_params.outputBitstream = encoder->bitstream_buffer;
    pic_params.bufferFmt = encoder->input_format;
    pic_params.pictureStruct = NV_ENC_PIC_STRUCT_FRAME;
    if (partylan_encoder_should_emit_idr(encoder, frame_idx)) {
        pic_params.encodePicFlags = NV_ENC_PIC_FLAG_FORCEIDR | NV_ENC_PIC_FLAG_OUTPUT_SPSPPS;
    }

    nv_status = encoder->api.nvEncEncodePicture(encoder->session, &pic_params);
    if (nv_status == NV_ENC_ERR_NEED_MORE_INPUT) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (nv_status != NV_ENC_SUCCESS) {
        if (nv_status == NV_ENC_ERR_ENCODER_BUSY || nv_status == NV_ENC_ERR_LOCK_BUSY) {
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        partylan_set_nv_reason("nvEncEncodePicture", nv_status);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    encoder->force_idr = 0;
    return partylan_encoder_collect_bitstream(encoder, timestamp_us, packet);
}

PartyLANMediaStatus PartyLAN_EncoderEncodeFromCapturer(void* encoder_handle, void* capturer_handle, int timeout_ms, PartyLANMediaPacket* packet) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)encoder_handle;
    PartyLANCapturer* capturer = (PartyLANCapturer*)capturer_handle;
    IDXGIResource* desktop_resource = NULL;
    ID3D11Texture2D* source_texture = NULL;
    DXGI_OUTDUPL_FRAME_INFO frame_info;
    D3D11_TEXTURE2D_DESC src_desc;
    NV_ENC_MAP_INPUT_RESOURCE map_input;
    NV_ENC_PIC_PARAMS pic_params;
    NVENCSTATUS nv_status;
    HRESULT hr;
    PartyLANMediaStatus status;
    uint32_t frame_idx;
    int frame_acquired = 0;
    int mapped = 0;
    int64_t timestamp_us;

    if (encoder == NULL || capturer == NULL || packet == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (!encoder->configured || !encoder->api_ready || encoder->session == NULL || encoder->bitstream_buffer == NULL ||
        !capturer->started || capturer->duplication == NULL || capturer->context == NULL) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (encoder->api.nvEncMapInputResource == NULL || encoder->api.nvEncUnmapInputResource == NULL ||
        encoder->api.nvEncRegisterResource == NULL || encoder->api.nvEncUnregisterResource == NULL) {
        partylan_set_reason("direct encode: required NVENC resource APIs unavailable");
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }

    packet->data = NULL;
    packet->data_len = 0;
    packet->timestamp_us = 0;

    hr = capturer->duplication->lpVtbl->AcquireNextFrame(
        capturer->duplication,
        timeout_ms < 0 ? 16 : (UINT)timeout_ms,
        &frame_info,
        &desktop_resource);
    if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
        if (encoder->input_texture != NULL && encoder->registered_input != NULL) {
            timestamp_us = (int64_t)(GetTickCount64() * 1000);
            goto encode_current_texture;
        }
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (FAILED(hr) || desktop_resource == NULL) {
        partylan_set_reason("direct encode: AcquireNextFrame failed");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    frame_acquired = 1;

    hr = desktop_resource->lpVtbl->QueryInterface(desktop_resource, &IID_ID3D11Texture2D, (void**)&source_texture);
    if (FAILED(hr) || source_texture == NULL) {
        partylan_set_reason("direct encode: frame QueryInterface failed");
        status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        goto cleanup;
    }

    source_texture->lpVtbl->GetDesc(source_texture, &src_desc);
    if (src_desc.Width != encoder->config.width || src_desc.Height != encoder->config.height) {
        partylan_set_reason("direct encode: capture size != encode target");
        status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        goto cleanup;
    }

    status = partylan_encoder_prepare_direct_input(encoder, src_desc.Width, src_desc.Height);
    if (status != PARTYLAN_MEDIA_STATUS_OK) {
        goto cleanup;
    }

    encoder->d3d_context->lpVtbl->CopyResource(
        encoder->d3d_context,
        (ID3D11Resource*)encoder->input_texture,
        (ID3D11Resource*)source_texture);

    timestamp_us = (int64_t)(GetTickCount64() * 1000);

encode_current_texture:
    memset(&map_input, 0, sizeof(map_input));
    map_input.version = partylan_nvenc_struct_version(encoder->api_version, 4);
    map_input.registeredResource = encoder->registered_input;
    nv_status = encoder->api.nvEncMapInputResource(encoder->session, &map_input);
    if (nv_status != NV_ENC_SUCCESS || map_input.mappedResource == NULL) {
        if (nv_status == NV_ENC_ERR_ENCODER_BUSY || nv_status == NV_ENC_ERR_LOCK_BUSY) {
            status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
            goto cleanup;
        }
        partylan_set_nv_reason("nvEncMapInputResource", nv_status);
        status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        goto cleanup;
    }
    mapped = 1;

    memset(&pic_params, 0, sizeof(pic_params));
    pic_params.version = partylan_nvenc_struct_version_ex(encoder->api_version, 7);
    pic_params.inputWidth = encoder->config.width;
    pic_params.inputHeight = encoder->config.height;
    frame_idx = encoder->frame_index++;
    pic_params.frameIdx = frame_idx;
    pic_params.inputTimeStamp = (uint64_t)timestamp_us;
    pic_params.inputBuffer = map_input.mappedResource;
    pic_params.outputBitstream = encoder->bitstream_buffer;
    pic_params.bufferFmt = map_input.mappedBufferFmt == NV_ENC_BUFFER_FORMAT_UNDEFINED ? NV_ENC_BUFFER_FORMAT_ARGB : map_input.mappedBufferFmt;
    pic_params.pictureStruct = NV_ENC_PIC_STRUCT_FRAME;
    if (partylan_encoder_should_emit_idr(encoder, frame_idx)) {
        pic_params.encodePicFlags = NV_ENC_PIC_FLAG_FORCEIDR | NV_ENC_PIC_FLAG_OUTPUT_SPSPPS;
    }

    nv_status = encoder->api.nvEncEncodePicture(encoder->session, &pic_params);
    if (nv_status == NV_ENC_ERR_NEED_MORE_INPUT || nv_status == NV_ENC_ERR_ENCODER_BUSY || nv_status == NV_ENC_ERR_LOCK_BUSY) {
        status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        goto cleanup;
    }
    if (nv_status != NV_ENC_SUCCESS) {
        partylan_set_nv_reason("nvEncEncodePicture(direct)", nv_status);
        status = PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        goto cleanup;
    }
    encoder->force_idr = 0;

    status = partylan_encoder_collect_bitstream(encoder, timestamp_us, packet);

cleanup:
    if (mapped) {
        encoder->api.nvEncUnmapInputResource(encoder->session, map_input.mappedResource);
    }
    if (source_texture != NULL) {
        source_texture->lpVtbl->Release(source_texture);
    }
    if (desktop_resource != NULL) {
        desktop_resource->lpVtbl->Release(desktop_resource);
    }
    if (frame_acquired) {
        capturer->duplication->lpVtbl->ReleaseFrame(capturer->duplication);
    }
    return status;
}

PartyLANMediaStatus PartyLAN_EncoderRequestKeyFrame(void* handle) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)handle;
    if (encoder == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (!encoder->configured) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    encoder->force_idr = 1;
    return PARTYLAN_MEDIA_STATUS_OK;
}

void PartyLAN_EncoderReleasePacket(void* handle, PartyLANMediaPacket* packet) {
    (void)handle;
    if (packet == NULL) {
        return;
    }
    if (packet->data != NULL) {
        free(packet->data);
        packet->data = NULL;
    }
    packet->data_len = 0;
    packet->timestamp_us = 0;
}

void PartyLAN_EncoderClose(void* handle) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)handle;
    if (encoder == NULL) {
        return;
    }
    partylan_encoder_cleanup_runtime(encoder);
    encoder->api_ready = 0;
    memset(&encoder->api, 0, sizeof(encoder->api));
}

PartyLANMediaStatus PartyLAN_DecoderConfigure(void* handle, const PartyLANMediaDecoderConfig* config) {
    return partylan_decoder_configure((PartyLANDecoder*)handle, config);
}

PartyLANMediaStatus PartyLAN_DecoderDecodeAnnexB(void* handle, const uint8_t* data, uint32_t data_len, int64_t timestamp_us, PartyLANMediaFrame* frame) {
    PartyLANDecoder* decoder = (PartyLANDecoder*)handle;
    IMFMediaBuffer* input_buffer = NULL;
    IMFSample* input_sample = NULL;
    IMFSample* output_sample = NULL;
    IMFMediaBuffer* output_buffer = NULL;
    MFT_OUTPUT_STREAM_INFO output_info;
    MFT_OUTPUT_DATA_BUFFER output_data;
    BYTE* dst = NULL;
    HRESULT hr;
    DWORD max_len = 0;
    DWORD status = 0;
    DWORD output_status = 0;
    DWORD output_size;
    PartyLANMediaStatus decode_status;

    if (decoder == NULL || data == NULL || data_len == 0 || frame == NULL) {
        return PARTYLAN_MEDIA_STATUS_INTERNAL_ERROR;
    }
    if (!decoder->configured || decoder->transform == NULL) {
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    memset(frame, 0, sizeof(*frame));

    hr = MFCreateMemoryBuffer(data_len, &input_buffer);
    if (FAILED(hr) || input_buffer == NULL) {
        partylan_set_hresult_reason("decoder MFCreateMemoryBuffer input", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    hr = input_buffer->lpVtbl->Lock(input_buffer, &dst, &max_len, NULL);
    if (FAILED(hr) || dst == NULL || max_len < data_len) {
        input_buffer->lpVtbl->Release(input_buffer);
        partylan_set_hresult_reason("decoder input Lock", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    memcpy(dst, data, data_len);
    input_buffer->lpVtbl->Unlock(input_buffer);
    input_buffer->lpVtbl->SetCurrentLength(input_buffer, data_len);

    hr = MFCreateSample(&input_sample);
    if (FAILED(hr) || input_sample == NULL) {
        input_buffer->lpVtbl->Release(input_buffer);
        partylan_set_hresult_reason("decoder MFCreateSample input", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    input_sample->lpVtbl->AddBuffer(input_sample, input_buffer);
    input_buffer->lpVtbl->Release(input_buffer);
    input_sample->lpVtbl->SetSampleTime(input_sample, timestamp_us * 10);

    hr = decoder->transform->lpVtbl->ProcessInput(decoder->transform, 0, input_sample, 0);
    input_sample->lpVtbl->Release(input_sample);
    if (FAILED(hr)) {
        if (hr == MF_E_NOTACCEPTING) {
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        partylan_set_hresult_reason("decoder ProcessInput", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    memset(&output_info, 0, sizeof(output_info));
    hr = decoder->transform->lpVtbl->GetOutputStreamInfo(decoder->transform, 0, &output_info);
    if (FAILED(hr)) {
        partylan_set_hresult_reason("decoder GetOutputStreamInfo", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    if ((output_info.dwFlags & MFT_OUTPUT_STREAM_PROVIDES_SAMPLES) == 0) {
        output_size = output_info.cbSize;
        if (output_size < decoder->width * decoder->height * 3 / 2) {
            output_size = decoder->width * decoder->height * 3 / 2;
        }

        hr = MFCreateSample(&output_sample);
        if (FAILED(hr) || output_sample == NULL) {
            partylan_set_hresult_reason("decoder MFCreateSample output", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        hr = MFCreateMemoryBuffer(output_size, &output_buffer);
        if (FAILED(hr) || output_buffer == NULL) {
            output_sample->lpVtbl->Release(output_sample);
            partylan_set_hresult_reason("decoder MFCreateMemoryBuffer output", hr);
            return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
        }
        output_sample->lpVtbl->AddBuffer(output_sample, output_buffer);
        output_buffer->lpVtbl->Release(output_buffer);
    }

    memset(&output_data, 0, sizeof(output_data));
    output_data.dwStreamID = 0;
    output_data.pSample = output_sample;

    hr = decoder->transform->lpVtbl->ProcessOutput(decoder->transform, 0, 1, &output_data, &output_status);
    if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
        if (output_sample != NULL) {
            output_sample->lpVtbl->Release(output_sample);
        }
        if (output_data.pEvents != NULL) {
            output_data.pEvents->lpVtbl->Release(output_data.pEvents);
        }
        (void)partylan_decoder_set_output_type(decoder);
        partylan_set_reason("decoder output stream changed; output type reset");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT) {
        if (output_sample != NULL) {
            output_sample->lpVtbl->Release(output_sample);
        }
        if (output_data.pEvents != NULL) {
            output_data.pEvents->lpVtbl->Release(output_data.pEvents);
        }
        partylan_set_reason("decoder needs more input");
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }
    if (FAILED(hr) || output_data.pSample == NULL) {
        if (output_sample != NULL) {
            output_sample->lpVtbl->Release(output_sample);
        }
        if (output_data.pEvents != NULL) {
            output_data.pEvents->lpVtbl->Release(output_data.pEvents);
        }
        partylan_set_hresult_reason("decoder ProcessOutput", hr);
        return PARTYLAN_MEDIA_STATUS_UNAVAILABLE;
    }

    decode_status = partylan_decoder_sample_to_rgba(decoder, output_data.pSample, timestamp_us, frame);

    if (output_data.pSample != NULL) {
        output_data.pSample->lpVtbl->Release(output_data.pSample);
    }
    if (output_data.pEvents != NULL) {
        output_data.pEvents->lpVtbl->Release(output_data.pEvents);
    }
    (void)status;
    return decode_status;
}

void PartyLAN_DecoderReleaseFrame(void* handle, PartyLANMediaFrame* frame) {
    (void)handle;
    if (frame == NULL) {
        return;
    }
    if (frame->data != NULL) {
        free(frame->data);
        frame->data = NULL;
    }
    frame->data_len = 0;
    frame->width = 0;
    frame->height = 0;
    frame->stride = 0;
    frame->timestamp_us = 0;
}

void PartyLAN_DecoderClose(void* handle) {
    PartyLANDecoder* decoder = (PartyLANDecoder*)handle;
    if (decoder == NULL) {
        return;
    }
    partylan_decoder_cleanup(decoder);
}

void PartyLAN_DestroyCapturer(void* handle) {
    PartyLANCapturer* capturer = (PartyLANCapturer*)handle;
    if (capturer == NULL) {
        return;
    }
    partylan_capturer_cleanup(capturer);
    free(capturer);
}

void PartyLAN_DestroyEncoder(void* handle) {
    PartyLANEncoder* encoder = (PartyLANEncoder*)handle;
    if (encoder == NULL) {
        return;
    }
    PartyLAN_EncoderClose(handle);
    if (encoder->nvenc_lib != NULL) {
        FreeLibrary(encoder->nvenc_lib);
        encoder->nvenc_lib = NULL;
    }
    free(encoder);
}

void PartyLAN_DestroyDecoder(void* handle) {
    PartyLANDecoder* decoder = (PartyLANDecoder*)handle;
    if (decoder == NULL) {
        return;
    }
    partylan_decoder_cleanup(decoder);
    partylan_decoder_shutdown_platform(decoder);
    free(decoder);
}
