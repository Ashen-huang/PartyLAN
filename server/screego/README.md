# PartyLAN Screego Patch Set

This directory keeps the production Screego changes reproducible while the
working source tree remains under the ignored `tmp/gamecast-sfu-source` path.

## Baseline

`upstream.lock` records the exact files and build inputs that the patch set
expects. `apply-patches.ps1` refuses to apply a patch when a baseline hash does
not match.

The current UI baseline is Screego revision
`f946cee83b61d9f47c2f1c2238fb31f4042b628a`. Its `ui/src/useRoom.ts` hash was
verified against both the local vendor tree and a clean upstream clone. The
PartyLAN repository revision that produced the previous custom binary is also
recorded in `upstream.lock` for rollback provenance.

## Workflow

```powershell
git clone https://github.com/screego/server.git tmp/screego-upstream
git -C tmp/screego-upstream checkout f946cee83b61d9f47c2f1c2238fb31f4042b628a
server/screego/apply-patches.ps1
server/screego/verify-build.ps1
```

Build a new image tag after verification. Never overwrite `gamecast-sfu:v2`.
Production rollout and rollback steps are documented in `deploy.md`.

`deploy/` contains the version-controlled nginx, register service, inject
script, and shadow telemetry assets that are copied to the server.

The native web UI is part of patch `0006-partylan-native-web-ui.patch`. It
replaces the legacy bottom action bar in `ui/src/Room.tsx` with the PartyLAN
side rail while keeping the media and room state contracts in `useRoom.ts`.
Build the UI before the Go binary so `ui/serve.go` embeds the current assets:

```powershell
Push-Location tmp/screego-upstream/ui
npm install --frozen-lockfile
npm run build
Pop-Location
server/screego/verify-build.ps1
```

Deploy the resulting Linux binary with a new immutable image tag. The native
UI image used on the Guangzhou canary is
`gamecast-sfu:native-web-ui-v1-20260824`; the previous
`gamecast-sfu:receiver-adaptive-v1-20260815` image remains available for
rollback.
