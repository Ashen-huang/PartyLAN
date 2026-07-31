# Screego Deployment

This repository defines two independent media services. The existing screen
SFU continues to use its current `5053` protocol. Room voice uses the separate
`partylan-voice-sfu:v1-20260731` image, `127.0.0.1:5054/TCP` for health and
WebSocket signaling, and `5054/UDP` for ICE media.

1. Record `docker inspect screego`, the active image ID, compose file hashes,
   `/var/www/screego/inject.js`, and `/root/screego/frontend/inject.js` hashes.
2. Verify the register service dependency with `python3 -c "import bcrypt"`.
3. Create a timestamped directory below `/root/screego/backups/` and copy those
   files, `/root/screego/register.py`, `/etc/gamecast-sfu/users.htpasswd`, and
   the current compose file into it.
4. Build a new immutable image tag, currently
   `gamecast-sfu:turn-recovery-v1-20260720`. Do not replace the previous image.
5. Start a temporary container with the production environment except for a
   non-conflicting HTTP port, then verify `/config`, `/`, and TURN startup.
6. Replace the production container using the recorded parameters. Verify room
   create/join, sharing, viewing, nginx, and the register service.
7. Automatic recovery is limited to one authenticated per-session ICE restart.
   Confirmed degraded TURN sessions are protected at 720p/3 Mbps first; a
   failed or repeatedly degraded session remains protected without rebuilding
   the peer connection.

Before a voice rollout, build the immutable image from the repository root:

```bash
docker build -f server/voice-sfu/Dockerfile -t partylan-voice-sfu:v1-20260731 .
```

Create `/etc/gamecast-sfu/runtime.env` from the variable names in
`deploy/runtime.env.example`. Generate `GAMECAST_VOICE_SECRET` outside the
repository and supply the same value to the Registration Control service and
the `voice-sfu` container. Do not put the value in compose, systemd unit files,
shell history, logs, or committed examples. Set `VOICE_SFU_PUBLIC_IP` to the
published GameCast address and configure `VOICE_SFU_ICE_SERVERS_JSON` only when
STUN/TURN fallback is required.

Before enabling clients, validate the compose model, confirm that
`http://127.0.0.1:5054/health` exposes only version and aggregate counts, open
`5054/UDP` in the host firewall, and test `/api/voice/token` and `/voice-ws`
through TLS. Nginx and service logs must never include Authorization headers,
request bodies, tokens, raw signaling, SDP, ICE candidates, or media payloads.

The Python control service on port 5052 accepts an optional
`GAMECAST_SAFE_VIDEO_EGRESS_KBPS` environment variable. Set it on that
service's systemd unit (or its environment file), not on the Screego container.
Use the safe aggregate video egress budget in Kbps; leave it unset or `0` to
disable proactive per-viewer budget division.

Every production compose command, including rollback, must load the runtime
environment explicitly:

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego voice-sfu
```

Rollback by restoring the recorded compose file and previous immutable image,
then restoring the register and inject scripts from the same backup directory.
Restore `users.htpasswd` only if its integrity is damaged; otherwise keep the
latest file so legitimate registrations are not lost. Verify that the public
inject.js hash matches the maintenance copy after rollback.

For a voice-only rollback, disable `/voice-ws` and `/api/voice`, stop the
`voice-sfu` service, and remove the voice secret from Registration Control.
Do not restart or alter the screen SFU on `5053`.
