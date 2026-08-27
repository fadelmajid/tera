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
| D-010 | A sales return restores to the **original** FIFO layer, as an appended reversal | 2026-08-21 | TASKS 1.2, 2.9, 3.1 |
| D-011 | Owner margin attributes revenue from the sale line and COGS from the stock layer, and **refuses when they disagree** | 2026-08-21 | TASKS 3.1, 3.3 |
| D-012 | A cross-boundary return counts in the month the **goods came back** (`RETURN_DATE`), configurable per entity | 2026-08-21 | TASKS 3.4 |
| D-013 | A sale that has had goods returned **cannot be voided**, however open the till still is | 2026-08-21 | TASKS 2.9, 2.10 |
| D-014 | An inter-company transfer creates **no hutang or piutang** — the two companies net off in their own report | 2026-08-21 | TASKS 4.5 |
| D-015 | Owner margin revenue is the **DPP**, not the amount the customer handed over | 2026-08-21 | TASKS 3.1, 5.3 |
| D-016 | A PKP sale with **no tax rule in force is refused** — the till stops rather than charging nothing | 2026-08-21 | TASKS 5.3, 5.4 |
| D-017 | **No PPnBM rate is seeded**, against SPEC §2.1's literal list | 2026-08-21 | TASKS 5.2 |
| D-018 | An invoice with **no due date is not aged** — its own bucket and a visible count, never folded into "current" | 2026-08-21 | TASKS 6.4, 6.5 |
| D-019 | The data export is the **whole database** and requires owner in **every active company** | 2026-08-21 | TASKS 6.6 |
| D-020 | A **void** carries the original sale's omzet date; a **return** carries the day the goods came back | 2026-08-21 | TASKS 7.5 |
| D-021 | **Both readings** of the PKP registration deadline are implemented; the choice is a config row | 2026-08-21 | TASKS 7.9 |
| D-022 | Findability is a **hostname**, not a bundled mDNS responder | 2026-08-21 | TASKS 8.3 |
| D-023 | **No Olsera history is migrated** — their exports cannot support it | 2026-08-21 | TASKS 8.4 |

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

## D-010 — A sales return restores to the original layer (R12.1, resolved)

Confirmed by the user on 2026-08-21: the goods go back to the layer they were
drawn from, not to a new layer at the return date.

**Why it is the right answer.** COGS reverses at exactly the cost that was
taken, so the margin reverses exactly. A new layer at return-date cost would
reverse revenue in full while reversing cost at a different figure, quietly
inventing margin — in the one report family members settle money on (R2.4). It
also keeps owner attribution automatic: the original layer belongs to an owner,
so the reversal lands in that owner's bucket without anyone deciding whose it is
(INV-8). And the layer's `faktur_received` basis is preserved rather than
re-derived (INV-9).

### The mechanism: append a reversal, never edit the layer

"Restore to the original layer" must not become "increment the layer". Layers
and consumptions are both append-only (INV-7), and a corrected transaction is a
compensating record, never a mutation (INV-2). So a return appends a **new
`stock_consumption` row** against the original layer with a negative `qty_out`
and a negative `cost_idr`.

The derivation is unchanged and needs no special case:

    remaining(layer) = qty_in - Σ qty_out

A negative draw raises the remainder. The layer row is never touched, and the
trail reads in order: this sale took 3 units at this cost, this return gave 2 of
them back at the same cost.

**This extends SPEC §3.1**, which describes `stock_consumption` without a
reversal concept. Two additions when the table is built (TASKS 1.1):

- `reverses_id` — nullable, references another `stock_consumption` row. A
  reversal must name the draw it reverses; a "return" that corresponds to no
  actual sale is a bug, not a stock increase.
- A rule enforced in the service layer: the reversals against one consumption
  cannot exceed what it consumed. Returning four of three units sold is not a
  correction, it is data entry to reject.

### The trade being accepted

A layer restored months later keeps its original `acquired_at`, so oldest-first
consumption will draw those units before newer stock. For *costing* that is
exactly right — it is the same goods at the same cost. For *physical picking* it
points at older stock, which matters when a batch expires. FEFO and batch-expiry
picking are explicitly out of scope (REQUIREMENTS §8), and the expiry field is
captured but unused (§6.4), so nothing acts on this today. Worth remembering if
expiry ever comes into scope.

### Still open, and a different question

This decides **which layer**, not **which month**. A November return of an
October sale — whose margin period does it land in, when October's money was
already split — remains undecided (SPEC §4.4, TASKS 3.4). Do not read D-010 as
having answered it; the two get conflated easily because both are "what happens
to a return".

## D-011 — Two attribution paths, checked against each other

Owner margin needs two figures per sale line, and they come from different
places written at different times by different people:

- **Revenue** is attributed by `sale_line.owner_id`, snapshotted when the sale
  was rung. Re-tagging a product later must not move money already settled.
- **COGS** is attributed by `stock_layer.owner_id`, recorded when the goods were
  bought, and reached through the `stock_consumption` rows the sale actually
  drew (SPEC §4.1). Never an average.

They should always agree, because `fifo.Consume` is owner-scoped (INV-8) and a
product carries one owner. `internal/domain/margin` checks anyway, and returns
`ErrOwnerMismatch` naming the sale and the product rather than producing a
figure. It also refuses when a line's recorded COGS does not equal the layer
draws behind it (`ErrCOGSMismatch`), and when a draw belongs to no line
(`ErrOrphanDraw`).

**Why refuse rather than pick one.** Every one of those cases produces a number
that looks entirely reasonable. On the report family members divide money on,
nothing downstream would ever question it — the drill-down would agree with
itself, and the error would surface as one person quietly receiving 11% less
than they earned. A refusal is loud, findable, and fixable. A plausible wrong
number is none of those.

**What this costs.** A corrupted row takes the whole report down rather than one
line of it. Accepted: at this scale the report is one query over a few hundred
rows, and there is no state of the database in which a partial margin report is
the right thing to show.

### Attribution is by product, not by a stored line reference

`stock_consumption` names the movement and the layer; the layer carries the
product and the owner. There is no `sale_line_id` on it, and none was added —
the drill-down SPEC §4.2 asks for is owner → sale → layers, and the layer's own
owner is the authority for cost. A sale with two lines of the same product
therefore appears as one product row, summed. That is correct rather than merely
tolerable: same product, same owner, same layers.

## D-012 — A cross-boundary return counts in the month the goods came back

SPEC §4.4 asked what a November return of an October sale does when October's
money has already been split. Resolved on 2026-08-21: **`RETURN_DATE`** — the
reversal comes out of November. `SALE_DATE` remains implemented and switchable
per entity.

### Why

**It never restates money that has already been divided.** The family settles
monthly. Under `SALE_DATE` a return accepted in November silently changes
October's figure — a month whose money was already handed over — and with no
period locking (R7.3) nothing prevents it and nobody is told. The only signal is
an audit row nobody reads proactively. Asking a family member to hand money back
because a report moved under them is the specific failure this system exists to
avoid.

**The margin lands where the money and the paperwork already are.** The goods
physically came back in November. The refund left the till in November, in a
November cash session, counted in a November Z-report. Under `SALE_DATE` the
margin reduction sits in October while the cash sits in November, and the two
ledgers disagree about which month lost money.

**It self-corrects; the alternative does not.** Returns are small and roughly
steady month to month, so over a year the two rules converge to the same total.
What does not converge is the cost of a wrong restatement after settlement.

**It is the recoverable direction.** If the family later decides `SALE_DATE`
matches how they actually settle, flipping the setting restates the reports —
a config change, not a migration. Going the other way, after months have been
restated and re-settled under `SALE_DATE`, is far messier.

### What it costs, and the mitigation

October's report overstates what really sold that month, and a large return in
the first days of November makes November look bad for something that happened
in October.

Both directions are made visible rather than left to be discovered:

- In the month it is counted, a return is its own line — never netted into
  revenue — carrying the return date, the original sale date, the date the rule
  actually counted, and a **lintas bulan** flag.
- In the month of the original sale, the same return appears as
  `OwnerReport.LaterReturns`: an informational section, excluded from every
  total, saying that goods from this month's sales came back later and where
  they were counted. So "Budi's October included two boxes that came back" is
  answerable from October's report, not only from November's.

That second section exists *because* of this decision. Under `SALE_DATE` it is
empty by construction, since the return is already counted in the sale's month.

### How it is configured

`margin_setting` (migration 010), one row per entity, changed only through
`Margin.SetReturnRule` — owner-only, and audited (INV-10), because changing it
changes what every past report says. No row means the default above.

`margin.ReturnPeriodUnset` is an error rather than a default, so the domain
cannot compute a settlement figure without a rule having been named. The report
states the rule in force on screen every time, next to the figures it produced.

## D-013 — A returned sale cannot be voided

R12.3 asks for a void (same-day error) and a return (goods came back later) as
distinct corrections. What separates them is one fact: **whether the goods ever
left the shop.** A void says they did not. A return is proof they did. So the
two cannot both apply to one sale, and `Sales.Void` now refuses once any
`sale_return` exists against it — regardless of whether the cash session is
still open.

The pair was already half enforced: `Sales.CreateReturn` has always refused a
voided sale. This closes the other direction.

### What was actually going wrong

Not the stock. `giveBackEverything` reverses each draw's *remaining* quantity,
so a void after a partial return gives back only what the return had not, and
quantities stayed correct.

The damage was in the till. Voiding withdraws the sale's takings from the cash
session, but the cash refund already paid against it still stands, and
`SumSessionCashRefunds` counts it. On the case that found this — a Rp 80.000
sale, one of four boxes returned for Rp 20.000 cash, then voided — expected cash
fell from Rp 560.000 to Rp 480.000 while the drawer really held Rp 560.000. The
Z-report told whoever counted it that the till was **Rp 80.000 over**: money that
is genuinely there, reported as a surplus, and the correction for a surplus is
somebody guessing.

It also wrote the whole sale off while a partial refund had already been paid
against it, which is not a state any report should have to interpret.

### The consequence worth keeping

A sale is now never both voided and returned. That is what lets the Z-report and
the margin report each read one of the two without checking for the other. The
margin queries still filter returns of voided sales — defence in depth for a
state the service will not create, because this database is explicitly one
anybody can open in `sqlite3` (D-003) and the recovery story is a file copy.

### Also fixed alongside

None of `ErrNoOpenSession`, `ErrSessionClosed`, `ErrVoidWindowClosed` or
`ErrAlreadyVoid` were mapped in `writeServiceError`, so all four fell through to
HTTP 500 and `"kesalahan internal"`. Every one of them already says in
Indonesian what the cashier should do instead — open a session, issue a return
rather than a void — and that text was being thrown away at the moment someone
has a customer in front of them. All are now 409, with the message intact.

## D-014 — A transfer nets off between the companies, it does not raise hutang

TASKS 4.5 asked whether moving stock from one company to the other creates a
debt between them. Decided with the user on 2026-08-21: **it records what the
receiving company owes, at cost, and the two net off in a report of their own.**
No `payable` row, no `receivable` row.

### Why not full hutang and piutang

It is the most faithful model of two separate legal entities, and it was
rejected on what it would cost the reports that already exist.

`payable.supplier_id` and `receivable.customer_id` are both `NOT NULL`, so a
company owing a company does not fit without either creating supplier and
customer records that stand for the family's own companies — which puts
"PT Sehat Sentosa" in PT Medika Jaya's supplier list — or rebuilding both tables
to take an entity counterparty. Either way the hutang aging report then mixes
real supplier debt with family bookkeeping, and R5.5's whole point is that a
balance without an age is not actionable. An age is meaningless on a balance
between two companies the same people own.

### What was built instead

`transfer.amount_idr` is cost plus any PPN charged on the delivery — what the
receiving company owes. `InterCompanyPosition` groups by counterparty and both
legs, and the service nets them: positive means the counterparty owes this
company, and the other company sees the mirror image rather than a second
opinion. It is a screen of its own, next to the transfer that produced it.

Settlements between the two are handled outside the application, the same way
shared costs already are (REQUIREMENTS §5). If the family later wants payments
recorded against transfers, that is an additive table, not a change to this one.

### What this does not do

It takes no view on whether the transfer price is defensible. Transferring at
cost between two entities under common family control is a related-party
transaction whatever the direction (REQUIREMENTS §6.1, Pasal 18 UU PPh), and
that is a conversation for their konsultan pajak, not a calculation this system
should be making for them.


## D-015 — Owner margin revenue is the DPP

Turning on the tax engine (TASKS 5.3) changed what the margin report means, and
this is that change written down.

A sale of Rp 240.000 at an inclusive effective 11% is Rp 216.216 of revenue and
Rp 23.784 of PPN. The report counts the Rp 216.216.

### Why

COGS comes off a stock layer whose `cost_total_idr` is already net of creditable
PPN (SPEC §3.2). Subtracting a tax-exclusive cost from a tax-inclusive revenue
overstates the margin by the rate — about 11% of turnover, in the one report
family members settle real money on monthly (R2.4). Whatever else is arguable
about that report, both sides of the subtraction have to be measured the same
way.

The second reason is that the PPN is not the shop's money. It is collected on
behalf of the state and appears again in the PPN position (SPEC §2.4). Dividing
it between family members and then paying it at the masa pajak filing takes it
out of somebody's pocket twice.

### What this cost

Every figure in `TestMarginAttributionIsExactAcrossAMonth` moved, and that test
carries an explicit instruction not to update its expectations to match new
output. They were re-derived by hand, not accepted: the derivations are in the
comments beside them.

### The mitigation

A revenue figure that does not match the invoices is a figure someone will
distrust, and rightly. So the report carries the tax alongside: `ppn_idr` and
`tendered_idr` on every owner line and every sale, and the screen states the
arithmetic in words — "Diterima dari pelanggan Rp 240.000 = penjualan
Rp 216.216 + PPN Rp 23.784. PPN disetor ke negara dan tidak masuk margin."

A return works the same way. `refund_idr` stays the whole sum handed back,
because that is what the customer received and what the document says;
`ppn_reversed_idr` is the tax inside it, and only the difference comes off the
margin.

### What this does not change

Nothing at the non-PKP company, where there is no PPN and revenue is the whole
amount. Nothing before the tax engine landed either: migration 013 backfills
`sale_line.dpp_idr` from `net_idr`, so historical margin figures are exactly
what they were.

## D-016 — A PKP sale with no tax rule in force is refused

The till stops, with a message naming the date and pointing at the tax settings
screen. It does not ring the sale at no PPN.

### Why

A PKP owes output PPN on the delivery of taxable goods whether or not it charged
any, and whether or not the buyer took a faktur (SPEC §2.3, TASKS 5.4). A sale
priced at zero because config had a hole does not avoid the liability — it
accrues it silently, against no document, to be discovered at the masa pajak
filing and paid out of margin.

Refusing costs an hour and a phone call. Not refusing costs 11% of whatever was
sold in the meantime, and the person who finds out is an accountant, months
later.

The mirror case is refused for the same reason: a company marked non-PKP that
holds a rule already in force is a contradiction only a person can resolve —
either it registered and nobody flipped the flag, or a rule staged for a future
registration has quietly taken effect.

### What makes this survivable

Staging is supported, so the correct workflow never trips it. After crossing the
threshold, registration is due by the end of the book year and the obligation
starts in the first tax period of the following one (PMK 164/2023 Pasal 17(3),
Pasal 18); a rule dated to that period sits harmlessly in config until then.

And a company gets its rule when it is created, not as a step somebody discovers
at the till (TASKS 5.2). The refusal is reachable mainly by closing a rule
without opening its replacement — which is the case worth stopping for.

## D-017 — No PPnBM rate is seeded

SPEC §2.1 lists a PPnBM row among the seeded defaults, at 12% on a full DPP.
`seed.TaxRulesFor` does not write it. This is a considered deviation.

Nothing in an alat kesehatan catalogue is a luxury good, so no line would ever
be levied under it — but the row would appear on the tax settings screen with a
rate and a citation beside it, and a figure presented that way is a figure
somebody will believe. The PPnBM tariff is banded by product, and it could not
be verified from here which band applies, whether PPnBM shares PPN's DPP or
stacks on top of it, or whether the nilai lain reaches it at all.

TASKS 5.6 says not to invent a figure somebody might trust. A rate on an admin
screen is exactly that, and an unused rule with an unverified rate in it is a
trap for whoever reads that screen in two years.

`domain/tax` still models the type, so a verified rate is a row rather than a
code change. The question is recorded in
`testdata/worked_examples/ppn_unverified.json` with what would need answering
before one is added.


## D-018 — An invoice with no due date is not aged

It goes into a bucket of its own, is counted on the screen, and is excluded from
the overdue total.

### Why not fold it into "current"

Because that reports it as fine. Most aging reports do exactly this, and it is
the reason a supplier can be six months unpaid while the screen looks healthy.
An invoice with no recorded term might be current, might be a year late; this
system does not know, and the honest report is one that says so.

### Why not age it from the invoice date

That is the other common choice, and it is worse. Ageing from `incurred_on`
assumes payment was due on delivery — a net-0 term nobody agreed to — and then
reports a supplier who deliberately gave no term as ninety days delinquent. It
manufactures a fact out of an absence.

### Why not settle the terms first

Because the terms are genuinely unknown. REQUIREMENTS §11 has the question open
in the user's own framing — "net 30, case by case?" — and it needs the family to
answer it, not us to pick. Until then the report cannot age those documents and
should not pretend to.

### What makes this actionable rather than merely honest

The count. `Report.WithoutDueDate` is a field, not something to be inferred from
a bucket total, and the screen leads with it: "3 dokumen tanpa tanggal jatuh
tempo, nilainya Rp 8.000.000". The fix is data entry, and the number is what
prompts it. When the terms question is answered the field goes to zero on its
own, without a code change.

### The related choice

An over-payment is reported separately as a credit rather than aged or netted.
A credit is not a debt and cannot be late — but dropping it would lose an
invoice paid twice, which is exactly the kind of thing a ledger should not
swallow quietly.

## D-019 — The export is the whole database, gated on owning every company

`GET /export` returns every table and view in the database. It is refused unless
the caller holds the owner role in every **active** legal entity, and the
refusal names the company they lack.

### Why it is not scoped per company

Because the database is not. The product catalogue, the owners, the suppliers
and the customers are shared across both entities by design (D-006). A
per-company archive would produce two files that each contain the shared half
and neither of which is the database — and R14.3 asks for the data, not for a
view of it.

Scoping it would also require per-table knowledge of how to reach an
`entity_id`, including through child tables. That is precisely the kind of
hand-maintained mapping that makes an exit route stop being complete the first
time somebody adds a table.

### Why the gate is ownership everywhere

Because the archive crosses the boundary R13.4 draws. A user may hold different
roles in each company; someone who owns only the non-PKP entity must not be able
to download the PKP entity's entire history by asking for "the export".

In the arrangement this was built for — a family whose owners hold both
companies — nobody ever sees the refusal. Creating a second company through the
API grants the creator owner in it, so the ordinary path stays open. When the
refusal does fire it names the company, because the fix is a role grant and an
owner can make one.

### Why the completeness is enforced rather than intended

The exit route is the one feature whose value is realised only when this product
has failed its customer. Nobody exercises it on an ordinary day, so nothing else
would notice it rotting.

Two things hold it. The table list is read from `sqlite_master` at export time,
so a table added next year is included without anybody remembering to add it.
And `Export.Archive` re-counts every table from the database after writing it
and refuses to hand over an archive that came up short — a quiet failure here
would be discovered on the day it is needed and no earlier.

### What is deliberately not in it

Nothing. Including `goose_db_version`, which says which schema shape the archive
holds, and the derived views, which save the reader reimplementing FIFO in
Excel. The README states how money, dates, instants and NULL are written, and
points at the SQLite file as the byte-exact copy — the archive is the
interpretable form, not a replacement for it.


## D-020 — A void is dated to the sale; a return is dated to itself

TASKS 7.5 says "void/refund → compensating row at the original sale's
effective_date". Only the void does. A return carries the day the goods came
back.

### Why they differ

They are different facts. A void says the sale did not happen, so the turnover
has to be removed from where it was counted — which may be a different book
year from the day somebody noticed, and the threshold resets between them
(SPEC §5.4). A return says the sale did happen and goods came back afterwards;
the turnover occurred, and it is being reduced now.

This is the same distinction D-012 already drew for the margin report, where a
cross-boundary return counts in the month the goods came back. Having the omzet
clock disagree with the margin report about what a return is would be two
answers to one question.

### What it buys

Stickiness falls out of the arithmetic instead of being bolted onto it.

`Compute` walks the ledger in effective-date order and tests the threshold
against the cumulative at the **end** of each day. So a void, landing back on
the day it is undoing, is applied before that day is tested — and if removing it
means the day never reached the threshold, the year genuinely never crossed. A
return, landing later, cannot lower an earlier day's total, so a year that
crossed in September stays crossed however much comes back in November.

SPEC §5.2 asks for exactly that stickiness. Implementing it as a separate
"once crossed, stay crossed" flag would have been the obvious way, and it would
have been wrong: it would also have kept a crossing that a void proved never
happened.

### What is not settled

Whether returns reduce peredaran bruto at all. Ordinary practice says yes, and
that is what is implemented, but it is not confirmed against a regulation — the
third question in `testdata/worked_examples/omzet_unverified.json`. If the
answer is no, the RETURN and REFUND rows stop being written; the SALE rows
already carry the full turnover, so nothing else changes.

## D-021 — Both readings of the registration deadline are implemented

`omzet_threshold` carries `register_by_policy` and `vat_starts_policy`, each
with two values, both implemented in `domain/omzet` and both tested. The seeded
pair is SPEC §5.2's.

### Why not just pick one

Because the two are months apart on the same facts, and the gap between the two
dates is most of this feature's value.

SPEC §5.2 says registration is due by the end of the current book year and the
PPN obligation starts in the first tax period of the following one, citing
PMK 164/2023 Pasal 17(3) and Pasal 18. But PMK 164/2023 governs the 0.5% final
PPh regime for small business, where exceeding Rp 4.8 billion moves a taxpayer
to ordinary rates from the following tax year. That is a PPh rule. The rule
usually quoted for PKP confirmation itself is PMK 197/2013 Pasal 4, which gives
until the end of the month **after** the month the threshold was passed.

On a crossing dated 14 July 2026 the first reading gives 31 December 2026 and
1 January 2027. The second gives 31 August 2026 and 1 September 2026. A business
told the first when the second applies misses its registration by four months.

They may well both be right about different obligations, in which case the
screen should eventually show two pairs rather than one. That is a bigger change
than a config row and is not worth making on a guess.

### Why this is the same shape as D-017

Phase 5 refused to seed a PPnBM rate nobody had verified, because a figure on an
admin screen with a citation beside it is a figure somebody will believe. The
same reasoning applies to a deadline: this one appears on the dashboard in bold,
next to the words "paling lambat".

The difference is that a deadline cannot simply be omitted — a crossed business
needs to be told something. So both readings ship, the seeded one is SPEC's, and
the question is recorded where it will be asked.

### What answering it costs

One row. `domain/omzet` implements both policies and
`TestBothDeadlinePoliciesAreImplemented` covers them, so the answer is a
configuration change with no release behind it.


## D-022 — Findability is a hostname, not a bundled mDNS responder

R8.6 asks for a stable address. Tera prints `http://<hostname>.local:8080` at
startup and `docs/NETWORK.md` says how to set the hostname. It ships no mDNS
code.

### Why not ship a responder

Because it would not do what people assume. A Go mDNS library advertises a
*service* — `_http._tcp` — which makes the server visible in a service browser
and does not make `tera.local` resolve. Resolving a hostname needs a host
responder, and macOS, most Linux desktops and Windows 10+ all already run one
for their own machine name.

Shipping a second responder would add a dependency, duplicate the operating
system, and fix nothing that setting a hostname does not fix better.

### What the code does instead

It notices when the address moves. Tera records the LAN addresses it served on,
beside the database, and warns at the next start when they differ — naming the
old address and the new one. That is the failure R8.6 is about: DHCP renews the
lease until one day it does not, every bookmark in the shop breaks at once, and
the only symptom is a browser that cannot connect, which looks exactly like the
server being down. The server is the only thing in the building that can see
both addresses.

A warning, never a refusal. A till that will not start because its IP moved is a
worse outage than the one being warned about.

## D-023 — No Olsera transaction history is migrated

Migration is **master data plus opening balances from a physical count**
(R11.5, TASKS 8.5). No past sales, no past purchases, no historical margin.

This is a consequence of what Olsera exports, not a shortcut.

### What their exports actually are

From their published knowledge base: the **import** side is well documented,
with downloadable CSV templates for suppliers, customers, purchases, stock in,
stock out, stock opname and production. The **export** side is documented only
as *report* exports — the remaining-stock report to PDF and Excel, unpaid
purchases to Excel, the daily transaction report emailed or printed.

Two things follow, and both are decisive:

- **The stock-movement view carries quantities only.** Awal, Masuk,
  Pengembalian, Penjualan, Keluar, Sisa. No cost per movement, and no documented
  export at all.
- **No documented full-data export and no public API.** Nothing in their FAQ
  about exporting everything, data ownership, or account closure.

### Why that rules out history

FIFO layers need a per-purchase cost and a faktur-received flag (SPEC §3.2,
INV-9) — that flag is worth 11% of the cost basis and is not a field their
system appears to have at all. Owner attribution does not exist there either;
the business currently fakes it with product categories, which is the whole
reason this project exists.

A report-shaped export of quantities cannot reconstruct any of that. An importer
fed those figures would produce FIFO layers with invented costs, and every
margin figure built on them would be a fabrication — in the one report a family
settles money on.

### And the source data is known to be wrong

Olsera records an inter-company transaction without moving the stock, so their
quantities have been drifting for as long as the business has been doing it
(REQUIREMENTS §2). Importing them would import the drift and then build a cost
basis on it. The physical count at go-live is the only thing that resets it, and
it is the last easy moment to do one.

### What is still worth building, when the files exist

A **column-mapping CSV importer for master data** — products, suppliers,
customers. A thousand SKUs typed by hand is the real pain of go-live, and that
part carries no cost basis and no attribution, so a bad import is visible and
fixable rather than silently wrong.

Deliberately not built yet, and not blind: it should map whatever headers the
actual export produces rather than hardcode columns nobody has seen. **The next
step is the user sending a real Olsera export.**
