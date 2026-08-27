# TASKS — Tera

Issue-sized, dependency-ordered. One PR each. `[S]` ≈ half a day, `[M]` ≈ 1–2 days, `[L]` ≈ 3–5 days for one experienced engineer working with an agent.

**⭐ = differentiator.** If time runs short, cut elsewhere.

**Build order ≠ priority order.** REQUIREMENTS §9 ranks the user's pain: owner margin first, omzet clock last. But there's no margin report without purchases, and no purchases without suppliers. Foundations get built first — they just shouldn't be *polished* at the expense of the ⭐ work.

---

## Phase 0 — Foundations

| # | Task | Size | Deps |
|---|---|---|---|
| 0.1 | Repo init: Go module, golangci-lint, Makefile, CI (build/test/lint) | S | — |
| 0.2 | `domain/money`: IDR int64, arithmetic, decimal boundary, `Rp 1.234.567` formatting | S | 0.1 |
| 0.3 | SQLite bootstrap: goose, WAL, sqlc wiring | M | 0.1 |
| 0.4 | Migration 001: `legal_entity`, `owner`, `product`, `supplier`, `customer` (R11) | M | 0.3 |
| 0.5 | HTTP server: chi, **binds 0.0.0.0**, health, graceful shutdown, slog | S | 0.1 |
| 0.6 | Users, roles (owner/manager/staff), login, per-entity scoping (R13) | M | 0.4 |
| 0.7 | Idempotency middleware on `client_request_id` + `request_log` table (INV-6) | M | 0.3, 0.5 |
| 0.8 | `audit_log` table + service-layer helper; every mutation writes one (INV-10) | M | 0.4 |
| 0.9 | React + Vite scaffold, `embed.FS` into the binary, **UI in Bahasa Indonesia** | M | 0.5 |
| 0.10 | Master data CRUD screens: products, suppliers, customers, owners | L | 0.9, 0.4 |

**Exit:** log in from a second device on the LAN, create a product attributed to an owner.

---

## Phase 1 — Purchasing and FIFO ⭐

Everything downstream is wrong without this. Start here, not with the cashier.

| # | Task | Size | Deps |
|---|---|---|---|
| 1.1 ⭐ | Migration 002: `stock_layer`, `stock_consumption` — **both append-only** (INV-7) | M | 0.4 |
| 1.2 ⭐ | `domain/fifo`: `Consume(request, layers) → consumptions, COGS`. Pure. Oldest-first, **owner-scoped** (INV-8). | L | 0.2, 1.1 |
| 1.3 ⭐ | Insufficient-stock is an **error**, never a silent draw from another owner's layers | S | 1.2 |
| 1.4 ⭐ | Unit-cost precision: store layer total + qty, derive per-unit, last draw absorbs remainder. Test that a 7-unit layer at Rp 100.000 fully consumes to exactly Rp 100.000. | M | 1.2 |
| 1.5 | Migration 003: `purchase`, `purchase_line` | M | 0.4 |
| 1.6 ⭐ | **`faktur_received` on the purchase → layer cost basis** (INV-9, SPEC §3.2). Three cases: PKP+faktur, PKP+no faktur, non-PKP. | M | 1.5, 1.1 |
| 1.7 ⭐ | Test: same supplier price, faktur vs no faktur → **different layer costs**. This is the whole thesis; make it a named test. | S | 1.6 |
| 1.8 | Purchasing UI (admin/manager only, R10.4), with a prominent faktur-received toggle | L | 1.6, 0.10 |
| 1.9 | Purchase returns → reverse layer and any claimed input PPN (R12.2) | M | 1.6 |
| 1.10 | Stock opname: count, variance, adjustment posting with reason code (R12.4–5) | L | 1.1, 0.8 |
| 1.11 | Opening balances: stock, hutang, piutang at go-live (R11.5) | M | 1.1 |

---

## Phase 2 — Sales ⭐

| # | Task | Size | Deps |
|---|---|---|---|
| 2.1 | Migration 004: `sale`, `sale_line` | M | 1.1 |
| 2.2 ⭐ | Sale consumes FIFO layers atomically in one db transaction (SPEC §4) | M | 1.2, 2.1 |
| 2.3 | Cashier UI: product grid + categories (fine at 100–1,000 SKUs), cart, qty, discount, payment | L | 0.9, 2.2 |
| 2.4 | Barcode scanner input (HID wedge) | S | 2.3 |
| 2.5 | ESC/POS receipt + cash drawer, driven from the server | M | 2.2 |
| 2.6 | Cash session: open/close, opening float, Z-report | M | 2.2 |
| 2.7 | Non-cash payment methods: transfer, QRIS — recorded, not processed (R9.10) | S | 2.3 |
| 2.8 | Credit sales → piutang from the sales screen (R9.11) | M | 2.2 |
| 2.9 | Sales return / refund → restores stock, reverses margin (R12.1) | M | 2.2 |
| 2.10 | Void (same-day error) as distinct from return (R12.3) | S | 2.9 |
| 2.11 | **LAN test: unplug the internet**, complete a sale from a second device, receipt prints (INV-11) | S | 2.5 |

**Fixed after Phase 3 (D-013):** `Sales.Void` allowed voiding a sale whose goods
had already come back as a return. R12.3 separates a void from a return on
whether the goods ever left the shop, and a return is proof they did, so the
two can no longer both apply to one sale.

---

## Phase 3 — Owner margin ⭐⭐

The highest-stakes screen. Money moves on it.

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 3.1 ⭐ | `domain/margin`: `revenue − COGS` per owner over a period, from actual `stock_consumption` rows. Pure. | L | 1.2, 2.2 | ✅ |
| 3.2 ⭐ | Company bucket for `owner_id = null` stock (R2.2) | S | 3.1 | ✅ |
| 3.3 ⭐ | **Drill-down**: owner total → sales → the individual layers each consumed, with costs (SPEC §4.2). No summary-only views. | L | 3.1 | ✅ |
| 3.4 ⭐ | Returns-across-period rule — **ask the user first** (SPEC §4.4). Implement as configurable; show returns as a distinct line either way. | M | 2.9, 3.1 | ✅ decided: `RETURN_DATE`, switchable — D-012 |
| 3.5 ⭐ | Label it *Laporan Margin per Owner*. Never *Laba Rugi*. Add a note that shared costs are excluded. | S | 3.1 | ✅ |
| 3.6 ⭐ | Test: a sale never draws another owner's layers; margin attribution is exact across a full simulated month | M | 3.1 | ✅ |

**3.4 resolved (D-012):** a cross-boundary return reduces the month the goods
came back, so an already-settled month never moves under anyone. `SALE_DATE`
stays implemented and switchable per company, owner-only and audited. The month
of the original sale still reports what came back later, as context excluded
from every total — that section is the mitigation the choice required.

---

## Phase 4 — Inter-company ⭐

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 4.1 ⭐ | Migration 011: `transfer`, `transfer_line` | S | 1.1 | ✅ |
| 4.2 ⭐ | Atomic transfer: consume source, create destination layer, **owner carried across** (SPEC §3.4) | L | 1.2, 4.1 | ✅ |
| 4.3 ⭐ | Direction-dependent faktur/PPN handling on the new layer | M | 4.2, 1.6 | ✅ |
| 4.4 ⭐ | **Warn before commit** on non-PKP → PKP: input credit lost permanently (R4.5). Blocking confirmation, not a toast. | M | 4.3 | ✅ |
| 4.5 | Decide and implement: does a transfer create hutang/piutang between entities? **Open question — ask the user.** | M | 4.2 | ✅ decided: net position, no hutang — D-014 |
| 4.6 ⭐ | Test: transfer is atomic (both sides or neither) and preserves owner attribution | S | 4.2 | ✅ |

Migration numbering: SPEC and this table said "migration 005", written before
the earlier phases took that number. The transfer tables are migration **011**.

**4.4 is enforced by the server, not the screen.** `acknowledge_credit_loss` is
a precondition of the write: without it a non-PKP → PKP transfer is refused and
nothing moves. The modal exists so the person finds out in time to change their
mind, and it quotes the actual input PPN about to be destroyed — prorated from
the source layers' `ppn_paid_idr` — rather than a percentage of a guess. Stock
that never carried input PPN correctly reports zero forfeited.

**Found missing while building this phase, and fixed:** there was no route to
create a second company. `POST /setup/entity` refuses once one exists and told
the user to ask an owner to add another, which was a route that did not exist —
so every inter-company feature had nowhere to send anything. `POST /entities`
(owner only) now exists, with a stopgap form on the transfer screen's empty
state; it belongs on a proper master-data screen when there is one.

---

## Phase 5 — Tax

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 5.1 | Migration 012: `tax_rule`, effective-dated with `legal_ref` (INV-4) | M | 0.4 | ✅ |
| 5.2 | Seed Aug-2026 rules: PPN 12%×11/12 as an exact fraction | S | 5.1 | ✅ PPnBM deliberately not seeded — D-017 |
| 5.3 ⭐ | `domain/tax`: `Calculate(seller, cart, rules)`. Pure. Exclusive + inclusive; inclusive uses `tax = price − dpp`. | L | 0.2, 5.1 | ✅ |
| 5.4 ⭐ | PKP owes output PPN **even when no faktur was issued** (SPEC §2.3) | M | 5.3 | ✅ |
| 5.5 | Non-PKP entity: no PPN charged, no faktur, no input credit — enforced, not conventional | S | 5.3 | ✅ |
| 5.6 ⭐ | Worked-example tests to the rupiah. Where uncertain, leave a `TODO` with reasoning — **don't invent figures**. | L | 5.3 | ✅ 5 open questions listed |
| 5.7 ⭐ | Property test: `Σ dpp + Σ tax == grand_total`, zero drift | M | 5.6 | ✅ |
| 5.8 | Migration 013: `sale_tax` snapshot — rate + dpp fraction + legal_ref (INV-3) | M | 5.3, 2.1 | ✅ |
| 5.9 | PPN position report: output − creditable input, per masa pajak (SPEC §2.4) | M | 5.8, 1.6 | ✅ |
| 5.10 | Tax rule admin UI, effective-dated (new row, never UPDATE), `legal_ref` shown | M | 5.1, 0.10 | ✅ |
| 5.11 | Regression test: changing a rate alters no historical transaction | S | 5.10 | ✅ |

Migration numbering: SPEC §2.1 said "migration 006" and 5.8 said 007, both
written before the earlier phases took those numbers. The tax tables are **012**
and **013**.

**Nothing here has been checked against a DJP source.** The arithmetic is
verified to the rupiah against the rule as stated; the *rule* is not. Five open
questions are in `testdata/worked_examples/ppn_unverified.json`, and the
load-bearing one is whether the 11/12 DPP nilai lain is still in force on the
day of sale. Every one of them is answered by changing a `tax_rule` row, not by
changing code. `go test ./internal/domain/tax/ -run Unverified -v` prints the
list.

**5.3 found a bug in its own property test, and the guard is in the schema.**
Under inclusive pricing the tax is the remainder — `price − dpp` — so rounding
the DPP to a unit coarser than a rupiah can push it past the price and make the
tax negative. `Rule.Validate` and a CHECK in migration 012 both refuse it now.

**Turning tax on changed what the margin report means (D-015).** Revenue became
the DPP rather than the amount tendered, because COGS is already net of
creditable PPN and the two have to compare. Every figure in the Phase 3 tests
moved; they were re-derived by hand and the derivations are in the comments. The
report now carries `ppn_idr` and `tendered_idr` beside every owner line so the
gap between a margin figure and an invoice total is explicable on screen.

**5.4 and 5.5 are enforced in three places, not one.** `domain/tax` refuses to
price the sale, the service turns the refusal into something a cashier can act
on, and triggers in migrations 012 and 013 refuse the rows outright — so an
importer or a repair script cannot route around it either.

**A PKP company with no rule in force cannot ring a sale (D-016).** The till
stops with a message naming the date. Pricing it at zero would accrue the
liability silently and pay it out of margin at the filing.

**Known gap — the cashier screen cannot preview an exclusive-priced cart.**
`Kasir.tsx` totals the cart in the browser from the shelf prices. That is exact
under inclusive pricing, which is what is seeded, and short by the PPN under
exclusive — so the change owed would be wrong until the sale is saved and the
server's figure comes back. The stored sale is correct either way; only the
preview is not.

The proper fix is a server-side cart preview (`POST /sales/preview` running the
same `priceCart` → `priceTax` path without writing), which would also take the
last client-side money arithmetic out of the till (INV-1). It was not built here
because rebuilding the till's interaction model is Phase 2 work and the shipped
configuration does not need it. In the meantime the tax settings screen warns
when an exclusive rule is being created, at the moment the choice is made.

---

## Phase 6 — Reports

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 6.1 | Sales report (R5.4) | M | 2.2 | ✅ |
| 6.2 | Purchases report (R5.3) | M | 1.5 | ✅ |
| 6.3 | Product / stock report (R5.2) | M | 1.1 | ✅ |
| 6.4 | Hutang with **due dates and aging**, partial payments (R5.5, R5.8) | L | 1.5 | ✅ |
| 6.5 | Piutang, same (R5.6, R5.8) | L | 2.8 | ✅ |
| 6.6 | Raw data export, open format — also the exit route (R14.3, R5.7) | M | — | ✅ |

**6.6 was built first, and built to resist rot.** It is the one feature whose
value is realised only after the product has failed the customer, which makes it
the one most likely to stop being complete without anyone noticing. So the table
list is read from `sqlite_master` rather than maintained by hand: a table added
in a future migration is exported the moment it exists, and
`TestExportCoversEveryTableInTheSchema` reads the schema independently and fails
if the two ever disagree. The archive also re-counts every table against the
database before it is handed over, so a short export is an error rather than a
file somebody discovers is wrong a year later.

**The export is the whole database, and the gate is stricter for it (D-019).**
It cannot be scoped per company — products, owners, suppliers and customers are
shared by design (D-006) — so it requires owner in *every* active company rather
than in the selected one.

**6.4/6.5 put the ageing rules in `internal/domain/aging`, not in SQL.** What
counts as overdue is a rule with edges worth testing: due today is not late,
day 30 and day 31 are different buckets, and an invoice with no term recorded is
not aged at all (D-018). A `CASE` expression can express that; nothing can write
a test against it.

**The credit-terms question is still open and the report now says so.** Ageing
needs a term, REQUIREMENTS §11 has not answered whether piutang is net 30 or
case by case, and every undated document is counted on screen with what it is
worth. That count is the nudge; the fix is data entry, not code.

**6.1–6.3 aggregate in SQL, unlike the margin report.** `margin.sql` sums in Go
because family members settle money on those figures and every step has to be
testable. A period total on a sales report is a sum of stored figures that are
themselves already checked, and reimplementing `SUM` in Go would add a place for
the two to disagree rather than remove one. Where a rule is involved rather than
a sum — ageing, margin — it stays in a domain package.

**Sales figures follow D-015.** Revenue on these reports is the DPP and the PPN
is beside it, for the same reason as on the margin report: cost is already net
of creditable PPN, so revenue has to be. The sales report carries the same
"this is not laba rugi" caveat in its payload.

**Stock on hand is now, not as of the period end**, and the screen says so.
This system does not reconstruct historical balances, and a figure labelled
"stock on 31 August" that was actually today's would be worse than one honestly
labelled.

---

## Phase 7 — Omzet clock ⭐

Last by build order, not by importance. Rules are exact; get them right.

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 7.1 ⭐ | Migration 014: `omzet_ledger`, append-only | M | 2.1 | ✅ |
| 7.2 ⭐ | Book-year resolver: `book_year` + `business_date` in **entity timezone**, honouring `book_year_start_month` (INV-5) | M | 7.1 | ✅ |
| 7.3 ⭐ | TZ tests: 23:30 WIB 31 Dec lands in the closing book year; non-January start | S | 7.2 | ✅ |
| 7.4 ⭐ | Ledger row inside the sale's db transaction | S | 7.1, 2.2 | ✅ |
| 7.5 ⭐ | Void/refund → compensating row at the **original** sale's `effective_date` | M | 7.4, 2.9 | ✅ split: void yes, return no — D-020 |
| 7.6 ⭐ | Cross-year test: voiding a December sale in January decrements the prior book year | S | 7.5 | ✅ |
| 7.7 ⭐ | `domain/omzet`: book-year cumulative (authoritative) + trailing-12m (estimate). Pure. | M | 7.1 | ✅ |
| 7.8 ⭐ | State machine OK→WATCH→WARN→CROSSED; **sticky within a book year** | M | 7.7 | ✅ |
| 7.9 ⭐ | Deadline calculator: `crossed_on`, `register_by` (end of book year), `vat_starts` (first period of next). Cite PMK 164/2023. | M | 7.8 | ✅ both readings implemented — D-021 |
| 7.10 ⭐ | Dashboard banner; **both numbers labelled distinctly** — legal vs estimate | M | 7.9 | ✅ |
| 7.11 ⭐ | Simulation test: crossing in month 7 → correct state and both dates | M | 7.9 | ✅ |
| 7.12 | Compliance disclaimer component on every tax/threshold surface | S | 7.10 | ✅ |

Migration numbering: SPEC §5.1 and 7.1 said "migration 008", written before the
earlier phases took that number. The omzet tables are **014**.

**7.5 was split, and the split is the point (D-020).** A void carries the
original sale's date, as SPEC §5.4 says. A return does not: it carries the day
the goods came back, because the turnover *did* happen and is being reduced now
— the same distinction D-012 draws for margin. That difference is what makes
stickiness fall out of the arithmetic instead of being bolted on: a same-day
void can prevent a crossing, and a later return cannot undo one.

**7.9 implements both readings of the deadline (D-021).** SPEC §5.2 cites
PMK 164/2023, which governs the 0.5% final PPh regime, for a deadline about PPN
registration; the rule usually quoted for registration itself is PMK 197/2013
Pasal 4 and is months earlier on the same facts. Rather than pick one and
present it as settled, both are implemented and the choice is a column on
`omzet_threshold`. Four open questions are in
`testdata/worked_examples/omzet_unverified.json`; run
`go test ./internal/domain/omzet/ -run Unverified -v` for the list.

**The threshold is config, not a constant (INV-4).** `domain/omzet` has no
fallback figure and refuses to measure turnover against one nobody configured —
an omzet clock running on a hardcoded number is a figure the owner plans a year
around and cannot check.

**The alarm reads the day the clock is read on, not the whole year.** A clock
read on 30 June counts to 30 June. Summing the full book year regardless would
report a business as crossed months before it was — a registration deadline
arriving early, with a year of planning built on it.

**Phase 7 found an int64 overflow in `money.Allocate`.** `total × weight`
overflowed at about 2.4e19 against a ceiling of 9.2e18, which a single invoice
near the Rp 4.8B threshold reaches — the product wrapped negative and the
allocation panicked. The multiplication now goes through `decimal.QuoRem`, which
is exact; the arithmetic is unchanged and only its range has moved. Regression
tests are in `internal/domain/money`. Worth noting that this was reachable from
the cashier screen by any invoice-level discount on a large sale, not only from
the omzet code that surfaced it.

---

## Phase 8 — Operations

| # | Task | Size | Deps | Status |
|---|---|---|---|---|
| 8.1 | Scheduled backup to removable/network storage (R14.1–2) | M | 0.3 | ✅ |
| 8.2 | **Restore drill** — documented and actually executed onto a spare laptop (R8.7) | M | 8.1 | ⚠️ executed in isolation; **the spare-laptop run is still owed** |
| 8.3 | Static IP / mDNS so the server stays findable (R8.6) | S | 0.5 | ✅ hostname, not a bundled responder — D-022 |
| 8.4 | Olsera migration importer — **investigate their export format first** | L | 1.11 | 🔎 investigated; **not built** — decision with the user, D-023 |
| 8.5 | Go-live stock opname (R12.4) | M | 1.10 | ✅ `docs/GO-LIVE.md` |

**8.1 verifies, it does not just write.** A snapshot is taken with `VACUUM INTO`
— copying the file would capture the main database without its write-ahead log,
which is the last few minutes of sales missing from a file that looks perfectly
valid. Every snapshot is then reopened **read-only**, integrity-checked, and its
schema version read back before it is called a backup. A backup nobody has
opened is a rumour.

Verifying read-only is not a detail: opening through `store.Open` applies
`journal_mode(WAL)`, which *writes to the file* — the first version of this
invalidated its own checksum, and would have invalidated the file on every
subsequent verification.

**A backup on the same disk is refused** (R14.2), by comparing device numbers,
not by trusting a folder called "Backup". `TERA_BACKUP_ALLOW_SAME_DISK=1` is an
explicit opt-in that warns at every start — refusing outright would leave a
single-disk machine with no backup at all, which is worse than a weak one.

**8.2 was executed, and it is honest about what it executed.** The drill seeds a
shop that buys 100 boxes and sells 3, backs it up, restores into a directory
that has never held a database, starts the server, logs in with the same
credentials, and compares the figures:

```
sumber:        penjualan total=333000, sisa stok=97
hasil restore: penjualan total=333000, sisa stok=97
```

The first version of the drill restored **zero rows** and passed — proving the
file copy and nothing else. That is exactly the failure the requirement is about
one level up, and it is why the drill now trades first. The drill also caught a
bug in itself: a relative binary path that broke the moment it `cd`'d into the
clean directory, which is precisely what would happen on a laptop with the
binary on a stick.

**What was not done: the spare laptop.** The run was isolated on this machine —
a directory with nothing in it but the binary and the backup. That proves the
tooling, the procedure and the data. It does not prove different hardware, a
different OS install, or a different user account, which is what R8.7 actually
promises. `docs/RESTORE-DRILL.md` carries the log table with that caveat written
into it, and the whole procedure is one command on the laptop.

**8.5 is a procedure, not code** — the opname machinery has existed since 1.10.
The order in `docs/GO-LIVE.md` matters: count before entering anything, because
their current quantities are known to be drifting (REQUIREMENTS §2), and
importing the drift would build FIFO layers on it.

---

## Cut lines

- **Demo-able:** end of Phase 2
- **Earns the switch:** end of Phase 4 — margin per owner and working inter-company stock are why they'd leave Olsera
- **Complete v1:** end of Phase 8

Under pressure, cut Phase 6 breadth and Phase 7 polish. Never cut the ⭐ tests: 1.7, 3.6, 4.6, 5.6, 5.7, 7.3, 7.6, 7.11. A margin engine without tests is a family argument waiting to happen.
