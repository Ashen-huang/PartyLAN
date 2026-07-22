# PartyLAN Launch Script

Use `scripts/Start-PartyLAN.ps1` for both run and build.

## Run

```powershell
.\scripts\Start-PartyLAN.ps1
```

The default run mode is `gamecast-client`, so the app opens the native
GameCast/Screego viewer shell and uses the configured GameCast server instead
of the legacy PartyLAN P2P network.

When `gamecast-client` starts on Windows and a supported MinGW-w64 `gcc`
toolchain is available, the launcher now auto-enables `-UseNativeMedia` so
native GameCast host sharing does not silently fall back to the stub backend.
To opt out explicitly for diagnostics, pass:

```powershell
.\scripts\Start-PartyLAN.ps1 -UseNativeMedia:$false
```

To run the old Tailscale-backed PartyLAN path explicitly:

```powershell
.\scripts\Start-PartyLAN.ps1 -Mode tailscale-system
```

For same-LAN discovery without Tailscale:

```powershell
.\scripts\Start-PartyLAN.ps1 -Mode lan
```

LAN mode uses UDP `9003` for PartyLAN discovery, TCP `9002` for room probes,
and TCP `9000` for room control. Allow PartyLAN through Windows Firewall on
private networks when prompted.

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

For a single-machine smoke test that validates the default GameCast client can
start and initialize native media:

```powershell
.\Start-PartyLAN-SmokeTest.ps1
```

The smoke test also accepts `-Mode`, `-TSControlURL`, `-ICEServers`,
`-TURNUsername`, and `-TURNCredential`. Use `-Mode networked` only with a build
that includes the `partylan_tsnet` tag when checking the custom DERP path.

## Build

```powershell
.\scripts\Start-PartyLAN.ps1 -Action build
```

## Optional tags

- `-Mode lan` builds/runs with direct local-network discovery and no Tailscale dependency.
- `-UseTsnet` enables the `partylan_tsnet` build tag and switches the app to `networked` mode.
- `-Mode networked` also enables the `partylan_tsnet` build tag automatically.
- `-UseNativeMedia` enables the `partylan_native_media` build tag and requires cgo (`gcc`/MinGW-w64 available in `PATH`).
- `-UseNativeCaptureExperiment` enables experimental `native capture + software encoder` (requires `-UseNativeMedia`).
- `-UseNativeEncoderExperiment` enables experimental native encoder path with automatic fallback to software encoder (requires `-UseNativeMedia`).
- When building with the above experimental flags, the switches are baked into the executable via `ldflags`, so packaged builds keep the same behavior without requiring runtime environment variables.
- Native builds are verified after build: `build/bin/PartyLAN.exe` must contain `CGO_ENABLED=1` and `partylan_native_media` in embedded build metadata.
- `-Installer` adds the NSIS installer when `makensis` is installed.
- `-Mode tailscale-system` builds/runs against the installed Tailscale client and is the default release path.
- `-Mode networked` builds an executable that starts in embedded network mode by default.
- `-DERPRegionID`, `-DERPRegionCode`, `-DERPHostName`, and `-DERPPort` declare the self-hosted DERP region PartyLAN expects to see in the active Tailscale DERP map.
- `-TSControlURL` preconfigures an embedded `tsnet` control-plane override for second-phase self-hosted control testing.
- `-ICEServers`, `-TURNUsername`, and `-TURNCredential` inject WebRTC ICE / TURN settings for media relay diagnostics and fallback.

## Custom DERP diagnostics

Self-hosted DERP is configured in the Tailscale tailnet policy `derpMap`. PartyLAN does not replace the Tailscale control plane; it verifies the effective DERP map reported to the client and logs peer path summaries.

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -Mode tailscale-system `
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

## WebRTC media relay diagnostics

Optional ICE / TURN settings can be injected from the launch scripts:

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -ICEServers "stun:stun.example.com:3478,turn:turn.example.com:3478" `
  -TURNUsername "user" `
  -TURNCredential "secret"
```

Equivalent runtime environment variables:

```powershell
$env:PARTYLAN_ICE_SERVERS = "stun:stun.example.com:3478,turn:turn.example.com:3478"
$env:PARTYLAN_TURN_USERNAME = "user"
$env:PARTYLAN_TURN_CREDENTIAL = "secret"
```

Check the log for `[media/ice]` lines, including:

- ICE server configuration status
- per-peer ICE state changes
- selected candidate path such as `host`, `srflx`, or `relay`

## Output

- `build/bin/PartyLAN.exe`

## Release bundle

```powershell
.\scripts\Package-PartyLAN.ps1
```

Release bundles default to `-Mode gamecast-client` with native WebRTC media
enabled. For non-native diagnostic builds, pass explicit overrides such as:

```powershell
.\scripts\Package-PartyLAN.ps1 -Mode loopback -UseNativeMedia:$false
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
