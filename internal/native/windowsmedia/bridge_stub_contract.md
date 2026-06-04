# Bridge ABI Contract

This file documents the ABI boundary between Go and the Windows native media bridge.

## Export Contract

The native bridge exposes:

- capability query
- capturer creation and destruction
- capturer start/grab/release/stop
- encoder creation and destruction
- encoder configure/encode/release/close
- decoder creation and destruction

Current implementation targets:

- Windows
- D3D11
- DXGI Desktop Duplication
- NVENC runtime detection

## Current Workspace Status

- The bridge is compiled and callable behind `partylan_native_media`.
- `QueryCapability` performs DXGI adapter and NVENC runtime detection.
- Capturer APIs are implemented with DXGI Desktop Duplication frame readback.
- Native encoder/decoder are still pending.
