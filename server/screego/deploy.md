# Screego ICE Recovery Deployment

1. Record `docker inspect screego`, the active image ID, compose file hashes,
   `/var/www/screego/inject.js`, and `/root/screego/frontend/inject.js` hashes.
2. Create `/root/screego/backups/ice-recovery-<UTC>/` and copy those files plus
   the current compose file into it.
3. Build a new immutable image tag, currently
   `gamecast-sfu:ice-recovery-shadow-v1`. Do not replace `gamecast-sfu:v2`.
4. Start a temporary container with the production environment except for a
   non-conflicting HTTP port, then verify `/config`, `/`, and TURN startup.
5. Replace the production container using the recorded parameters. Verify room
   create/join, sharing, viewing, nginx, and the register service.
6. Keep automatic recovery disabled. P1-P3 initially run in shadow mode only.

Every production compose command, including rollback, must load the runtime
environment explicitly:

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego
```

Rollback by restoring the recorded compose file and `gamecast-sfu:v2`, then
restoring both inject.js copies from the same backup directory. Verify that the
public inject.js hash matches the maintenance copy after rollback.
