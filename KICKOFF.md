# Claude Code kickoff — Tera

## Setup

```bash
bash setup-tera.sh          # → ./tera
cd tera && claude
```

`CLAUDE.md` is read automatically each session, so these prompts stay short — they point at the docs rather than restating them.

**Give Phases 1, 3, 4, and 5 their own sessions with clean context.** They're the parts that carry real risk, and a session already full of scaffolding details will do worse work on them.

---

## Prompt 1 — Bootstrap

> Read `CLAUDE.md`, `docs/REQUIREMENTS.md`, `docs/SPEC.md`, `docs/ARCHITECTURE.md`, and `docs/TASKS.md` in full before writing anything.
>
> Then scaffold the repo per ARCHITECTURE §6: Go module, golangci-lint, Makefile (`build`/`test`/`lint`/`run`), GitHub Actions running all three, goose and sqlc config, the directory tree with `doc.go` stubs so the layering is visible, and a `.gitignore`.
>
> No domain logic yet. When done, show me the tree and tell me what you'd want clarified before Phase 0.

---

## Prompt 2 — Phase 0, foundations

> Implement Phase 0 from `docs/TASKS.md`, one task per commit, in dependency order.
>
> Check in with me after 0.8 (audit log) before starting the UI work.
>
> Two things I'll hold you to: `int64` rupiah with no float anywhere (INV-1), and the server binding `0.0.0.0` not localhost — clients are browsers on the shop LAN, not this machine.
>
> UI copy is in Bahasa Indonesia from the start. Don't build it in English and translate later.

---

## Prompt 3 — Phase 1, purchasing and FIFO ⭐

Own session.

> Read `docs/SPEC.md` §1 and §3 carefully, then implement Phase 1 from `docs/TASKS.md`.
>
> Build `internal/domain/fifo` first and in isolation — pure functions over value types, no database or HTTP imports — before any migration or handler. I want to review `Consume()` and its tests before it's wired to anything.
>
> The things I care about:
>
> - **Layers and consumption are both append-only** (INV-7). Never decrement a layer in place. Remaining quantity is derived. This is what makes owner settlement auditable months later.
> - **Consumption is owner-scoped** (INV-8). A sale of Budi's product draws only from Budi's layers. If there isn't enough, that's an error surfaced to the user — never a silent fall back to another owner's stock. Real money is settled on these figures between family members.
> - **`faktur_received` determines the layer's cost basis.** Three cases in SPEC §3.2: PKP with faktur (cost is net of the creditable PPN), PKP without (the PPN is real cost), non-PKP (always cost). Same supplier price, three different bases.
> - Store layer total and quantity; derive unit cost. A 7-unit layer at Rp 100.000 must consume to exactly Rp 100.000 with no rupiah lost.
>
> Task 1.7 is the thesis of the whole product — make it a named, obvious test.

---

## Prompt 4 — Phase 2, sales

> Implement Phase 2 from `docs/TASKS.md`.
>
> The sale must consume FIFO layers and write everything in a single database transaction — sale, lines, consumptions. A partial commit corrupts stock and margin simultaneously.
>
> Task 2.11 is a real test, not a checkbox: unplug the internet, ring a sale from a second device on the LAN, confirm the receipt prints.

---

## Prompt 5 — Phase 3, owner margin ⭐⭐

Own session. This is the highest-stakes screen in the product.

> Read `docs/SPEC.md` §4 and REQUIREMENTS §5, then implement Phase 3.
>
> Context that should shape every decision here: this is a family business, and **money is settled between family members monthly based on these numbers**. If a figure is wrong, people who eat dinner together argue about it. Build it like that's true, because it is.
>
> - Build `internal/domain/margin` as pure functions over consumption records first, fully tested, before any UI.
> - COGS comes from the **actual `stock_consumption` rows** — the specific layers drawn — never an average.
> - **Drill-down is a hard requirement** (SPEC §4.2). Every figure expands to the sales behind it, and every sale to the layers it consumed with their costs. No summary-only views. When someone asks "why is mine lower this month," the answer has to be on screen.
> - Label it *Laporan Margin per Owner*, never *Laba Rugi*. It is gross margin — shared costs are out of scope and it hasn't paid rent. Put that note on the report.
>
> **Task 3.4 is blocked on a decision I haven't made** — what happens to a November return of an October sale, when October's money was already split. Implement it as configurable, show returns as a distinct line, and flag it to me rather than silently picking one.

---

## Prompt 6 — Phase 4, inter-company ⭐

> Read `docs/SPEC.md` §3.4 and `docs/ARCHITECTURE.md` §5, then implement Phase 4.
>
> This is the flow Olsera gets wrong — it records the transaction without moving stock, which is why the user's quantities are drifting from reality today. Source consumption and destination layer commit in one transaction or neither happens.
>
> Owner attribution carries across the entity boundary. The family member owning the goods doesn't change because the goods crossed a company line.
>
> **Task 4.4 must be a blocking confirmation, not a toast.** Moving stock from the non-PKP entity into the PKP entity destroys the input PPN credit permanently — the chain can't be reconnected afterwards, and it costs them ~11% of margin on that stock. The warning has to land *before* the commit, because afterwards there's nothing to act on.
>
> Task 4.5 is an open question — ask me before implementing, don't assume.

---

## Prompt 7 — Phase 5, tax ⭐

Own session.

> Read `docs/SPEC.md` §2, then implement Phase 5.
>
> `internal/domain/tax` first, in isolation, before migrations or handlers.
>
> - The DPP nilai lain is an **exact fraction** (`11/12`), not a decimal. No `0.916666...` anywhere in the codebase.
> - Inclusive pricing: `dpp = round(price / (1 + effective_rate))`, then `tax = price − dpp`. Subtract. Never recompute the tax independently and add — a one-rupiah gap between the shelf price and the sum of its parts is what makes a cashier stop trusting the till.
> - **A PKP owes output PPN whether or not the buyer took a faktur** (5.4). A walk-in sale with no faktur still accrues the liability. This is the most common way a newly-PKP business loses margin without noticing.
> - The non-PKP entity charges no PPN, issues no faktur, credits nothing. Enforce it — don't leave it as a convention someone can bypass.
> - Cite the regulation in a comment wherever a legal rule is encoded.
>
> For 5.6: write worked examples as table-driven fixtures in `testdata/worked_examples/`. **Where you're not certain of the expected rupiah figure, say so and leave a `TODO` with your reasoning rather than inventing a number I might trust.** I'll verify those against DJP sources and a tax consultant.

---

## Prompt 8 — Phase 6, reports

> Implement Phase 6 from `docs/TASKS.md`.
>
> Hutang and piutang need due dates, aging, and partial payments (R5.8) — a balance without an age isn't actionable.
>
> R5.7 says reports can start simple *provided raw data can be pulled*. Prioritise 6.6 (export) accordingly; it's also the user's exit route if they ever leave, and I'd rather that be honest than sticky.

---

## Prompt 9 — Phase 7, omzet clock ⭐

> Read `docs/SPEC.md` §5, then implement Phase 7.
>
> The thing most guidance online gets wrong: the Rp 4.8B threshold is **book-year cumulative, reset annually** — not a rolling 12 months. Build the book-year counter as authoritative. The trailing-12-month figure is a momentum estimate only, and the UI must label the two distinctly so nobody mistakes the estimate for the legal number.
>
> On crossing, emit **both** dates: registration due by the end of the current book year, and VAT obligation beginning the first tax period of the *following* book year. That gap is the most misunderstood part of the rule and most of this feature's value.
>
> Build `internal/domain/omzet` as pure functions over a ledger slice, with the state machine and deadline calculator fully tested, before wiring persistence.
>
> Must have tests: 23:30 WIB on 31 December; a non-January book-year start; voiding a December sale in January (decrements the prior year); crossing being sticky even if a later refund drops the cumulative back under.

---

## Prompt 10 — Phase 8, operations

> Implement Phase 8.
>
> 8.2 is not documentation — **actually restore a backup onto a different machine and confirm it works.** One machine holds the numbers this family settles money on. An untested backup is a rumour.
>
> Before 8.4, investigate what Olsera's export actually contains and tell me. It constrains what migration is possible, and I'd rather know now than after building an importer for a format that doesn't exist.

---

## Useful mid-session prompts

**Before a risky change**
> Before writing code: what's your plan, what could break, and which invariant in `CLAUDE.md` is most at risk?

**When it drifts**
> Re-read `CLAUDE.md`. Which invariant did this violate, and what's the smallest fix?

**On FIFO or margin**
> Walk me through a sale of 5 units where the owner has a 3-unit layer at one cost and a 10-unit layer at another. Show every intermediate value, which layers were drawn, and the exact rupiah COGS.

**On tax**
> Walk me through a Rp 100.000 inclusive-price item at PPN 12%×11/12, showing every intermediate and exactly where rounding happens.

**Reality check**
> Which parts of this did you verify with a passing test, and which are you asserting from pattern-matching? Be specific.

---

## Open questions to resolve with the user

These are flagged in the docs and will block or reshape work when reached:

| Question | Blocks |
|---|---|
| Returns across a settlement boundary (SPEC §4.4) | Task 3.4 |
| Does an inter-company transfer create hutang/piutang between entities? | Task 4.5 |
| Can staff see other owners' margin figures? (R13.3) | Phase 3 UI |
| Credit terms on piutang — net 30, case by case? | Task 6.5 |
| Which FIFO layer does a sales return restore to? (R12.1) | Task 2.9 |
| What does Olsera's export contain? | Task 8.4 |
| Do they need faktur *generated*, or is recording it enough? | Scope of Phase 5 — if generated, e-Faktur comes back in |

---

## On the legal figures

Rates, thresholds, and deadlines here were researched as of August 2026, and Indonesian tax law has moved fast — PP 20/2026 rewrote the PPh Final 0.5% regime in April, and the PPN 12%×11/12 arrangement is barely eighteen months old. Everything is effective-dated config precisely because of that (INV-4).

Before this touches real books, have a konsultan pajak verify the seeded `tax_rule` rows and the omzet base definition (gross vs net of VAT). The `legal_ref` column exists so you can hand them the config and have them check it line by line.
