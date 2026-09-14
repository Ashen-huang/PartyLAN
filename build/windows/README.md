# Windows Build Prerequisites

partylan 使用 Wails Windows 打包路径和可选的原生构建标签。

## Required Tooling

Install and verify:

1. Go
2. Node.js and npm
3. Wails CLI
4. Visual Studio Build Tools with MSVC C++ toolchain
5. Windows SDK with D3D11 and DXGI headers
6. NVIDIA driver environment on at least one host machine for hardware-share validation

## Environment Variables

The app currently supports:

- `PARTYLAN_MODE`
  - `loopback`
  - `lan`
  - `tailscale-system`
  - `networked`
- `PARTYLAN_AUTH_KEY`
  - optional auth key passed into network startup

Example:

```powershell
$env:PARTYLAN_MODE = "tailscale-system"
$env:PARTYLAN_AUTH_KEY = ""
```

## Current State

- `tailscale-system` is the default release runtime path and reuses the installed Tailscale client
- `lan` remains available for same-network discovery without Tailscale
- `networked` is retained for embedded `tsnet` diagnostics and future work
- The old local loopback path is intended for tests and diagnostics only

## Native Media Bridge

The Windows native media bridge is present as an opt-in build path.

Enable it with:

```powershell
$env:GOFLAGS = "-tags=partylan_native_media"
```

This path requires the Windows C++ toolchain and enables the native DXGI/NVENC
media bridge used by the release package.

The real `tsnet` network backend is also opt-in:

```powershell
$env:GOFLAGS = "-tags=partylan_tsnet"
```

Without that tag, the default build uses an explicit unavailable networked backend so Wails packaging stays buildable.
