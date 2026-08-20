# DECISIONS — Tera

Resolved choices, with the reasoning. REQUIREMENTS §11 lists what is still open;
this is its counterpart. Append, don't rewrite — a superseded decision gets a new
row citing the one it replaces.

| # | Decision | Date | Affects |
|---|---|---|---|
| D-001 | Module path is `github.com/fadelmajid/tera` | 2026-08-20 | everything |
| D-002 | Go floor 1.23; tool versions pinned in the Makefile | 2026-08-20 | build, CI |
| D-003 | Identifiers are **UUIDv7**, stored as canonical lowercase `TEXT` | 2026-08-20 | every migration |
| D-004 | All three roles — owner, manager, staff — exist at go-live | 2026-08-20 | TASKS 0.6 |
| D-005 | Instants are UTC `INTEGER`; `business_date` and `book_year` are denormalised at write time in entity-local time | 2026-08-20 | every dated table |
| D-006 | The product catalogue is **shared across entities**; stock is what is entity-scoped | 2026-08-20 | migration 001, Phase 4 |
| D-007 | Every table is `STRICT` | 2026-08-20 | every migration |
| D-008 | Server-side sessions over plain HTTP on the LAN; cookie `Secure` is opt-in | 2026-08-20 | auth, deployment |
| D-009 | Owner and manager see owner margin figures; staff see none | 2026-08-20 | Phase 3, query layer |

---

## D-001 — Module path

`github.com/fadelmajid/tera`, private repo under the author's account. Changing
it later is a repo-wide rewrite plus a `.golangci.yml` depguard edit, so it was
settled before the first commit.

## D-002 — Go 1.23 floor, tools pinned in the Makefile

`go.mod` declares `go 1.23.0`, matching the stack in CLAUDE.md, and CI builds on
exactly that so the floor is real rather than aspirational. Tool versions
(golangci-lint, sqlc, goose) are pinned as Makefile variables and fetched with
`go run <pkg>@<version>` when no local binary is present.

The alternative — go.mod `tool` directives — is tidier but requires Go 1.24+,
which would raise the floor for no benefit the project currently needs.

## D-003 — UUIDv7 as TEXT

SQLite has no UUID type, so the choice is a storage encoding plus a version.

**Version 7, not 4.** FIFO consumption is oldest-first and breaks ties on
`acquired_at` by `id`, for determinism (SPEC §3.3). Two layers acquired in the
same second is ordinary — one purchase, several lines. With v4 that tiebreak is
stable but arbitrary: the layer created second can be consumed first. With v7 the
id is time-ordered, so the tiebreak resolves to insertion order, which is what
FIFO means. The invariant is unaffected either way; v7 just makes it agree with
intuition, and it keeps a family from being told the cheaper layer went first
when it didn't.

v7 also clusters index writes instead of scattering them, though at this scale
that's a rounding error and not the reason.

**TEXT, not BLOB.** The canonical 36-character form costs 20 bytes more per id
than a 16-byte blob. At 1,000 SKUs that is noise, and it buys a database anyone
can open in `sqlite3` and read — which matters for a system whose backup story is
"copy the file" and whose recovery story is "restore onto any laptop" (R8.7).

Generated with `github.com/google/uuid` (`uuid.NewV7`). Lowercase, canonical
hyphenated form, enforced at the boundary — mixed case in a TEXT primary key is
a silent duplicate waiting to happen.

## D-004 — Three roles at go-live

owner, manager, staff — all three exist from TASKS 0.6, per R13.2. R7.1 depends
on the staff/manager distinction existing, so deferring the role would mean
retrofitting authorization into every mutating handler later.

**Still open, and separate:** whether staff can *see* other owners' margin
figures (R13.3). That is a visibility rule inside the Phase 3 UI, not a question
of whether the role exists. Sensitive in both directions in a family business —
needs the user's answer before the margin screen ships.

## D-005 — UTC instants, entity-local date denormalised at write time

Every table that records when something happened carries two things:

    occurred_at    INTEGER  -- unix seconds, UTC. The instant.
    business_date  TEXT     -- 'YYYY-MM-DD' in the entity's timezone. The day it counts for.

plus `book_year INTEGER` wherever the book year is the reporting window
(`omzet_ledger` already has it, ARCHITECTURE §3).

**Why not derive the local date at read time.** INV-5 requires book-year and
business-day boundaries in entity-local time. SQLite cannot do that conversion:
`datetime(ts, 'unixepoch', 'localtime')` uses the *server process's* timezone,
not the entity's. It would produce right-looking answers on a shop PC set to WIB
and wrong ones the moment the machine is restored onto a laptop in another zone
(R8.7 says that restore is the recovery plan), or if the two entities ever
differ. So deriving in SQL is not a worse option — it is not an available one.

Deriving in Go at read time is available, but then the margin report, the omzet
clock, the Z-report, the sales report, and the aging buckets each re-implement
the same rule, and the one that gets it wrong is discovered in January.
Computing it once, at the moment of the event, from the entity's IANA timezone
and its `book_year_start_month`, means every reader just groups by a column.

**This is a snapshot, not a cache.** It looks like it contradicts the
derive-don't-store stance behind INV-7, and the distinction matters enough to
write down: a layer's remaining quantity is a *running* figure over an
append-only event stream, and storing it would destroy the audit trail. A
business date is a *pure function of the moment it happened* — instant, entity
timezone, book-year start — fixed forever once the event exists. It belongs with
the tax snapshot (INV-3), not with a materialised balance. Recomputing it later
from current config is the bug, not the feature.

Corrections inherit rather than recompute: a void or refund carries the original
transaction's `business_date` and `book_year`, so a January void of a December
sale decrements the prior book year (SPEC §5.4, TASKS 7.6).

**Seconds, not milliseconds.** Ties are expected — one purchase writes several
layers in the same second — and they are already resolved deterministically by
the UUIDv7 tiebreak (D-003). Finer granularity would reduce ties without
removing the need to break them.

**Never store local wall-clock time**, with or without an offset. An instant is
UTC; a local rendering is derived from it.

**Consequence for the binary:** `cmd/tera` blank-imports `time/tzdata`. The
server is a shop PC that may carry no system zoneinfo, and without the embedded
database `time.LoadLocation("Asia/Jakarta")` fails, the fallback is UTC, and a
23:30 WIB sale on 31 December books into the following year — silently, once a
year, in the figure the omzet alarm reads. Costs ~450KB and keeps the deploy one
file. TASKS 7.3 is the test.

## D-006 — One product catalogue, entity-scoped stock

**This departs from the ER in ARCHITECTURE §3**, which draws
`LEGAL_ENTITY ||--o{ PRODUCT : owns`. Flagging it rather than quietly diverging.

The alternative is a product row per entity. Then an inter-company transfer of
product X from entity A to entity B has to bridge A's row to B's row, and the
only thing to match them on is `code`. That bridge is a permanent source of
drift: the two rows can disagree on name, unit, or barcode; a typo in a code
silently breaks a transfer or, worse, transfers into the wrong product. Phase 4
exists precisely because Olsera's inter-company stock movement is broken and the
user's quantities are drifting from reality today — founding it on a fuzzy
string match would reproduce the bug we are being paid to fix.

With a shared catalogue, a transfer is: consume layers where `entity_id = A`,
create a layer where `entity_id = B`, same `product_id`, same `owner_id`. There
is nothing to match and nothing to drift. Note `stock_layer` already carries its
own `entity_id` in SPEC §3.1, which only makes sense if the product does not.

**What this defers.** Per-entity selling prices. The PKP entity's shelf prices
should be PPN-inclusive while the non-PKP entity's are not (REQUIREMENTS §4), so
a single `product.sale_price_idr` will not hold forever. That is an additive
`product_entity_price` table when Phase 5 needs it — cheap, because the product
identity it hangs off is already stable. Building it now would be speculative;
building the transfer bridge now would be a mistake.

R11.1 lists one selling price, so this matches the requirement as written.

## D-007 — STRICT tables

Every table is declared `STRICT`. SQLite's default type affinity will store the
text `'abc'` or the float `1000.5` in a column declared `INTEGER` and say
nothing. STRICT rejects both at the storage layer.

That turns INV-1 from a convention the code is expected to honour into something
the database refuses to violate — including through a hand-written `INSERT` in a
`sqlite3` shell during a support session, which is exactly when the convention
would otherwise be forgotten.

## D-008 — Sessions on a plain-HTTP LAN

Sessions are server-side rows, not signed tokens. There is no key to rotate, no
clock skew to reason about, and revoking a session is a `DELETE` — which matters
when the machine holding the family's settlement figures is a PC in a shop. The
token is never stored, only its SHA-256, so a copy of the database file — and
R14 puts copies on removable media — does not hand anyone a working session.

**The trade worth stating plainly.** The shop LAN is plain HTTP. There is no
certificate authority behind a `192.168.1.x` address, and R8.6 wants a stable IP
or mDNS name rather than a domain. So the session cookie is `HttpOnly` and
`SameSite=Lax` unconditionally, but `Secure` is off by default — a Secure cookie
over HTTP is simply never sent, which would lock every user out on day one.

What that costs: anyone who can already put a device on the shop's network and
observe traffic can capture a session cookie. That is the same person who could
already reach the server directly. It is a real exposure and it is accepted for
the same reason R8.7 accepts the single point of failure — the threat model is a
small shop's own network, not a hostile one.

`TERA_COOKIE_SECURE=1` turns it on the day TLS is terminated in front of the
server. gosec's G124 flags the non-literal `Secure` value; the two suppressions
point here rather than hiding it.

Passwords are bcrypt at cost 12. An unknown username still pays for one bcrypt
comparison, so login timing does not reveal which usernames exist.

## D-009 — Who sees owner margin (R13.3, resolved)

Owners and managers can see owner margin figures. Regular staff cannot — and
cannot see a reduced version either. A cashier is not one of the family members
products are attributed to, so "other owners' figures" means all of them.

R13.3 flagged this as needing confirmation; the user confirmed it on 2026-08-20.

**Enforced in the query layer, not the screen.** A margin figure that reaches the
browser has already left the building, so hiding a table client-side is not the
control. The margin endpoints refuse the request; the UI simply does not offer
the menu item.

This is a per-entity authority like every other (R13.4): a manager of the
non-PKP entity sees that company's margin, not the PKP entity's.
