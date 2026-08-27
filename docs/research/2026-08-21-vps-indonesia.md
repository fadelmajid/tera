# Cheap Indonesian VPS for a Tera test box

**Researched 2026-08-21.** Indonesian VPS pricing moves constantly and most
headline figures are promotional. Treat every number here as a snapshot and
check the provider's own pricelist before paying.

## First, a scoping note

**A VPS is not the shop deployment.** INV-11 says the till keeps working with no
internet, on the shop's own LAN, with the printer attached. A datacentre in
Jakarta cannot do either of those things — it is a different failure model, not
a better one.

What a VPS is good for: a box that stays up without your laptop, somewhere to
show the family the margin report, and a place to leave a demo running.

## Tera needs almost nothing

This is the finding that decides the whole question.

| | |
|---|---|
| Binary | 13 MB |
| Container image | 22 MB |
| RAM in practice | ~100–200 MB (512 MB specified with headroom) |
| Database after a year | tens of MB — 1,000 SKUs and under 1,000 transactions a day |
| Instances | Exactly one. SQLite has one writer, and `store.Open` pins the pool to one connection. |

**Every entry-level plan below is over-provisioned.** So specs do not decide
this; disk, included backups, payment method and price stability do.

## What is available (2026-08-21)

| Provider | Entry plan | Price/month | vCPU / RAM / Disk | Location | Notes |
|---|---|---|---|---|---|
| **DomaiNesia** VPS Lite 1GB | Rp43.200 promo, **renews Rp48.000** | 1 / 1 GB / 20 GB NVMe | Jakarta | RAID10, unlimited bandwidth, flat-pricing reputation |
| **Biznet Gio** NEO Lite XS 1.1 | **Rp59.000** | 1 / 1 GB / **60 GB SSD** | Indonesia | **Snapshots included**, unlimited 10 Gbps traffic, Docker/K8s supported |
| **CloudRaya** | from Rp20.000, **hourly** | varies | Jakarta, Surabaya, Bali, Medan | Best local payment: QRIS, OVO, Dana, ShopeePay, VA |
| **IDCloudHost** | ~Rp50.000–100.000 | 1 / 1 GB / 20 GB | Jakarta + Singapore | Raised prices April 2025; reviews flag inconsistent support |
| **Niagahoster** | Rp104.000 | 1 / 1 GB / 20 GB | Indonesia | Bandwidth capped at 2 TB/month, unlike the others |

DomaiNesia also runs a free 30-day VPS trial, but it is **not practically
usable**: 30 slots per quarter, selected quarterly, requires following three
social accounts, and takes up to 14 working days to activate.

## Recommendation

### For this app: Biznet Gio NEO Lite XS 1.1 — Rp59.000/month

Not the cheapest, and that is the point. Rp16.000/month more than DomaiNesia
buys two things that matter specifically to Tera:

- **60 GB disk against 20 GB.** Tera takes an hourly verified snapshot
  (`VACUUM INTO`) and keeps 72 of them by default. Each is roughly the size of
  the database. On a 20 GB disk that is fine for years — but the disk is also
  where the exports land, and headroom is worth having on the one box holding
  the numbers a family settles money on.
- **Snapshots included.** R14.1's whole point is that a backup nobody has
  tested is a rumour, and R8.7 accepts hardware failure *only* because backups
  mitigate it. A provider-level snapshot is a second, independent copy at a
  different layer from Tera's own. Paying separately for that elsewhere costs
  more than the price difference.

Unlimited 10 Gbps traffic and documented Docker support are incidental here but
remove two things to worry about.

### If cheapest wins: DomaiNesia VPS Lite 1GB — Rp43.200/month

NVMe, RAID10, Jakarta, and a reputation for flat renewal pricing rather than a
first-year discount that doubles later. Note the renewal is Rp48.000, not
Rp43.200. Perfectly adequate.

### If the box is only occasionally on: CloudRaya

Hourly billing genuinely lands around Rp20–50k/month if you destroy it between
sessions, and the payment options (QRIS, OVO, Dana, bank VA) are the easiest of
any provider here. Worth it if the test box is for a demo next week rather than
something that stays up.

### Avoid for this

**IDCloudHost** — a price rise in April 2025 and repeated notes about support
quality. **Niagahoster** — twice the price of DomaiNesia for the same specs and
a bandwidth cap nobody else imposes.

## What a VPS costs you that Fly and Zo did not

This is the real trade, and it is not money.

| | Fly / Zo | VPS |
|---|---|---|
| TLS certificate | Terminated for you | **Yours to run.** `deploy/Caddyfile` handles it via Let's Encrypt |
| OS patching | Platform's | **Yours.** Unmanaged means unmanaged |
| Firewall | Platform default | **Yours to configure** |
| Auth in front of the app | Zo has private mode | **None.** See below |

**The security gap matters more here.** Tera has no login rate limiting and no
account lockout — deliberate, because D-008's threat model is a shop LAN where
an attacker has to be in the building. A public VPS removes that assumption and
gives you nothing in return. `deploy/Caddyfile` carries a commented-out
`basicauth` block; **turn it on** if the box is reachable from the internet and
has anything resembling real data on it.

**And back up off the box.** A provider snapshot lives on the provider's
infrastructure; Tera's own snapshots live on the same disk as the database,
which is precisely what R14.2 refuses. Pull them down:

```bash
ssh vps 'docker compose -f deploy/docker-compose.yml exec -T tera tera backup --to /data/cadangan'
scp vps:/var/lib/docker/volumes/deploy_tera-data/_data/cadangan/tera-*.db ./
```

## Deploying

`deploy/docker-compose.yml` and `deploy/Caddyfile` are in the repo. On a fresh
Ubuntu box:

```bash
# on the VPS
curl -fsSL https://get.docker.com | sh
git clone <repo> tera && cd tera
$EDITOR deploy/Caddyfile          # set your domain, enable basicauth
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml logs tera | grep "pengguna pertama"
```

Tera's port is deliberately **not** published to the host — only Caddy reaches
it, so the app is never served over plain HTTP on the VPS's public address.

## Comparison against the earlier options

| | Tailscale funnel | VPS (Biznet Gio) | Fly.io | Zo |
|---|---|---|---|---|
| Cost/month | Free | Rp59k (~$3.60) | ~$2–3 | $18 |
| You run TLS | No | **Yes** | No | No |
| You patch the OS | No | **Yes** | No | No |
| Auth in front | Tailnet-only | Yours to add | None | Built in |
| Local payment (IDR) | — | **Yes** | Card only | Card only |
| Local support, Bahasa | — | **Yes** | No | No |
| Tests the real model | **Yes** | No | No | No |

**If the goal is still just "easy to test", the Tailscale funnel is still the
answer** — free, two commands, and it exercises the deployment the shop will
actually use.

**A VPS earns its keep when you want the box up without your laptop, billed in
rupiah, with support in Bahasa Indonesia** — which for a family business is a
better answer than a cheaper card-only foreign platform, and is the one real
argument for going local here.

## Sources

- [Biznet Gio pricelist](https://www.biznetgio.com/pricelist) · [NEO Lite](https://www.biznetgio.com/en/product/neo-lite)
- [DomaiNesia Cloud VPS Lite](https://www.domainesia.com/cloud-vps-lite/) · [free trial terms](https://www.domainesia.com/vps-gratis/)
- [CloudRaya VPS Indonesia](https://cloudraya.com/id/produk/vps-indonesia-murah/)
- [IDCloudHost pricing](https://idcloudhost.com/pricing/)
- [CekIPSaya — VPS Indonesia Terbaik 2026](https://cekipsaya.com/artikel/vps-indonesia-terbaik-2026/) (updated 11 April 2026)
