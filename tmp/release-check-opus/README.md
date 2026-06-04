# PartyLAN

Desktop app built with Wails + Go + React.

## Run

```powershell
.\scripts\Start-PartyLAN.ps1
```

## Build

```powershell
.\scripts\Start-PartyLAN.ps1 -Action build
```

## Output

The packaged app is written to `build/bin/PartyLAN.exe`.

## Release Bundle

```powershell
.\scripts\Package-PartyLAN.ps1
```

This writes a zip release under `release/`.

Pushes to `main` or `master` also upload the same zip from GitHub Actions.
Tag pushes like `v0.4.8` publish a GitHub Release too.

If NSIS is installed, the same command also emits `PartyLAN-v<version>-win64-installer.exe`.
The release bundle also includes `SHA256SUMS.txt` for integrity checks and `RELEASE-INFO.txt` for a concise version + launcher summary.
Packaged releases now also include `Start-PartyLAN-DERP.ps1`, which launches the
bundled `PartyLAN.exe` with the current custom DERP runtime environment
preconfigured.
For a quick local-only sanity check, use `Start-PartyLAN-SmokeTest.ps1`.

## Optional networked mode

```powershell
.\scripts\Start-PartyLAN.ps1 -Mode networked -UseTsnet
```

`-Mode networked` auto-enables the `partylan_tsnet` build tag.
Packages built with `-Mode networked` now select backend automatically:

1. `tailscale-system` (reuse installed/sign-in Tailscale client)
2. `tsnet` fallback (embedded mode)

Optional override:

```powershell
$env:PARTYLAN_NETWORKED_BACKEND = "tailscale-system" # or "tsnet"
.\scripts\Start-PartyLAN.ps1 -Mode networked
```

## Custom DERP diagnostics

PartyLAN stays on the Tailscale/tsnet path for identity, WireGuard encryption, NAT traversal, and virtual IPs. Self-hosted DERP should be published through your tailnet policy `derpMap`; the client verifies that the expected region is visible and logs whether peers are direct or relayed.

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -Mode networked `
  -DERPRegionID 901 `
  -DERPRegionCode "cn1" `
  -DERPHostName "relay.example.com" `
  -DERPPort 443
```

The same values can be provided with environment variables:

```powershell
$env:PARTYLAN_DERP_REGION_ID = "901"
$env:PARTYLAN_DERP_REGION_CODE = "cn1"
$env:PARTYLAN_DERP_HOSTNAME = "relay.example.com"
$env:PARTYLAN_DERP_PORT = "443"
```

Look for `[network/derp]` lines in the diagnostics log, including `custom DERP present`, `custom DERP missing`, and peer `path=direct` / `path=derp` summaries.

## WebRTC media diagnostics

The WebRTC media path requires a native H.264 build. It does not fall back to
the legacy software stream. Optional ICE/TURN servers can be provided with:

```powershell
$env:PARTYLAN_ICE_SERVERS = "stun:stun.example.com:3478,turn:turn.example.com:3478"
$env:PARTYLAN_TURN_USERNAME = "user"
$env:PARTYLAN_TURN_CREDENTIAL = "secret"
```

Shared application audio uses Windows process loopback capture and an Opus
WebRTC audio track. Native release bundles include `opus.dll` next to
`PartyLAN.exe`; local native builds use the bundled runtime under
`third_party/opus/win64`. If the DLL is missing, screen sharing continues as
video-only and the log reports the audio reason.

## Prerequisites

- Go
- Node.js and npm
- Wails CLI
- Windows build tools for native packaging
- NSIS for installer builds

## Diagnostics Log

PartyLAN now writes runtime diagnostics logs to:

- `%APPDATA%\\PartyLAN\\logs\\partylan-YYYYMMDD.log` (Windows default)
- fallback: `%TEMP%\\PartyLAN\\logs\\partylan-YYYYMMDD.log`

The log includes stream sender and receiver runtime metrics (`actual fps`, `bitrate`, `capture ms`, `encode ms`) and decode error counters.

By default logs are written to file only.  
If you also want stdout mirroring (development), set:

```powershell
$env:PARTYLAN_LOG_STDOUT = "1"
```
