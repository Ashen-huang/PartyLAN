# PartyLAN Client Custom DERP Design

## Goal

Advance the client toward the original PartyLAN networking plan: keep the Tailscale/tsnet ecosystem for identity, WireGuard encryption, NAT traversal, virtual IPs, and peer discovery, while preparing PartyLAN to use a self-hosted DERP relay instead of relying silently on public Tailscale DERP servers when direct paths fail.

The self-hosted DERP map is distributed by the Tailscale control plane through the tailnet policy `derpMap`. The PartyLAN client does not inject a DERP map into `tsnet.Server`; in the current Tailscale dependency, that is not a public `tsnet.Server` field. The client verifies that the expected DERP region is visible in the active DERP map and reports whether peer traffic is direct, DERP-relayed, or using peer relay.

## Client Changes

- Add optional custom DERP expectation settings:
  - region ID
  - region code
  - hostname
  - DERP TLS port
- Allow these settings from saved config and environment variables.
- For `networked` / tsnet mode, query the local tsnet client for:
  - current backend state
  - current DERP map
  - peer path status
- For `tailscale-system` mode, query system Tailscale status and local API where available.
- Surface diagnostics through `RuntimeInfo.Health`, logs, and the existing runtime status UI.

## Data Flow

1. App startup loads config.
2. App passes custom DERP expectations into the network package.
3. Network startup proceeds as today.
4. During status refresh, the network backend checks the current DERP map.
5. If a custom DERP expectation is configured, diagnostics report `present` or `missing`.
6. Peer status reports direct address, DERP relay region, or peer relay address.

## Error Handling

- Missing custom DERP settings are not errors.
- If custom DERP is configured but absent from the active DERP map, PartyLAN reports a warning in runtime health and logs.
- If LocalAPI diagnostics fail, PartyLAN falls back to the existing status path and reports the diagnostic failure as a health line.
- Client startup must not fail solely because diagnostics cannot be collected.

## Testing

- Unit test custom DERP expectation matching against DERP maps.
- Unit test peer path summaries.
- Run the existing Go test matrix after implementation.

## Out Of Scope

- Deploying the Ubuntu DERP server.
- Editing the tailnet policy automatically.
- Replacing Tailscale/tsnet with a custom relay protocol.
- Guaranteeing that Tailscale chooses the custom DERP; the client can verify and report the effective DERP map and peer path, but final selection is controlled by Tailscale.
