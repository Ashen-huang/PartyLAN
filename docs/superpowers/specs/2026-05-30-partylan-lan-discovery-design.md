# PartyLAN LAN Discovery Design

## Goal

Add an explicit local-network mode so two PartyLAN clients on the same LAN can
discover and join each other without depending on system Tailscale, embedded
tsnet, DERP, or a central server.

## Scope

- New runtime mode: `lan`.
- UDP broadcast discovery for PartyLAN clients on the same subnet.
- Reuse the existing room probe on TCP `9002`, room control on TCP `9000`, and
  WebRTC signaling through the existing room protocol.
- Keep WebRTC media unchanged. Once room signaling works, browser playback can
  use LAN-routable candidates.

## Non-Goals

- No internet relay, TURN, or SFU behavior in LAN mode.
- No silent fallback from Tailscale to LAN. Users choose `-Mode lan`.
- No cross-subnet service discovery beyond normal IPv4 broadcast reachability.

## Architecture

`internal/network/LANNetwork` implements the existing `network.Network`
interface.

- `Start` opens UDP `9003` and sends periodic JSON beacons.
- `readLoop` records remote beacons as `NodeInfo` entries.
- `ListNodes` prunes stale peers and probes each peer's TCP `9002` room probe.
- `Dial` connects directly to the peer LAN IP.
- `Listen` binds room ports to `0.0.0.0`.

The frontend and scripts gain a `lan` label/mode, but room, media signaling, and
WebRTC manager code do not need a separate server-side path.

## Operational Notes

Windows Firewall must allow inbound UDP `9003` and TCP `9000`/`9002` on private
networks. TCP `9001` remains documented for the legacy video receiver path even
though the WebRTC media path does not rely on it.

## Validation

- Unit tests cover LAN beacon ingestion, self-beacon filtering, stale peer
  pruning, and direct IP dialing.
- Existing appmode and network tests cover mode parsing and factory wiring.
- Frontend build verifies the new labels and diagnostics rendering.
