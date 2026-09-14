#define WIN32_LEAN_AND_MEAN
#define CINTERFACE
#define COBJMACROS

#include "bridge.h"

#include <d3d11.h>
#include <dxgi.h>
#include <inspectable.h>
#include <roapi.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <windows.foundation.h>
#include <windows.graphics.capture.h>
#include <windows.graphics.capture.interop.h>
#include <windows.graphics.directx.direct3d11.h>
#include <winstring.h>

STDAPI CreateDirect3D11DeviceFromDXGIDevice(
    IDXGIDevice* dxgi_device,
    IInspectable** graphics_device);

typedef struct PartyLANIDirect3DDxgiInterfaceAccess PartyLANIDirect3DDxgiInterfaceAccess;

typedef struct PartyLANIDirect3DDxgiInterfaceAccessVtbl {
    HRESULT (STDMETHODCALLTYPE *QueryInterface)(
        PartyLANIDirect3DDxgiInterfaceAccess* self,
        REFIID iid,
        void** result);
    ULONG (STDMETHODCALLTYPE *AddRef)(PartyLANIDirect3DDxgiInterfaceAccess* self);
    ULONG (STDMETHODCALLTYPE *Release)(PartyLANIDirect3DDxgiInterfaceAccess* self);
    HRESULT (STDMETHODCALLTYPE *GetInterface)(
        PartyLANIDirect3DDxgiInterfaceAccess* self,
        REFIID iid,
        void** result);
} PartyLANIDirect3DDxgiInterfaceAccessVtbl;

struct PartyLANIDirect3DDxgiInterfaceAccess {
    const PartyLANIDirect3DDxgiInterfaceAccessVtbl* lpVtbl;
};

static const IID PARTYLAN_IID_IDIRECT3D_DXGI_INTERFACE_ACCESS = {
    0xa9b3d012,
    0x3df2,
    0x4ee3,
    {0xb8, 0xd1, 0x86, 0x95, 0xf4, 0x57, 0xd3, 0xc1}
};

static const IID PARTYLAN_IID_ICLOSABLE = {
    0x30d5a829,
    0x7fa4,
    0x4026,
    {0x83, 0xbb, 0xd7, 0x5b, 0xae, 0x4e, 0xa9, 0x9e}
};

static const IID PARTYLAN_IID_WINRT_D3D_DEVICE = {
    0xa37624ab,
    0x8d5f,
    0x4650,
    {0x9d, 0x3e, 0x9e, 0xae, 0x3d, 0x9b, 0xc6, 0x70}
};

static const IID PARTYLAN_IID_GRAPHICS_CAPTURE_ITEM_INTEROP = {
    0x3628e81b,
    0x3cac,
    0x4c60,
    {0xb7, 0xf4, 0x23, 0xce, 0x0e, 0x0c, 0x33, 0x56}
};

static const IID PARTYLAN_IID_GRAPHICS_CAPTURE_ITEM = {
    0x79c3f95b,
    0x31f7,
    0x4ec2,
    {0xa4, 0x64, 0x63, 0x2e, 0xf5, 0xd3, 0x07, 0x60}
};

static const IID PARTYLAN_IID_FRAME_POOL_STATICS2 = {
    0x589b103f,
    0x6bbc,
    0x5df5,
    {0xa9, 0x91, 0x02, 0xe2, 0x8b, 0x3b, 0x66, 0xd5}
};

typedef __x_ABI_CWindows_CGraphics_CCapture_CIDirect3D11CaptureFrame PartyLANWGCFrame;
typedef __x_ABI_CWindows_CGraphics_CCapture_CIDirect3D11CaptureFramePool PartyLANWGCFramePool;
typedef __x_ABI_CWindows_CGraphics_CCapture_CIDirect3D11CaptureFramePoolStatics2 PartyLANWGCFramePoolStatics;
typedef __x_ABI_CWindows_CGraphics_CCapture_CIGraphicsCaptureItem PartyLANWGCItem;
typedef __x_ABI_CWindows_CGraphics_CCapture_CIGraphicsCaptureSession PartyLANWGCSession;
typedef __x_ABI_CWindows_CGraphics_CDirectX_CDirect3D11_CIDirect3DDevice PartyLANWinRTD3DDevice;
typedef __x_ABI_CWindows_CGraphics_CDirectX_CDirect3D11_CIDirect3DSurface PartyLANWinRTD3DSurface;
typedef __x_ABI_CWindows_CFoundation_CIClosable PartyLANClosable;
typedef __x_ABI_CWindows_CGraphics_CSizeInt32 PartyLANSizeInt32;

typedef struct PartyLANWindowCapturer {
    HWND window;
    ID3D11Device* device;
    ID3D11DeviceContext* context;
    ID3D11Texture2D* staging;
    PartyLANWinRTD3DDevice* winrt_device;
    PartyLANWGCItem* item;
    PartyLANWGCFramePool* frame_pool;
    PartyLANWGCSession* session;
    UINT staging_width;
    UINT staging_height;
    DXGI_FORMAT staging_format;
    UINT content_width;
    UINT content_height;
    int started;
    char reason[384];
} PartyLANWindowCapturer;

static void partylan_wgc_set_reason(PartyLANWindowCapturer* capturer, const char* reason) {
    if (capturer == NULL) {
        return;
    }
    if (reason == NULL) {
        capturer->reason[0] = '\0';
        return;
    }
    strncpy(capturer->reason, reason, sizeof(capturer->reason) - 1);
    capturer->reason[sizeof(capturer->reason) - 1] = '\0';
}

static void partylan_wgc_set_hresult_reason(
    PartyLANWindowCapturer* capturer,
    const char* step,
    HRESULT hr) {
    char buffer[384];
    snprintf(
        buffer,
        sizeof(buffer),
        "%s failed: 0x%08lx",
        step == NULL ? "Windows Graphics Capture" : step,
        (unsigned long)hr);
    partylan_wgc_set_reason(capturer, buffer);
}

static HRESULT partylan_wgc_enter_runtime(int* should_uninitialize) {
    HRESULT hr;
    if (should_uninitialize == NULL) {
        return E_INVALIDARG;
    }
    *should_uninitialize = 0;
    hr = RoInitialize(RO_INIT_MULTITHREADED);
    if (hr == S_OK || hr == S_FALSE) {
        *should_uninitialize = 1;
        return S_OK;
    }
    if (hr == RPC_E_CHANGED_MODE) {
        return S_OK;
    }
    return hr;
}

static void partylan_wgc_leave_runtime(int should_uninitialize) {
    if (should_uninitialize) {
        RoUninitialize();
    }
}

static void partylan_wgc_close_object(IUnknown* object) {
    PartyLANClosable* closable = NULL;
    if (object == NULL) {
        return;
    }
    if (SUCCEEDED(object->lpVtbl->QueryInterface(
            object,
            &PARTYLAN_IID_ICLOSABLE,
            (void**)&closable)) &&
        closable != NULL) {
        closable->lpVtbl->Close(closable);
        closable->lpVtbl->Release(closable);
    }
}

static void partylan_wgc_release_staging(PartyLANWindowCapturer* capturer) {
    if (capturer == NULL) {
        return;
    }
    if (capturer->staging != NULL) {
        capturer->staging->lpVtbl->Release(capturer->staging);
        capturer->staging = NULL;
    }
    capturer->staging_width = 0;
    capturer->staging_height = 0;
    capturer->staging_format = DXGI_FORMAT_UNKNOWN;
}

static void partylan_wgc_cleanup(PartyLANWindowCapturer* capturer) {
    if (capturer == NULL) {
        return;
    }
    if (capturer->session != NULL) {
        partylan_wgc_close_object((IUnknown*)capturer->session);
        capturer->session->lpVtbl->Release(capturer->session);
        capturer->session = NULL;
    }
    if (capturer->frame_pool != NULL) {
        partylan_wgc_close_object((IUnknown*)capturer->frame_pool);
        capturer->frame_pool->lpVtbl->Release(capturer->frame_pool);
        capturer->frame_pool = NULL;
    }
    if (capturer->item != NULL) {
        capturer->item->lpVtbl->Release(capturer->item);
        capturer->item = NULL;
    }
    if (capturer->winrt_device != NULL) {
        capturer->winrt_device->lpVtbl->Release(capturer->winrt_device);
        capturer->winrt_device = NULL;
    }
    partylan_wgc_release_staging(capturer);
    if (capturer->context != NULL) {
        capturer->context->lpVtbl->Release(capturer->context);
        capturer->context = NULL;
    }
    if (capturer->device != NULL) {
        capturer->device->lpVtbl->Release(capturer->device);
        capturer->device = NULL;
    }
    capturer->window = NULL;
    capturer->content_width = 0;
    capturer->content_height = 0;
    capturer->started = 0;
}

static PartyLANWindowCaptureStatus partylan_wgc_window_bounds(
    HWND window,
    uint32_t* width,
    uint32_t* height) {
    RECT bounds;
    LONG window_width;
    LONG window_height;
    if (width == NULL || height == NULL) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR;
    }
    *width = 0;
    *height = 0;
    if (window == NULL || !IsWindow(window)) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
    }
    if (!GetWindowRect(window, &bounds)) {
        return IsWindow(window)
            ? PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE
            : PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
    }
    window_width = bounds.right - bounds.left;
    window_height = bounds.bottom - bounds.top;
    if (window_width <= 0 || window_height <= 0) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE;
    }
    *width = (uint32_t)window_width;
    *height = (uint32_t)window_height;
    return PARTYLAN_WINDOW_CAPTURE_STATUS_OK;
}

static PartyLANWindowCaptureStatus partylan_wgc_prepare_window_bounds(
    HWND window,
    uint32_t timeout_ms,
    uint32_t* width,
    uint32_t* height) {
    WINDOWPLACEMENT placement;
    RECT normal_bounds;
    ULONGLONG deadline;
    uint32_t expected_width;
    uint32_t expected_height;
    uint32_t minimum_width;
    uint32_t minimum_height;
    uint32_t previous_width = 0;
    uint32_t previous_height = 0;
    int stable_samples = 0;

    if (width == NULL || height == NULL) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR;
    }
    *width = 0;
    *height = 0;
    if (window == NULL || !IsWindow(window)) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
    }
    if (!IsIconic(window)) {
        return partylan_wgc_window_bounds(window, width, height);
    }

    memset(&placement, 0, sizeof(placement));
    placement.length = sizeof(placement);
    if (!GetWindowPlacement(window, &placement)) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE;
    }
    normal_bounds = placement.rcNormalPosition;
    if (normal_bounds.right <= normal_bounds.left || normal_bounds.bottom <= normal_bounds.top) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE;
    }
    expected_width = (uint32_t)(normal_bounds.right - normal_bounds.left);
    expected_height = (uint32_t)(normal_bounds.bottom - normal_bounds.top);
    /* A restored window may be resized by DPI or the window manager. Keep a
       generous lower bound so the minimized title-bar shell cannot be
       mistaken for a stable capture size. */
    minimum_width = expected_width >= 128 ? expected_width - expected_width / 4 : expected_width;
    minimum_height = expected_height >= 128 ? expected_height - expected_height / 4 : expected_height;
    ShowWindowAsync(window, SW_RESTORE);
    if (timeout_ms == 0) {
        timeout_ms = 1500;
    }
    deadline = GetTickCount64() + (ULONGLONG)timeout_ms;
    while (GetTickCount64() <= deadline) {
        RECT bounds;
        LONG window_width;
        LONG window_height;
        if (!IsWindow(window)) {
            return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
        }
        if (!IsIconic(window) && GetWindowRect(window, &bounds)) {
            window_width = bounds.right - bounds.left;
            window_height = bounds.bottom - bounds.top;
            if (window_width >= (LONG)minimum_width && window_height >= (LONG)minimum_height) {
                if ((uint32_t)window_width == previous_width && (uint32_t)window_height == previous_height) {
                    stable_samples++;
                } else {
                    previous_width = (uint32_t)window_width;
                    previous_height = (uint32_t)window_height;
                    stable_samples = 1;
                }
                if (stable_samples >= 2) {
                    *width = (uint32_t)window_width;
                    *height = (uint32_t)window_height;
                    return PARTYLAN_WINDOW_CAPTURE_STATUS_OK;
                }
            }
        }
        Sleep(20);
    }
    return IsWindow(window)
        ? PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE
        : PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
}

static HRESULT partylan_wgc_create_device(PartyLANWindowCapturer* capturer) {
    static const D3D_FEATURE_LEVEL feature_levels[] = {
        D3D_FEATURE_LEVEL_11_0,
        D3D_FEATURE_LEVEL_10_1,
        D3D_FEATURE_LEVEL_10_0
    };
    D3D_FEATURE_LEVEL selected_level;
    HRESULT hr = D3D11CreateDevice(
        NULL,
        D3D_DRIVER_TYPE_HARDWARE,
        NULL,
        D3D11_CREATE_DEVICE_BGRA_SUPPORT,
        feature_levels,
        (UINT)(sizeof(feature_levels) / sizeof(feature_levels[0])),
        D3D11_SDK_VERSION,
        &capturer->device,
        &selected_level,
        &capturer->context);
    if (FAILED(hr)) {
        hr = D3D11CreateDevice(
            NULL,
            D3D_DRIVER_TYPE_WARP,
            NULL,
            D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            feature_levels,
            (UINT)(sizeof(feature_levels) / sizeof(feature_levels[0])),
            D3D11_SDK_VERSION,
            &capturer->device,
            &selected_level,
            &capturer->context);
    }
    return hr;
}

static HRESULT partylan_wgc_create_winrt_device(PartyLANWindowCapturer* capturer) {
    IDXGIDevice* dxgi_device = NULL;
    IInspectable* inspectable = NULL;
    HRESULT hr = capturer->device->lpVtbl->QueryInterface(
        capturer->device,
        &IID_IDXGIDevice,
        (void**)&dxgi_device);
    if (FAILED(hr) || dxgi_device == NULL) {
        return FAILED(hr) ? hr : E_NOINTERFACE;
    }
    hr = CreateDirect3D11DeviceFromDXGIDevice(dxgi_device, &inspectable);
    dxgi_device->lpVtbl->Release(dxgi_device);
    if (FAILED(hr) || inspectable == NULL) {
        return FAILED(hr) ? hr : E_NOINTERFACE;
    }
    hr = inspectable->lpVtbl->QueryInterface(
        inspectable,
        &PARTYLAN_IID_WINRT_D3D_DEVICE,
        (void**)&capturer->winrt_device);
    inspectable->lpVtbl->Release(inspectable);
    return hr;
}

static HRESULT partylan_wgc_create_item(PartyLANWindowCapturer* capturer) {
    HSTRING class_name = NULL;
    IGraphicsCaptureItemInterop* interop = NULL;
    HRESULT hr = WindowsCreateString(
        RuntimeClass_Windows_Graphics_Capture_GraphicsCaptureItem,
        (UINT32)wcslen(RuntimeClass_Windows_Graphics_Capture_GraphicsCaptureItem),
        &class_name);
    if (FAILED(hr)) {
        return hr;
    }
    hr = RoGetActivationFactory(
        class_name,
        &PARTYLAN_IID_GRAPHICS_CAPTURE_ITEM_INTEROP,
        (void**)&interop);
    WindowsDeleteString(class_name);
    if (FAILED(hr) || interop == NULL) {
        return FAILED(hr) ? hr : E_NOINTERFACE;
    }
    hr = interop->lpVtbl->CreateForWindow(
        interop,
        capturer->window,
        &PARTYLAN_IID_GRAPHICS_CAPTURE_ITEM,
        (void**)&capturer->item);
    interop->lpVtbl->Release(interop);
    return hr;
}

static HRESULT partylan_wgc_create_frame_pool(PartyLANWindowCapturer* capturer) {
    HSTRING class_name = NULL;
    PartyLANWGCFramePoolStatics* factory = NULL;
    PartyLANSizeInt32 size;
    HRESULT hr;

    size.Width = (INT32)capturer->content_width;
    size.Height = (INT32)capturer->content_height;
    hr = WindowsCreateString(
        RuntimeClass_Windows_Graphics_Capture_Direct3D11CaptureFramePool,
        (UINT32)wcslen(RuntimeClass_Windows_Graphics_Capture_Direct3D11CaptureFramePool),
        &class_name);
    if (FAILED(hr)) {
        return hr;
    }
    hr = RoGetActivationFactory(
        class_name,
        &PARTYLAN_IID_FRAME_POOL_STATICS2,
        (void**)&factory);
    WindowsDeleteString(class_name);
    if (FAILED(hr) || factory == NULL) {
        return FAILED(hr) ? hr : E_NOINTERFACE;
    }
    hr = factory->lpVtbl->CreateFreeThreaded(
        factory,
        capturer->winrt_device,
        DirectXPixelFormat_B8G8R8A8UIntNormalized,
        2,
        size,
        &capturer->frame_pool);
    factory->lpVtbl->Release(factory);
    return hr;
}

static HRESULT partylan_wgc_ensure_staging(
    PartyLANWindowCapturer* capturer,
    const D3D11_TEXTURE2D_DESC* source_desc) {
    D3D11_TEXTURE2D_DESC staging_desc;
    if (capturer->staging != NULL &&
        capturer->staging_width == source_desc->Width &&
        capturer->staging_height == source_desc->Height &&
        capturer->staging_format == source_desc->Format) {
        return S_OK;
    }
    partylan_wgc_release_staging(capturer);
    memset(&staging_desc, 0, sizeof(staging_desc));
    staging_desc.Width = source_desc->Width;
    staging_desc.Height = source_desc->Height;
    staging_desc.MipLevels = 1;
    staging_desc.ArraySize = 1;
    staging_desc.Format = source_desc->Format;
    staging_desc.SampleDesc.Count = 1;
    staging_desc.Usage = D3D11_USAGE_STAGING;
    staging_desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
    HRESULT hr = capturer->device->lpVtbl->CreateTexture2D(
        capturer->device,
        &staging_desc,
        NULL,
        &capturer->staging);
    if (SUCCEEDED(hr)) {
        capturer->staging_width = source_desc->Width;
        capturer->staging_height = source_desc->Height;
        capturer->staging_format = source_desc->Format;
    }
    return hr;
}

static int64_t partylan_wgc_now_us(void) {
    FILETIME value;
    ULARGE_INTEGER ticks;
    const uint64_t windows_to_unix_epoch_100ns = 116444736000000000ULL;
    GetSystemTimeAsFileTime(&value);
    ticks.LowPart = value.dwLowDateTime;
    ticks.HighPart = value.dwHighDateTime;
    if (ticks.QuadPart <= windows_to_unix_epoch_100ns) {
        return 0;
    }
    return (int64_t)((ticks.QuadPart - windows_to_unix_epoch_100ns) / 10ULL);
}

static void partylan_wgc_close_frame(PartyLANWGCFrame* frame) {
    if (frame == NULL) {
        return;
    }
    partylan_wgc_close_object((IUnknown*)frame);
    frame->lpVtbl->Release(frame);
}

void* PartyLAN_CreateWindowCapturer(void) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)calloc(1, sizeof(PartyLANWindowCapturer));
    if (capturer != NULL) {
        partylan_wgc_set_reason(capturer, "Windows Graphics Capture is not started");
    }
    return capturer;
}

PartyLANWindowCaptureStatus PartyLAN_WindowCapturerBounds(
    uint64_t window_handle,
    uint32_t* width,
    uint32_t* height) {
    return partylan_wgc_window_bounds((HWND)(uintptr_t)window_handle, width, height);
}

PartyLANWindowCaptureStatus PartyLAN_WindowCapturerPrepareBounds(
    uint64_t window_handle,
    uint32_t timeout_ms,
    uint32_t* width,
    uint32_t* height) {
    return partylan_wgc_prepare_window_bounds(
        (HWND)(uintptr_t)window_handle,
        timeout_ms,
        width,
        height);
}

PartyLANWindowCaptureStatus PartyLAN_WindowCapturerStart(void* handle, uint64_t window_handle) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)handle;
    PartyLANSizeInt32 content_size;
    int uninitialize_runtime = 0;
    HRESULT hr;
    if (capturer == NULL) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR;
    }
    partylan_wgc_cleanup(capturer);
    capturer->window = (HWND)(uintptr_t)window_handle;
    if (capturer->window == NULL || !IsWindow(capturer->window)) {
        partylan_wgc_set_reason(capturer, "shared window is closed");
        return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
    }
    hr = partylan_wgc_enter_runtime(&uninitialize_runtime);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "RoInitialize", hr);
        return PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE;
    }
    hr = partylan_wgc_create_device(capturer);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "D3D11CreateDevice", hr);
        goto unavailable;
    }
    hr = partylan_wgc_create_winrt_device(capturer);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "CreateDirect3D11DeviceFromDXGIDevice", hr);
        goto unavailable;
    }
    hr = partylan_wgc_create_item(capturer);
    if (FAILED(hr) || capturer->item == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "CreateForWindow", hr);
        goto unavailable;
    }
    memset(&content_size, 0, sizeof(content_size));
    hr = capturer->item->lpVtbl->get_Size(capturer->item, &content_size);
    if (FAILED(hr) || content_size.Width <= 0 || content_size.Height <= 0) {
        partylan_wgc_set_hresult_reason(capturer, "GraphicsCaptureItem.Size", hr);
        goto unavailable;
    }
    capturer->content_width = (UINT)content_size.Width;
    capturer->content_height = (UINT)content_size.Height;
    hr = partylan_wgc_create_frame_pool(capturer);
    if (FAILED(hr) || capturer->frame_pool == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "CreateFreeThreaded", hr);
        goto unavailable;
    }
    hr = capturer->frame_pool->lpVtbl->CreateCaptureSession(
        capturer->frame_pool,
        capturer->item,
        &capturer->session);
    if (FAILED(hr) || capturer->session == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "CreateCaptureSession", hr);
        goto unavailable;
    }
    hr = capturer->session->lpVtbl->StartCapture(capturer->session);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "StartCapture", hr);
        goto unavailable;
    }
    capturer->started = 1;
    partylan_wgc_set_reason(capturer, "Windows Graphics Capture started");
    partylan_wgc_leave_runtime(uninitialize_runtime);
    return PARTYLAN_WINDOW_CAPTURE_STATUS_OK;

unavailable:
    partylan_wgc_cleanup(capturer);
    partylan_wgc_leave_runtime(uninitialize_runtime);
    return IsWindow((HWND)(uintptr_t)window_handle)
        ? PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE
        : PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
}

PartyLANWindowCaptureStatus PartyLAN_WindowCapturerGrabFrame(
    void* handle,
    int timeout_ms,
    PartyLANWindowCaptureFrame* output) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)handle;
    PartyLANWGCFrame* frame = NULL;
    PartyLANWinRTD3DSurface* surface = NULL;
    PartyLANIDirect3DDxgiInterfaceAccess* access = NULL;
    ID3D11Texture2D* texture = NULL;
    D3D11_TEXTURE2D_DESC texture_desc;
    D3D11_MAPPED_SUBRESOURCE mapped;
    PartyLANSizeInt32 content_size;
    DWORD started_at;
    int uninitialize_runtime = 0;
    HRESULT hr;
    UINT width;
    UINT height;
    UINT row_bytes;
    uint64_t total_bytes;
    UINT row;

    if (capturer == NULL || output == NULL) {
        return PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR;
    }
    memset(output, 0, sizeof(*output));
    if (!capturer->started || capturer->frame_pool == NULL) {
        partylan_wgc_set_reason(capturer, "Windows Graphics Capture is not started");
        return PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE;
    }
    if (!IsWindow(capturer->window)) {
        partylan_wgc_set_reason(capturer, "shared window is closed");
        return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
    }
    hr = partylan_wgc_enter_runtime(&uninitialize_runtime);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "RoInitialize", hr);
        return PARTYLAN_WINDOW_CAPTURE_STATUS_UNAVAILABLE;
    }

    started_at = GetTickCount();
    do {
        hr = capturer->frame_pool->lpVtbl->TryGetNextFrame(capturer->frame_pool, &frame);
        if (FAILED(hr)) {
            partylan_wgc_set_hresult_reason(capturer, "TryGetNextFrame", hr);
            partylan_wgc_leave_runtime(uninitialize_runtime);
            return IsWindow(capturer->window)
                ? PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE
                : PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
        }
        if (frame != NULL) {
            break;
        }
        if (!IsWindow(capturer->window)) {
            partylan_wgc_set_reason(capturer, "shared window is closed");
            partylan_wgc_leave_runtime(uninitialize_runtime);
            return PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;
        }
        if (timeout_ms <= 0 || (DWORD)(GetTickCount() - started_at) >= (DWORD)timeout_ms) {
            partylan_wgc_set_reason(capturer, "Windows Graphics Capture frame is temporarily unavailable");
            partylan_wgc_leave_runtime(uninitialize_runtime);
            return PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE;
        }
        Sleep(2);
    } while (frame == NULL);

    memset(&content_size, 0, sizeof(content_size));
    hr = frame->lpVtbl->get_ContentSize(frame, &content_size);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "Direct3D11CaptureFrame.ContentSize", hr);
        goto frame_unavailable;
    }
    hr = frame->lpVtbl->get_Surface(frame, &surface);
    if (FAILED(hr) || surface == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "Direct3D11CaptureFrame.Surface", hr);
        goto frame_unavailable;
    }
    hr = surface->lpVtbl->QueryInterface(
        surface,
        &PARTYLAN_IID_IDIRECT3D_DXGI_INTERFACE_ACCESS,
        (void**)&access);
    if (FAILED(hr) || access == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "IDirect3DDxgiInterfaceAccess", hr);
        goto frame_unavailable;
    }
    hr = access->lpVtbl->GetInterface(access, &IID_ID3D11Texture2D, (void**)&texture);
    if (FAILED(hr) || texture == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "GetInterface(ID3D11Texture2D)", hr);
        goto frame_unavailable;
    }
    memset(&texture_desc, 0, sizeof(texture_desc));
    texture->lpVtbl->GetDesc(texture, &texture_desc);
    hr = partylan_wgc_ensure_staging(capturer, &texture_desc);
    if (FAILED(hr) || capturer->staging == NULL) {
        partylan_wgc_set_hresult_reason(capturer, "CreateTexture2D(staging)", hr);
        goto frame_unavailable;
    }
    capturer->context->lpVtbl->CopyResource(
        capturer->context,
        (ID3D11Resource*)capturer->staging,
        (ID3D11Resource*)texture);
    memset(&mapped, 0, sizeof(mapped));
    hr = capturer->context->lpVtbl->Map(
        capturer->context,
        (ID3D11Resource*)capturer->staging,
        0,
        D3D11_MAP_READ,
        0,
        &mapped);
    if (FAILED(hr)) {
        partylan_wgc_set_hresult_reason(capturer, "Map(staging)", hr);
        goto frame_unavailable;
    }

    width = content_size.Width > 0 ? (UINT)content_size.Width : texture_desc.Width;
    height = content_size.Height > 0 ? (UINT)content_size.Height : texture_desc.Height;
    if (width > texture_desc.Width) {
        width = texture_desc.Width;
    }
    if (height > texture_desc.Height) {
        height = texture_desc.Height;
    }
    row_bytes = width * 4U;
    total_bytes = (uint64_t)row_bytes * (uint64_t)height;
    if (width == 0 || height == 0 || total_bytes == 0 || total_bytes > UINT32_MAX) {
        capturer->context->lpVtbl->Unmap(capturer->context, (ID3D11Resource*)capturer->staging, 0);
        partylan_wgc_set_reason(capturer, "Windows Graphics Capture returned invalid frame bounds");
        goto frame_unavailable;
    }
    output->data = (uint8_t*)malloc((size_t)total_bytes);
    if (output->data == NULL) {
        capturer->context->lpVtbl->Unmap(capturer->context, (ID3D11Resource*)capturer->staging, 0);
        partylan_wgc_set_reason(capturer, "failed to allocate Windows Graphics Capture frame");
        goto internal_error;
    }
    for (row = 0; row < height; row++) {
        memcpy(
            output->data + ((size_t)row * row_bytes),
            (const uint8_t*)mapped.pData + ((size_t)row * mapped.RowPitch),
            row_bytes);
    }
    capturer->context->lpVtbl->Unmap(capturer->context, (ID3D11Resource*)capturer->staging, 0);
    output->data_len = (uint32_t)total_bytes;
    output->width = width;
    output->height = height;
    output->stride = row_bytes;
    output->timestamp_us = partylan_wgc_now_us();

    if ((UINT)content_size.Width != capturer->content_width ||
        (UINT)content_size.Height != capturer->content_height) {
        PartyLANSizeInt32 next_size;
        next_size.Width = content_size.Width;
        next_size.Height = content_size.Height;
        if (next_size.Width > 0 && next_size.Height > 0 &&
            SUCCEEDED(capturer->frame_pool->lpVtbl->Recreate(
                capturer->frame_pool,
                capturer->winrt_device,
                DirectXPixelFormat_B8G8R8A8UIntNormalized,
                2,
                next_size))) {
            capturer->content_width = (UINT)next_size.Width;
            capturer->content_height = (UINT)next_size.Height;
        }
    }

    texture->lpVtbl->Release(texture);
    access->lpVtbl->Release(access);
    surface->lpVtbl->Release(surface);
    partylan_wgc_close_frame(frame);
    partylan_wgc_set_reason(capturer, "Windows Graphics Capture frame acquired");
    partylan_wgc_leave_runtime(uninitialize_runtime);
    return PARTYLAN_WINDOW_CAPTURE_STATUS_OK;

frame_unavailable:
    if (texture != NULL) {
        texture->lpVtbl->Release(texture);
    }
    if (access != NULL) {
        access->lpVtbl->Release(access);
    }
    if (surface != NULL) {
        surface->lpVtbl->Release(surface);
    }
    partylan_wgc_close_frame(frame);
    partylan_wgc_leave_runtime(uninitialize_runtime);
    return IsWindow(capturer->window)
        ? PARTYLAN_WINDOW_CAPTURE_STATUS_FRAME_UNAVAILABLE
        : PARTYLAN_WINDOW_CAPTURE_STATUS_WINDOW_CLOSED;

internal_error:
    if (texture != NULL) {
        texture->lpVtbl->Release(texture);
    }
    if (access != NULL) {
        access->lpVtbl->Release(access);
    }
    if (surface != NULL) {
        surface->lpVtbl->Release(surface);
    }
    partylan_wgc_close_frame(frame);
    partylan_wgc_leave_runtime(uninitialize_runtime);
    return PARTYLAN_WINDOW_CAPTURE_STATUS_INTERNAL_ERROR;
}

void PartyLAN_WindowCapturerReleaseFrame(void* handle, PartyLANWindowCaptureFrame* frame) {
    (void)handle;
    if (frame == NULL) {
        return;
    }
    free(frame->data);
    memset(frame, 0, sizeof(*frame));
}

void PartyLAN_WindowCapturerStop(void* handle) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)handle;
    int uninitialize_runtime = 0;
    if (capturer == NULL) {
        return;
    }
    (void)partylan_wgc_enter_runtime(&uninitialize_runtime);
    partylan_wgc_cleanup(capturer);
    partylan_wgc_set_reason(capturer, "Windows Graphics Capture stopped");
    partylan_wgc_leave_runtime(uninitialize_runtime);
}

void PartyLAN_DestroyWindowCapturer(void* handle) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)handle;
    if (capturer == NULL) {
        return;
    }
    PartyLAN_WindowCapturerStop(handle);
    free(capturer);
}

const char* PartyLAN_GetWindowCaptureReason(void* handle) {
    PartyLANWindowCapturer* capturer = (PartyLANWindowCapturer*)handle;
    if (capturer == NULL) {
        return "Windows Graphics Capture handle is null";
    }
    return capturer->reason;
}
