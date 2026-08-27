# Hosting a test box

**This is for trying the software from a phone or sharing it with someone. It
is not the shop deployment and must not become it.**

The shop model is one machine on its own LAN with the printer attached
(ARCHITECTURE §1). A container in a datacentre cannot reach a USB receipt
printer, and INV-11's whole point is that the business keeps trading when the
internet does not. Hosting inverts that: the shop stops when someone else's
network does.

---

## Read this before putting anything real on a public URL

Tera's security model is tuned for a shop LAN, and three things follow from
that. None is a bug; all three matter more on the open internet.

| | |
|---|---|
| **No login rate limiting or lockout** | Nothing throttles repeated login attempts. bcrypt makes each one slow, which is real protection, but there is no lockout. On a LAN the attacker has to be in the building. On a public URL they do not. |
| **The first password is printed to the log** | It is logged once at first run (`pengguna pertama dibuat`). On a hosted platform that log may be readable by anyone with access to the dashboard. Change the password immediately after the first login. |
| **The session cookie is not Secure by default** | Deliberate — see D-008. Behind TLS you **must** set `TERA_COOKIE_SECURE=1`, which `fly.toml` already does. |

**Use dummy data.** Do not put the family's real purchases, margins or customer
records on a free host to try a button.

---

## The easiest option, and the best one: a tunnel

Run it on your own machine, exactly as it will run in the shop, and expose that
one URL. Nothing to deploy, real persistence, and you are testing the actual
deployment model rather than a container that resembles it.

Tailscale is already installed on this machine:

```bash
# 1. Run Tera locally as usual
mkdir -p ~/tera-coba
TERA_DB_PATH=~/tera-coba/tera.db \
TERA_ADDR=0.0.0.0:8080 \
TERA_COOKIE_SECURE=1 \
./bin/tera

# 2. In another terminal, publish port 8080
tailscale funnel 8080
```

It prints an `https://<machine>.<tailnet>.ts.net` URL, with TLS terminated for
you. `tailscale serve 8080` instead of `funnel` keeps it private to your own
devices, which is enough to test from your phone and exposes nothing publicly.

Stop it with `tailscale funnel --https=443 off`.

Cloudflare Tunnel (`cloudflared tunnel --url http://localhost:8080`) does the
same thing with no account and a throwaway URL.

---

## A real hosted box: Fly.io

The repository has a `Dockerfile` and a `fly.toml` ready. Fly fits because it
offers a **persistent volume** and a **single long-running process**, which is
what a SQLite application needs.

```bash
brew install flyctl          # not currently installed
fly auth login

fly launch --no-deploy       # accept the existing fly.toml; pick a unique app name
fly volumes create tera_data --region sin --size 1
fly deploy

fly logs                     # the first-run password is in here
fly open
```

Then **change the admin password immediately**, because it was in those logs.

### The two settings in `fly.toml` that must not change

```toml
min_machines_running = 1
auto_stop_machines   = false
```

A second machine gets its **own volume** — a second database that believes it
is the only one. Two tills, two sets of invoice numbers, two omzet counters,
and no way to merge them afterwards. `store.Open` pins the connection pool to
one connection precisely because SQLite has one writer.

If you ever see `fly scale count 2` suggested anywhere, that is the command
that corrupts this.

### Getting data off it

The container has one disk, so `TERA_BACKUP_ALLOW_SAME_DISK=1` is set and the
scheduled snapshots sit beside the database. That survives a bad migration and
nothing else.

```bash
fly ssh console -C "tera backups --dir /data/cadangan"
fly sftp get /data/cadangan/tera-<timestamp>.db
fly sftp get /data/cadangan/tera-<timestamp>.json     # the checksum
```

Or use the export screen in the app, which gives you a zip of CSVs (R14.3).

---

## Why not Vercel, Netlify, or Cloudflare Pages

They run **serverless functions**: no persistent filesystem, and the process is
created and destroyed per request. SQLite would write to a disk that vanishes,
so every sale would disappear — silently, because each request would see a
fresh empty database and behave perfectly normally.

Making Tera fit that model means replacing SQLite with a network database, and
that is not a deployment change. It contradicts the choices the whole system
rests on: a single file anyone can copy as a backup (R14.2), a single writer
that removes an entire class of concurrency bug, and a shop that keeps trading
with no internet (INV-11).

## Others worth knowing about

| Host | Fit |
|---|---|
| **Render** | Free web services have an **ephemeral disk and spin down when idle** — the database is lost on restart. Persistent disks are a paid add-on. |
| **Railway** | Volumes supported; runs on trial credit rather than a permanent free tier. |
| **Koyeb, Northflank** | Free instances exist; check whether the free plan includes a persistent volume before relying on one. |
| **Zo Computer** | Technically a good fit — real Linux box, persistent disk, services with public HTTPS, and a **private-with-auth mode** that would cover Tera's missing login rate limiting. But the free plan's computer *stops* after 14 days, so hosting starts at $18/mo. See [the research note](research/2026-08-21-zo-computer-hosting.md). |
| **Indonesian VPS** | Rp43–59k/month, billed in rupiah with local payment and Bahasa support. You run the TLS and the patching — `deploy/docker-compose.yml` and `deploy/Caddyfile` handle the first. See [the research note](research/2026-08-21-vps-indonesia.md). |
| **Any small VPS** | Works exactly like the shop machine and costs a few dollars a month. |

Free-tier terms change often. Confirm the persistent-volume question on the
current pricing page before trusting a box with anything you would mind losing.

---

## Running the container locally

Worth doing before deploying anywhere — it is the same image.

```bash
docker build -t tera:test .
docker run -d --name tera-test -p 8080:8080 -v tera-data:/data tera:test
docker logs tera-test          # the first-run password
open http://localhost:8080
```

The image is about 22 MB. The database lives on the named volume, never in an
image layer, so `docker restart` keeps your data and `docker rm` does not
delete it.

```bash
docker rm -f tera-test && docker volume rm tera-data   # start over
```
