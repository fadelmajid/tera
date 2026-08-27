# Running Tera on the Zo computer

Public URL: **https://tera-anahata.zocomputer.io** → `0.0.0.0:8080` in the sandbox.

| | |
|---|---|
| Binary | `bin/tera` (front end embedded) |
| Database | `/home/workspace/Project/tera-data/tera.db` — outside the repo, so `git clean` can't take it |
| Backups | `/home/workspace/Project/tera-data/backups` |
| Process | supervisord program `tera`, logs at `/dev/shm/tera.log` |
| Go | `/opt/go1.27` — the distro Go is 1.19 and `go.mod` needs 1.25+ |

## Scripts

| Script | Does |
|---|---|
| `build.sh` | `npm ci && vite build`, then `go build` with the SPA embedded |
| `start.sh` | What supervisord runs: sets env, execs the binary |
| `reapply.sh` | Puts `[program:tera]` back into `/etc/zo/supervisord-user.conf` |

## After a sandbox restart

`/etc/zo/supervisord-user.conf` is platform-managed and restored from the image
on every restart, which drops the hand-added `[program:tera]` block. The frp
proxy that serves the public URL is written by the platform and survives; the
app behind it does not. Run:

    deploy/zo/reapply.sh

## `TERA_COOKIE_SECURE`

`start.sh` sets it to `1`. The public URL is HTTPS, so the session cookie is
marked `Secure` — which also means **hitting `http://<lan-ip>:8080` directly
will not log you in**. That is the right trade for a service on the public
internet; for a shop-LAN install it must go back to unset (DECISIONS D-008).
