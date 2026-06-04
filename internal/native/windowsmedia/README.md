# Windows Native Media Bridge

This directory contains the Phase 1 Windows native media bridge.

Responsibilities:

- D3D11 device setup
- DXGI Desktop Duplication capture
- NVENC H.264 host encoding
- Viewer-side decode helpers (pending)
- C ABI exports consumed from Go through CGO

## Current State

Implemented:

- Capability query with DXGI adapter + NVENC runtime detection
- Capturer lifecycle APIs
- DXGI Desktop Duplication frame readback to BGRA buffers
- NVENC API header integration (`third_party/nvcodec/nvEncodeAPI.h`)
- NVENC API instance creation during encoder configure

Pending:

- NVENC encode path
- Native decode path
