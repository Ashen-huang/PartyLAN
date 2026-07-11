# Screego Deployment

1. Record `docker inspect screego`, the active image ID, compose file hashes,
   `/var/www/screego/inject.js`, and `/root/screego/frontend/inject.js` hashes.
2. Verify the register service dependency with `python3 -c "import bcrypt"`.
3. Create a timestamped directory below `/root/screego/backups/` and copy those
   files, `/root/screego/register.py`, `/etc/gamecast-sfu/users.htpasswd`, and
   the current compose file into it.
4. Build a new immutable image tag, currently
   `gamecast-sfu:registration-hot-reload-v1`. Do not replace the previous image.
5. Start a temporary container with the production environment except for a
   non-conflicting HTTP port, then verify `/config`, `/`, and TURN startup.
6. Replace the production container using the recorded parameters. Verify room
   create/join, sharing, viewing, nginx, and the register service.
7. Keep automatic recovery disabled. P1-P3 remain in shadow mode only.

Every production compose command, including rollback, must load the runtime
environment explicitly:

```bash
docker compose --env-file /etc/gamecast-sfu/runtime.env up -d --force-recreate screego
```

Rollback by restoring the recorded compose file and previous immutable image,
then restoring the register and inject scripts from the same backup directory.
Restore `users.htpasswd` only if its integrity is damaged; otherwise keep the
latest file so legitimate registrations are not lost. Verify that the public
inject.js hash matches the maintenance copy after rollback.
