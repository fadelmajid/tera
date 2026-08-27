# Can Zo Computer host Tera, database included?

**Researched 2026-08-21.** Pricing and free-tier terms on this product move;
treat the figures as a snapshot and re-check the pricing page before acting.

## TL;DR

**Technically: yes, and it is a genuinely good fit — better than Fly in one
respect.** Zo is a real Linux server with a persistent 100 GB disk, long-running
"Services" that auto-restart, and public HTTPS that proxies to a local port. A
single Go binary writing to a SQLite file is exactly the shape it hosts.

**As a free host: no.** The free plan's computer *stops* after the 14-day trial.
Files survive, but Sites and Services do not run, so the URL is dead. Hosting
starts at **$18/month** (Basic).

**Recommendation: don't buy Zo for this.** A Tailscale funnel costs nothing and
tests the real deployment model; Fly.io costs a few dollars a month and the
`Dockerfile`/`fly.toml` are already written. **But if you already pay for Zo,
use it** — it is a better test host than Fly for this app, for one specific
reason (see *The one thing Zo does better*).

---

## What Zo actually is

A "personal AI cloud computer": a real Linux server with 100 GB of storage, an
agent that can read and write files and run commands, and hosting built in. Not
a serverless platform — which is the thing that disqualified Vercel.

## Feasibility against Tera's requirements

| Tera needs | Zo | Verdict |
|---|---|---|
| Long-running process, not functions | **Services** — "a long-running program on your Zo… a custom web server… or any process you want kept running in the background" | ✅ |
| Survives restarts | "When you register a service, Zo brings it back up automatically with the same config every time your Zo starts" | ✅ |
| Persistent writable filesystem | 100 GB disk, persists across restarts and even across downgrade | ✅ |
| Inbound public HTTPS on a stable URL | HTTP-mode services are "Public by default at `*.zocomputer.io`", proxying to a local port | ✅ |
| Port binding | Service declares a local port; "Zo injects this as the `PORT` env var" | ✅ |
| Arbitrary compiled binary | Services take an entrypoint command (`bun run start`, `python3 app.py`). A binary should be `./tera` — **not explicitly documented** | ⚠️ unverified |
| ~512 MB RAM, one instance | Basic gives 4 cores / 32 GB RAM | ✅ (wildly over) |
| Custom domain | 3 on Basic, 5 on Pro | ✅ |

## Pricing (2026-08-21)

| Plan | Price | CPU / RAM | Hosted services | Uptime |
|---|---|---|---|---|
| Free | $0 | limited, trial only | 1, **trial only** | 14-day trial, then **the computer stops** |
| Basic | $18/mo | 4 cores / 32 GB | 5 | Always on |
| Pro | $64/mo | 16 cores / 128 GB | 10 | Always on |
| Ultra | $200/mo | 64 cores / 512 GB | 50 | Always on |

### The free tier does not host anything

Marketing copy and at least one review describe the free plan as letting you
"host one project for free with no expiry". The billing documentation is
explicit and says otherwise:

> "Zo does not start or renew the ordinary Free computer."
> "Sites and Services are unavailable while the computer is stopped, but your
> files remain stored and retrievable."

After downgrading you get "up to seven one-hour recovery sessions to download
workspace data". So the free plan is a storage locker with an AI chat attached,
not a host. **Where the review and the billing page disagree, believe the
billing page.**

Even during the trial, the computer "sleeps when idle" and hosted services are
"not reachable while your computer is asleep" — so a shared test URL would be
dead most of the time.

## The one thing Zo does better than Fly

**Private services with authentication.** HTTP services are public by default at
`*.zocomputer.io` with "no built-in auth", but can be made **private at
`*.zo.computer`, requiring authentication**.

That matters here more than it would for most apps. Tera has **no login rate
limiting and no lockout** — deliberate, because its threat model is a shop LAN
(D-008). Putting it on a public URL removes that assumption. Zo's private mode
puts an authenticated proxy in front of the login page, which closes the gap
without changing Tera's code. Fly gives you a public URL and nothing in front
of it.

## Risks and unknowns

**Periodic restarts, even on paid plans.** The services docs say plainly: "your
Zo restarts periodically to pick up updates and snapshots, and that happens even
on paid plans." Mostly fine — Tera shuts down gracefully and SQLite WAL is
crash-safe — but `store.Open` uses `synchronous=NORMAL`, which accepts a
power-loss window on the assumption that an hourly backup covers it. A clean
process stop flushes; a hard machine kill during a write is the case that
window is about. Mitigation is already in place (hourly verified backups), and
`synchronous=FULL` is a one-line change if it ever bites.

**Snapshots of a live SQLite file.** Zo snapshots the machine. A filesystem-level
snapshot taken while the WAL is active can capture the main file and the WAL
inconsistently. This is their recovery mechanism, not ours — Tera's own
`VACUUM INTO` backups are consistent by construction, so do not rely on Zo's
snapshots as the backup story. Pull real backups off the box.

**CPU architecture is undocumented.** x86_64 vs arm64 was not stated anywhere I
could find. Irrelevant in practice: CGO is off, so `GOOS=linux GOARCH=amd64` and
`GOARCH=arm64` both cross-compile in seconds. Check with `uname -m` on the box
and build for that.

**Whether an arbitrary uploaded binary runs.** Every documented example is an
interpreted runtime. Nothing suggests it is blocked, and a service entrypoint is
a shell command, but I could not confirm it. **This is the one thing to test
first**, and it takes a minute.

**An AI agent has read/write access to the workspace by design.** Zo states it
does not train on user data or sell it. Still worth naming: this app holds a
family's purchase costs, margins and customer records. Fine for dummy data;
a considered decision for real data.

## Comparison

| | Tailscale funnel | Fly.io | Zo Computer |
|---|---|---|---|
| Cost | Free | ~$2–3/mo | $18/mo |
| Setup | 2 commands | `fly deploy`, artefacts written | Upload binary, register a service |
| Persistent disk | Your own machine | Volume | 100 GB |
| Public HTTPS | ✅ | ✅ | ✅ |
| Auth in front of the app | Tailnet-only with `serve` | ❌ | ✅ private mode |
| Tests the real model | ✅ exactly | ➖ | ➖ |
| Over-provisioned | — | No | 32 GB RAM for a 512 MB app |

## Recommendation

1. **Tailscale funnel** for the smoke test. Free, no deploy, and it exercises
   the deployment model the shop will actually use.
2. **Fly.io** if you want a box that stays up without your laptop. A few dollars
   a month; `Dockerfile` and `fly.toml` are in the repo and verified.
3. **Zo** only if you already subscribe. Then it is a good host, the private-auth
   mode is a real advantage, and setup is roughly: build a Linux binary, upload
   it, register an HTTP service with entrypoint `./tera`, set `TERA_DB_PATH` to
   a path on the persistent disk and `TERA_COOKIE_SECURE=1`.

Do not subscribe to Zo *in order to* host this. $18/month buys 32 GB of RAM for
an application that needs half a gigabyte.

## Sources

- [Zo docs — Services](https://www.zo.computer/docs/services)
- [Zo docs — Sites](https://www.zo.computer/docs/sites)
- [Zo docs — zo.pub](https://www.zo.computer/docs/zo-pub)
- [Zo docs — Subscription/billing](https://www.zo.computer/docs/billing)
- [Zo pricing](https://www.zo.computer/pricing)
- [Zo Computer review — LOW/CODE](https://www.lowcode.agency/blog/zo-computer-review) (free-tier claim contradicted by the billing docs)
- [Cerebral Valley — Zo Computer](https://cerebralvalley.beehiiv.com/p/zo-computer-is-your-personal-ai-cloud-computer)
