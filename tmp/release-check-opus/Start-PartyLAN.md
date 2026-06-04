# PartyLAN Launch Script

Use `scripts/Start-PartyLAN.ps1` for both run and build.

## Run

```powershell
.\scripts\Start-PartyLAN.ps1
```

For packaged builds that should always launch with the current custom DERP
settings, use:

```powershell
.\Start-PartyLAN-DERP.ps1
```

The DERP launcher no longer forces embedded `tsnet` by default, because the
WebRTC native media path currently requires `tailscale-system` or another
browser-routable network path for playback. To force embedded `tsnet` for
non-WebRTC diagnostics:

```powershell
.\Start-PartyLAN-DERP.ps1 -ForceTsnet
```

If you need to override the locally saved auth key for a test run:

```powershell
.\Start-PartyLAN-DERP.ps1 -AuthKey "tskey-auth-..."
```

If you want log mirroring in the terminal while using the packaged build:

```powershell
.\Start-PartyLAN-DERP.ps1 -LogToStdout
```

For a single-machine smoke test that validates the current build can start,
join the tailnet, and see the custom DERP region in the active DERP map:

```powershell
.\Start-PartyLAN-SmokeTest.ps1
```

## Build

```powershell
.\scripts\Start-PartyLAN.ps1 -Action build
```

## Optional tags

- `-UseTsnet` enables the `partylan_tsnet` build tag and switches the app to `networked` mode.
- `-Mode networked` also enables the `partylan_tsnet` build tag automatically.
- `-UseNativeMedia` enables the `partylan_native_media` build tag and requires cgo (`gcc`/MinGW-w64 available in `PATH`).
- `-UseNativeCaptureExperiment` enables experimental `native capture + software encoder` (requires `-UseNativeMedia`).
- `-UseNativeEncoderExperiment` enables experimental native encoder path with automatic fallback to software encoder (requires `-UseNativeMedia`).
- When building with the above experimental flags, the switches are baked into the executable via `ldflags`, so packaged builds keep the same behavior without requiring runtime environment variables.
- Native builds are verified after build: `build/bin/PartyLAN.exe` must contain `CGO_ENABLED=1` and `partylan_native_media` in embedded build metadata.
- `-Installer` adds the NSIS installer when `makensis` is installed.
- `-Mode networked` builds an executable that starts in embedded network mode by default.
- `-DERPRegionID`, `-DERPRegionCode`, `-DERPHostName`, and `-DERPPort` declare the self-hosted DERP region PartyLAN expects to see in the active Tailscale DERP map.

## Custom DERP diagnostics

Self-hosted DERP is configured in the Tailscale tailnet policy `derpMap`. PartyLAN does not replace the Tailscale control plane; it verifies the effective DERP map reported to the client and logs peer path summaries.

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -Mode networked `
  -DERPRegionID 901 `
  -DERPRegionCode "cn1" `
  -DERPHostName "relay.example.com" `
  -DERPPort 443
```

Equivalent runtime environment variables:

```powershell
$env:PARTYLAN_DERP_REGION_ID = "901"
$env:PARTYLAN_DERP_REGION_CODE = "cn1"
$env:PARTYLAN_DERP_HOSTNAME = "relay.example.com"
$env:PARTYLAN_DERP_PORT = "443"
```

Check the log for `[network/derp] custom DERP present` and peer `path=direct` or `path=derp` lines.

## Output

- `build/bin/PartyLAN.exe`

## Release bundle

```powershell
.\scripts\Package-PartyLAN.ps1
```

For native capture/encoder retest builds, use:

```powershell
.\scripts\Package-PartyLAN-NativeTest.ps1
```

If NSIS is installed, the build also produces an installer executable; otherwise it falls back to zip only.
The bundle also includes `SHA256SUMS.txt`.

## Log analysis

```powershell
.\scripts\Analyze-PartyLANLog.ps1 -Path .\build\bin\logs\partylan-20260524.log
```

By default, logs are now written to `logs` under the executable directory (for packaged builds).  
If that folder cannot be created (permissions), it automatically falls back to `%AppData%\PartyLAN\logs`.
