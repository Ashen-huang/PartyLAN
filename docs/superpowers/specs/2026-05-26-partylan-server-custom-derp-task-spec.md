# PartyLAN Server Task Spec: Self-Hosted DERP Relay

## Purpose

Deploy a self-hosted DERP relay on the Ubuntu 22.04 public server so PartyLAN keeps the original Tailscale/tsnet networking model while avoiding silent dependence on public Tailscale DERP servers when direct NAT traversal fails.

The server must act only as a Tailscale DERP relay and STUN endpoint. It does not terminate PartyLAN application traffic, does not decrypt WireGuard payloads, and does not replace the Tailscale control plane.

## Server Profile

- OS: Ubuntu 22.04 x64
- CPU: 4 cores
- Memory: 4 GB
- Public bandwidth: 20 Mbps
- Monthly traffic: 100 GB
- Data disk: none

Capacity target for first rollout:

- One active PartyLAN screen-share session at 720p30 H.264/NVENC target bitrate.
- Prefer 2-4 Mbps video while validating.
- Avoid multi-viewer or 1080p/high-FPS expectations until traffic and bitrate measurements prove headroom.

Approximate monthly relay budget:

- 2 Mbps continuous relay: about 0.9 GB/hour, about 110 hours per 100 GB.
- 4 Mbps continuous relay: about 1.8 GB/hour, about 55 hours per 100 GB.
- 8 Mbps continuous relay: about 3.6 GB/hour, about 27 hours per 100 GB.

## Required Inputs

Fill these before execution:

- Public server IPv4: `<SERVER_IPV4>`
- Optional IPv6: `<SERVER_IPV6>`
- DNS name for DERP: `<DERP_HOSTNAME>`, for example `derp-cn1.example.com`
- Region ID: `901`
- Region code: `cn1`
- Region name: `PartyLAN CN Relay`
- DERP TLS port: `443`
- STUN UDP port: `3478`
- Admin email for TLS/ACME if using automatic certificates: `<ADMIN_EMAIL>`

Use region IDs in the 900-999 range for user-owned/custom DERP regions.

## Non-Goals

- Do not build a custom PartyLAN relay protocol.
- Do not deploy a separate room/signaling server.
- Do not store or inspect video, audio, or room payloads on the server.
- Do not disable public Tailscale DERP globally during first rollout.
- Do not tune PartyLAN bitrate in this task; client-side bitrate adaptation is a separate task.

## Architecture

```text
PartyLAN Client A
  |
  | Tailscale/tsnet WireGuard traffic
  v
Tailscale direct path if available
  |
  | fallback when direct path fails
  v
Self-hosted DERP on Ubuntu
  |
  v
PartyLAN Client B
```

The Tailscale coordination server distributes the active DERP map to clients through the tailnet policy `derpMap`. PartyLAN clients verify that region `901/cn1` appears in the active map and log whether peer paths are `direct`, `derp`, or `peer-relay`.

## Server Tasks

1. Prepare DNS
   - Create an A record for `<DERP_HOSTNAME>` pointing to `<SERVER_IPV4>`.
   - Create an AAAA record if `<SERVER_IPV6>` is available.
   - Confirm public DNS resolves from outside the server network.

2. Prepare firewall
   - Allow inbound TCP `443` to the DERP HTTPS endpoint.
   - Allow inbound UDP `3478` for STUN.
   - Allow inbound SSH only from trusted admin IPs where possible.
   - If using ACME HTTP validation or a reverse proxy that needs HTTP, allow TCP `80`; otherwise keep it closed.

3. Install runtime dependencies
   - Install Go or use a pinned prebuilt `derper` binary from a controlled build process.
   - Install `systemd` service unit support, log rotation, and basic monitoring tools.
   - Keep the server minimal: no database, no PartyLAN app service, no public web panel.

4. Build and install `cmd/derper`
   - Build from a pinned Tailscale version compatible with the PartyLAN client dependency family.
   - Install the binary at `/usr/local/bin/derper`.
   - Record the exact Tailscale source version, Go version, binary hash, and build date.

5. Configure TLS
   - Preferred: valid public TLS certificate for `<DERP_HOSTNAME>`.
   - Certificate renewal must be automatic.
   - The DERP service must restart or reload cleanly after certificate renewal.
   - If using a reverse proxy, confirm DERP protocol upgrades/websocket behavior is preserved.

6. Create the `derper` systemd service
   - Run as a dedicated unprivileged user such as `derper`.
   - Restart on failure.
   - Start after network is online.
   - Bind HTTPS on TCP `443`.
   - Serve STUN on UDP `3478`.
   - Log to journald.

7. Add tailnet policy `derpMap`
   - Add a custom DERP region for `901/cn1`.
   - Keep default Tailscale DERP regions enabled for first rollout fallback.
   - Do not set omit-default-regions during first validation.

Example policy shape:

```jsonc
{
  "derpMap": {
    "regions": {
      "901": {
        "regionID": 901,
        "regionCode": "cn1",
        "regionName": "PartyLAN CN Relay",
        "nodes": [
          {
            "name": "901a",
            "regionID": 901,
            "hostName": "<DERP_HOSTNAME>",
            "derpPort": 443,
            "stunPort": 3478
          }
        ]
      }
    }
  }
}
```

If the Tailscale policy editor rejects casing, adapt keys to the casing expected by the current policy UI while preserving the same semantic fields.

8. Validate DERP availability
   - From at least one Windows client, run `tailscale netcheck`.
   - Confirm custom DERP region `901/cn1` appears.
   - Run `tailscale status` and `tailscale ping <peer>` between two clients.
   - Confirm whether the peer path is direct or uses the custom DERP region.

9. Validate with PartyLAN client
   - Start PartyLAN with:

```powershell
.\scripts\Start-PartyLAN.ps1 `
  -Mode networked `
  -DERPRegionID 901 `
  -DERPRegionCode "cn1" `
  -DERPHostName "<DERP_HOSTNAME>" `
  -DERPPort 443 `
  -UseNativeMedia `
  -UseNativeCaptureExperiment `
  -UseNativeEncoderExperiment
```

   - Confirm logs contain `[network/derp] custom DERP present`.
   - Confirm logs include peer path lines such as `path=direct` or `path=derp relay=cn1`.
   - If logs show `custom DERP missing`, treat the tailnet policy or DNS/TLS deployment as failed.

10. Monitor initial rollout
    - Track server bandwidth, CPU, memory, and process restarts.
    - Track monthly outbound traffic against the 100 GB quota.
    - Capture PartyLAN stream logs for FPS, bitrate, and decode stats.
    - If the server nears 70% sustained bandwidth, lower PartyLAN target bitrate/resolution before adding viewers.

## Acceptance Criteria

- `<DERP_HOSTNAME>` resolves publicly to the server.
- TCP `443` and UDP `3478` are reachable from an external network.
- `derper` runs under systemd and restarts automatically on failure.
- Tailscale clients receive a DERP map containing region `901/cn1`.
- PartyLAN logs show `custom DERP present` when started with matching DERP parameters.
- In a non-LAN test where direct path fails, `tailscale ping <peer>` or PartyLAN logs show traffic using the custom DERP region.
- A 720p30 H.264/NVENC PartyLAN test remains usable within the server's 20 Mbps cap.

## Rollback

- Remove or disable region `901` from the tailnet policy `derpMap`.
- Restart or reconnect clients so they receive the updated DERP map.
- Stop and disable the `derper` systemd service.
- Keep public DERP fallback enabled until the custom DERP path has been stable across multiple sessions.

## Risks

- 20 Mbps is enough for early validation but not enough for many simultaneous high-quality streams.
- 100 GB/month can be consumed quickly by relayed video.
- DERP improves fallback locality but does not solve all hard NAT, bandwidth, or congestion problems.
- Custom DERP requires ongoing patching and monitoring.
- If DNS, TLS, or tailnet policy is wrong, clients may silently continue using public DERP unless PartyLAN diagnostics are checked.

## References

- Tailscale DERP servers: https://tailscale.com/docs/reference/derp-servers
- Tailscale policy file `derpMap`: https://tailscale.com/docs/reference/syntax/policy-file#derpmap
- Tailscale DERP routing troubleshooting: https://tailscale.com/docs/reference/troubleshooting/network-configuration/derp-routing
- Tailscale firewall ports: https://tailscale.com/docs/reference/faq/firewall-ports
