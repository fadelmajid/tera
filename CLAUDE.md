# CLAUDE.md — Tera

Read this first, every session. Then `docs/REQUIREMENTS.md`, then `docs/SPEC.md` and `docs/ARCHITECTURE.md`.

## What this is

**Tera** — a trading and inventory system for a small Indonesian medical-supplies (alat kesehatan) family business. It includes a POS; it is not a POS.

Built for one real user, replacing Olsera. Their pain, ranked, from the interview:

1. **Margin per owner** — family members own different product lines and **settle money monthly** on this figure. They currently fake it with product categories.
2. **Inter-company stock movement** — Olsera records the transaction but doesn't move stock. Their quantities are drifting from reality right now.
3. **Purchase-side PPN** — whether a supplier gave a faktur changes true cost by 11%. Invisible to them today.
4. Reports (margin, product, purchases, sales, hutang, piutang)
5. Omzet clock against the Rp 4.8B PKP threshold

Note item 1 is the highest-stakes screen in the product. If it's wrong, a family argues about money. Treat it accordingly.

## Scale — this is small

| | |
|---|---|
| SKUs | 100–1,000 |
| Transactions/day | < 1,000 |
| Users | < 10 |
| Entities | 2 (one PKP, one non-PKP) |

Do not build for scale this business doesn't have. No microservices, no message queue, no distributed anything.

## Stack

| Layer | Choice | Why |
|---|---|---|
| Backend | Go 1.23+, single binary | Zero-dependency deploy; owner's primary stack |
| DB | SQLite (WAL), `modernc.org/sqlite` | Serverless, file-copy backup. Comfortable at this scale. |
| Frontend | TypeScript + React + Vite | Built to static assets, **embedded into the binary** via `embed.FS` |
| Money | `int64` rupiah + `shopspring/decimal` for intermediates | Never float |
| Hardware | ESC/POS over TCP:9100 or USB, from Go | Printer + drawer, attached to the server machine |

**Deployment: LAN server.** One machine holds the data and serves the others over the shop's own network. Clients are browsers. Bind `0.0.0.0:8080`, not localhost. "Offline" means no internet, not no network — their router works fine.

Install is: copy one binary, run it, everyone opens a URL.

## Non-negotiable invariants

Correctness properties. Write tests asserting them.

- **INV-1** Money is never a float. Not in Go, not in TS, not in JSON.
- **INV-2** A finalised transaction is immutable. Corrections are compensating records, never mutations.
- **INV-3** Tax is snapshotted onto the transaction at sale time. Never recompute historical tax from current config.
- **INV-4** No tax rate, threshold, or deadline hardcoded. All in effective-dated config rows. Indonesian tax law changed three times in 18 months.
- **INV-5** Book-year and business-day boundaries computed in entity-local time (WIB), never UTC.
- **INV-6** Every mutating API call carries a `client_request_id` (UUID) and is idempotent on it.
- **INV-7** **FIFO layers are append-only.** Consumption is recorded as a separate event referencing the layer. Never decrement a layer in place — you lose the audit trail that owner settlement depends on.
- **INV-8** **Every stock movement has exactly one owner attribution** (possibly the company bucket). A movement with ambiguous ownership is a bug, not a default.
- **INV-9** A FIFO layer records whether a **faktur pajak was received**. Without it, the layer's true cost is wrong by ~11% and every margin downstream is wrong.
- **INV-10** Every edit to historical data is logged: who, when, before, after.
- **INV-11** The system must function fully with no internet, on the shop LAN alone.

## Working agreements

- Go standard layout, `internal/` for everything. Table-driven tests. `golangci-lint` clean.
- No ORM. `sqlc` over hand-written SQL. `goose` migrations, forward-only, checked in.
- Domain packages (`internal/domain/...`) import **no** database or HTTP packages. Pure functions over value types. That's where the tests live.
- Conventional commits, one logical change each.
- When a legal rule is encoded, cite the regulation in a comment: `// PMK 164/2023 Pasal 18`.
- UI in **Bahasa Indonesia**. Code and comments in English.

## Domain vocabulary

Use these terms; don't translate them into generic accounting words.

| Term | Meaning |
|---|---|
| PKP | VAT-registered business. Must charge PPN, can credit input PPN. |
| PPN | Indonesian VAT. Effective 11% (statutory 12% on a DPP of 11/12). |
| PPN Masukan | Input VAT, paid on purchases. Creditable **only with a faktur**. |
| PPN Keluaran | Output VAT, collected on sales. |
| Faktur pajak | The tax invoice. The paper that makes input VAT creditable. |
| Omzet | Gross turnover (peredaran bruto). |
| Hutang / Piutang | Payable / receivable. |
| Owner | A family member products are attributed to. **Attribution tag, not legal ownership.** |

## Out of scope — do not build

Rejected by the user or deferred. See REQUIREMENTS §8.

- Marketing, staff management, general expense/operational-cost ledger
- **Net profit / P&L.** Scope is **gross margin per owner** only. Shared costs (listrik, gaji, sewa) are handled outside the app. Label the report *Laporan Margin per Owner*, never *Laba Rugi*.
- Consignment or legal ownership modelling
- Markup on inter-company transfers
- FEFO / batch-expiry picking (leave a nullable expiry field; don't build the logic)
- Shared-cost allocation between owners
- e-Faktur / Coretax API integration — capture the fields, ship no integration
- Cloud sync, multi-outlet, payment gateway processing
- PB1 / PBJT restaurant tax — not applicable to medical supplies

## Guardrails

**Multi-entity.** Two entities under one family, one PKP and one not, transferring at cost. Legitimate, and they asked for it. But:

- Never write copy or name a feature in a way that frames multi-entity as a way to manage the Rp 4.8B threshold.
- If asked to route a transaction to an entity *based on whether the buyer wants a faktur*, push back and flag it.
- The transfer screen must warn that moving stock into the PKP entity destroys input PPN credit permanently (R4.5).

**Compliance surfaces.** Every screen showing a tax figure, threshold, or deadline carries: *"Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda."* We are not tax advisors.

**Owner margin.** Money moves on this number. Every figure drills down to the transactions behind it. No exceptions, no summary-only views.
