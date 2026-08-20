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

---

## Phase 3 — Owner margin ⭐⭐

The highest-stakes screen. Money moves on it.

| # | Task | Size | Deps |
|---|---|---|---|
| 3.1 ⭐ | `domain/margin`: `revenue − COGS` per owner over a period, from actual `stock_consumption` rows. Pure. | L | 1.2, 2.2 |
| 3.2 ⭐ | Company bucket for `owner_id = null` stock (R2.2) | S | 3.1 |
| 3.3 ⭐ | **Drill-down**: owner total → sales → the individual layers each consumed, with costs (SPEC §4.2). No summary-only views. | L | 3.1 |
| 3.4 ⭐ | Returns-across-period rule — **ask the user first** (SPEC §4.4). Implement as configurable; show returns as a distinct line either way. | M | 2.9, 3.1 |
| 3.5 ⭐ | Label it *Laporan Margin per Owner*. Never *Laba Rugi*. Add a note that shared costs are excluded. | S | 3.1 |
| 3.6 ⭐ | Test: a sale never draws another owner's layers; margin attribution is exact across a full simulated month | M | 3.1 |

---

## Phase 4 — Inter-company ⭐

| # | Task | Size | Deps |
|---|---|---|---|
| 4.1 ⭐ | Migration 005: `transfer` | S | 1.1 |
| 4.2 ⭐ | Atomic transfer: consume source, create destination layer, **owner carried across** (SPEC §3.4) | L | 1.2, 4.1 |
| 4.3 ⭐ | Direction-dependent faktur/PPN handling on the new layer | M | 4.2, 1.6 |
| 4.4 ⭐ | **Warn before commit** on non-PKP → PKP: input credit lost permanently (R4.5). Blocking confirmation, not a toast. | M | 4.3 |
| 4.5 | Decide and implement: does a transfer create hutang/piutang between entities? **Open question — ask the user.** | M | 4.2 |
| 4.6 ⭐ | Test: transfer is atomic (both sides or neither) and preserves owner attribution | S | 4.2 |

---

## Phase 5 — Tax

| # | Task | Size | Deps |
|---|---|---|---|
| 5.1 | Migration 006: `tax_rule`, effective-dated with `legal_ref` (INV-4) | M | 0.4 |
| 5.2 | Seed Aug-2026 rules: PPN 12%×11/12 as an exact fraction, PPnBM 12% | S | 5.1 |
| 5.3 ⭐ | `domain/tax`: `Calculate(cart, rules, is_pkp)`. Pure. Exclusive + inclusive; inclusive uses `tax = price − dpp`. | L | 0.2, 5.1 |
| 5.4 ⭐ | PKP owes output PPN **even when no faktur was issued** (SPEC §2.3) | M | 5.3 |
| 5.5 | Non-PKP entity: no PPN charged, no faktur, no input credit — enforced, not conventional | S | 5.3 |
| 5.6 ⭐ | Worked-example tests to the rupiah. Where uncertain, leave a `TODO` with reasoning — **don't invent figures**. | L | 5.3 |
| 5.7 ⭐ | Property test: `Σ dpp + Σ tax == grand_total`, zero drift | M | 5.6 |
| 5.8 | Migration 007: `sale_tax` snapshot — rate + dpp fraction + legal_ref (INV-3) | M | 5.3, 2.1 |
| 5.9 | PPN position report: output − creditable input, per masa pajak (SPEC §2.4) | M | 5.8, 1.6 |
| 5.10 | Tax rule admin UI, effective-dated (new row, never UPDATE), `legal_ref` shown | M | 5.1, 0.10 |
| 5.11 | Regression test: changing a rate alters no historical transaction | S | 5.10 |

---

## Phase 6 — Reports

| # | Task | Size | Deps |
|---|---|---|---|
| 6.1 | Sales report (R5.4) | M | 2.2 |
| 6.2 | Purchases report (R5.3) | M | 1.5 |
| 6.3 | Product / stock report (R5.2) | M | 1.1 |
| 6.4 | Hutang with **due dates and aging**, partial payments (R5.5, R5.8) | L | 1.5 |
| 6.5 | Piutang, same (R5.6, R5.8) | L | 2.8 |
| 6.6 | Raw data export, open format — also the exit route (R14.3, R5.7) | M | — |

---

## Phase 7 — Omzet clock ⭐

Last by build order, not by importance. Rules are exact; get them right.

| # | Task | Size | Deps |
|---|---|---|---|
| 7.1 ⭐ | Migration 008: `omzet_ledger`, append-only | M | 2.1 |
| 7.2 ⭐ | Book-year resolver: `book_year` + `business_date` in **entity timezone**, honouring `book_year_start_month` (INV-5) | M | 7.1 |
| 7.3 ⭐ | TZ tests: 23:30 WIB 31 Dec lands in the closing book year; non-January start | S | 7.2 |
| 7.4 ⭐ | Ledger row inside the sale's db transaction | S | 7.1, 2.2 |
| 7.5 ⭐ | Void/refund → compensating row at the **original** sale's `effective_date` | M | 7.4, 2.9 |
| 7.6 ⭐ | Cross-year test: voiding a December sale in January decrements the prior book year | S | 7.5 |
| 7.7 ⭐ | `domain/omzet`: book-year cumulative (authoritative) + trailing-12m (estimate). Pure. | M | 7.1 |
| 7.8 ⭐ | State machine OK→WATCH→WARN→CROSSED; **sticky within a book year** | M | 7.7 |
| 7.9 ⭐ | Deadline calculator: `crossed_on`, `register_by` (end of book year), `vat_starts` (first period of next). Cite PMK 164/2023. | M | 7.8 |
| 7.10 ⭐ | Dashboard banner; **both numbers labelled distinctly** — legal vs estimate | M | 7.9 |
| 7.11 ⭐ | Simulation test: crossing in month 7 → correct state and both dates | M | 7.9 |
| 7.12 | Compliance disclaimer component on every tax/threshold surface | S | 7.10 |

---

## Phase 8 — Operations

| # | Task | Size | Deps |
|---|---|---|---|
| 8.1 | Scheduled backup to removable/network storage (R14.1–2) | M | 0.3 |
| 8.2 | **Restore drill** — documented and actually executed onto a spare laptop (R8.7) | M | 8.1 |
| 8.3 | Static IP / mDNS so the server stays findable (R8.6) | S | 0.5 |
| 8.4 | Olsera migration importer — **investigate their export format first** | L | 1.11 |
| 8.5 | Go-live stock opname (R12.4) | M | 1.10 |

---

## Cut lines

- **Demo-able:** end of Phase 2
- **Earns the switch:** end of Phase 4 — margin per owner and working inter-company stock are why they'd leave Olsera
- **Complete v1:** end of Phase 8

Under pressure, cut Phase 6 breadth and Phase 7 polish. Never cut the ⭐ tests: 1.7, 3.6, 4.6, 5.6, 5.7, 7.3, 7.6, 7.11. A margin engine without tests is a family argument waiting to happen.
