# REQUIREMENTS — Tera (working name)

Consolidated from user interview. **Requirements only — no design decisions.**
Status: draft · supersedes the sales-only scope in the earlier `docs/SPEC.md`

---

## 1. Context

| | |
|---|---|
| Industry | Alat kesehatan (medical supplies) — trading/distribution |
| Structure | Family business, 2 legal entities |
| Tax status | **Entity A: PKP. Entity B: non-PKP.** |
| Ownership | Multiple family members ("owners") within a company, each with their own product lines |
| Current tool | **Olsera** — actively used, 1 account covering 2 companies |
| Users | < 10 — owners, manager(s), regular staff |
| SKUs | 100 – 1,000 |
| Volume | < 1,000 transactions/day |
| Purchase data entry | Admin / manager |

Small scale. The complexity is in the domain, not the load.

**What this system is.** A trading and inventory system with multi-entity tax awareness and per-owner margin settlement, which **includes** a POS. The cashier screen is one input surface among two (the other being purchasing), not the product.

This distinction matters commercially: framed as a POS, it gets evaluated against Olsera's cashier screen, which has years of polish. Framed correctly, the comparison happens on owner splits, working inter-company stock, and true cost after faktur — where the gaps actually are.

Working name: **Tera** — from the calibration stamp certifying that a scale measures true. Not final; pending PDKI trademark search and domain check.

---

## 2. Why they'd switch

They already have a working POS. This is a replacement, not a greenfield need — so the gaps *are* the product.

| Capability | Olsera | Gap |
|---|---|---|
| POS / cashier | ✅ | — |
| Basic reports | ✅ | — |
| Inter-company transaction | ⚠️ records it, **stock does not move** | data integrity bug |
| Multi-owner margin split | ❌ | **faked using product categories** |
| Accounting-style reports | ❌ | primary ask |
| Tax features | ❌ | — |

**Read this table as "where we differentiate," not "what we build."** POS and basic reports still have to be built — they're table stakes. The gaps are why they'd switch.

**Stated frustrations:** monthly subscription (prefers one-time payment); too many unused features (marketing, staff management, operational-cost tracking); missing the features above.

**The strongest signal:** they are bending the product *category* field into an ownership dimension because there's no proper slot for it. It's load-bearing enough to justify corrupting their own data model — and it costs them the category field's actual purpose.

---

## 3. Functional requirements

> **Ordering note.** R1–R8 were derived from the *gaps* against Olsera. R9–R14 cover the table stakes: sales entry, purchasing, master data, returns and stock opname, users and access, and backup. These must be built regardless of whether Olsera already has them, and several never came up in the interview because nobody thinks to ask for them. They are foundational despite appearing last — nothing above works without them.

### R1 — Multi-company

| ID | Requirement |
|---|---|
| R1.1 | One user account can access multiple companies. |
| R1.2 | Each company is an independent legal/tax entity with its own configuration. |
| R1.3 | The two companies have **different PKP status** (one PKP, one not) and must behave differently on tax. |

### R2 — Multi-owner within a company

| ID | Requirement |
|---|---|
| R2.1 | Each product is attributed to an owner (family member). |
| R2.2 | The owner attribute is **optional** — some stock is company-owned and reports into a separate bucket. |
| R2.3 | Margin is sliced per owner from product attribution. |
| R2.4 | **Money is settled between owners based on this figure** — see §5, this is the highest-stakes output in the system. |

### R3 — Inventory

| ID | Requirement |
|---|---|
| R3.1 | **FIFO** costing. (User chose FIFO over FEFO explicitly.) |
| R3.2 | FIFO cost layers drive COGS, margin, and the ledger. |
| R3.3 | Stock moves correctly on inter-company transactions — the thing Olsera fails at. |
| R3.4 | Cost layers must record whether a **faktur pajak was received** from the supplier — see §4, this changes the true cost of the layer. |

### R4 — Inter-company transactions

| ID | Requirement |
|---|---|
| R4.1 | Company A can transact with Company B under the same user account. |
| R4.2 | Stock decrements in the source company and increments in the destination. |
| R4.3 | Transfer price is **at cost** — no markup. |
| R4.4 | See §6 — "at cost" between a PKP and a non-PKP entity has tax consequences the user may not have considered. |
| R4.5 | Transfers **into the PKP entity from the non-PKP entity** must warn that input PPN credit is permanently lost on that stock. See §6.2. |

### R5 — Reports

| ID | Requirement |
|---|---|
| R5.1 | **Margin per owner** — revenue − COGS, grouped by owner. Not net profit (see §5). |
| R5.2 | Product report |
| R5.3 | Purchases (pembelian) |
| R5.4 | Sales (penjualan) |
| R5.5 | Hutang (accounts payable) |
| R5.6 | Piutang (accounts receivable) |
| R5.7 | Accounting reports can start simple **provided raw data can be pulled** — user's words. Extraction fidelity matters more than report polish in v1. |
| R5.8 | Hutang and piutang require **due dates and aging** to be useful, and the ability to record **partial payments** against an invoice. A balance without an age is not actionable. |

### R6 — Tax

| ID | Requirement |
|---|---|
| R6.1 | PPN handling appropriate to each entity's PKP status. |
| R6.2 | Track PPN Masukan (input, on purchases) vs PPN Keluaran (output, on sales) — see §4. |
| R6.3 | Faktur received / faktur issued status per transaction. |
| R6.4 | Omzet tracking against the Rp 4.8 billion PKP threshold, book-year cumulative. |
| R6.5 | Rates and thresholds must be **configurable, never hardcoded** — Indonesian tax law changed three times in the last 18 months. |
| R6.6 | The omzet clock (R6.4) matters most for the **non-PKP entity**, which is the one that can still cross the threshold. Track both entities regardless. |
| R6.7 | **PB1 / PBJT is not applicable** to this user — that is a restaurant tax and this is medical supplies trading. The earlier `SPEC.md` models it; carry the capability if cheap, but it is not a requirement here. |

### R7 — Audit

| ID | Requirement |
|---|---|
| R7.1 | Edits to past transactions are permitted for owners and managers; **regular staff excluded**. |
| R7.2 | **All edits logged** — who, when, before/after. User accepted this. |
| R7.3 | Period locking **not required** by the user. Logged as a known risk in §7. |

### R8 — Deployment

| ID | Requirement |
|---|---|
| R8.1 | Runs on a local machine. Offline-tolerant. |
| R8.2 | **One-time payment** preferred over subscription. See §7 for the tension this creates with R6.5. |
| R8.3 | Migration from Olsera — they have live stock and history. |
| R8.4 | **Decided: LAN server model.** One machine holds the data and serves the others over the shop network — see §10. |
| R8.5 | Clients are browsers on the local network. No per-device database, no sync layer. |
| R8.6 | The server must be reachable by a stable address (static IP or mDNS). DHCP reassignment must not break access. |
| R8.7 | Server hardware failure is the accepted single point of failure, mitigated by R14 backups and a documented restore-onto-any-laptop procedure. |

### R9 — Sales entry (POS)

The cashier screen. Olsera already does this well, so it is not a differentiator — but it is the primary input surface and every figure downstream depends on it.

| ID | Requirement |
|---|---|
| R9.1 | Cashier flow: search/select product, cart, quantity, discount, payment, receipt. |
| R9.2 | Product selection suited to 100–1,000 SKUs — grid with categories is sufficient at this scale. |
| R9.3 | Barcode scanner input (HID keyboard wedge). |
| R9.4 | Thermal receipt printing and cash drawer. |
| R9.5 | Works fully offline (R8.1). |
| R9.6 | Records **whether a faktur was issued** to the buyer — drives R6.2 and the §4 margin calculation. |
| R9.7 | Records the selling entity, so the sale routes to the correct company's tax treatment and omzet counter. |
| R9.8 | Cash session: open/close, opening float, end-of-day reconciliation. |
| R9.9 | Sale consumes FIFO layers (R3.1), which determines COGS and therefore which owner's margin it lands in. |
| R9.10 | Non-cash payment methods — bank transfer and QRIS at minimum. Recording the method, not processing it (R8 out-of-scope note). |
| R9.11 | Credit sales (piutang) can be raised from the sales screen, not only from a separate invoice flow. |
| R9.12 | **UI in Bahasa Indonesia.** Never stated in the interview but not optional for staff usage. Confirm. |

### R10 — Purchasing

Not present in the original scope. Required by §4 — margin cannot be computed without it.

| ID | Requirement |
|---|---|
| R10.1 | Record purchases from suppliers: supplier, items, quantities, prices. |
| R10.2 | Record **whether a faktur pajak was received** (R3.4). This is the field that determines true cost. |
| R10.3 | Creates FIFO cost layers with the correct net-of-creditable-PPN cost. |
| R10.4 | Entered by admin/manager, not cashier staff. |
| R10.5 | Records the purchasing entity — determines whether input PPN is creditable at all (§6.2). |
| R10.6 | Supports comparing suppliers on **true cost after faktur status**, not headline price. |
| R10.7 | Feeds hutang (R5.5). |

### R11 — Master data

Never surfaced in the interview because it's assumed. It isn't optional.

| ID | Requirement |
|---|---|
| R11.1 | Product catalog CRUD: code, name, unit, selling price, category, **owner attribution** (R2.1), active/inactive. |
| R11.2 | **Supplier** records — required by R10 and by hutang (R5.5). Include NPWP and whether they normally issue faktur. |
| R11.3 | **Customer** records — required by piutang (R5.6). Include NPWP/NIK for buyers who need faktur. |
| R11.4 | **Owner** records — the family members that R2 attributes products to. |
| R11.5 | Opening balances: stock on hand, hutang, piutang at go-live. Migration (R8.3) lands here. |

### R12 — Returns, adjustments, stock opname

| ID | Requirement |
|---|---|
| R12.1 | **Sales return / refund** — restores stock, reverses margin. Which FIFO layer it returns to must be decided (see §7). |
| R12.2 | **Purchase return** — removes stock, reverses the cost layer and any input PPN claimed. |
| R12.3 | **Void** a transaction (same-day error) as distinct from a return (goods came back later). |
| R12.4 | **Stock opname** — physical count, variance report, adjustment posting. Required at go-live given the existing drift, and periodically after. |
| R12.5 | Adjustments carry a reason code and land in the audit log (R7.2). |
| R12.6 | Every stock-changing event above must be attributable to an owner's margin correctly, or R2.4 breaks. |

### R13 — Users and access

| ID | Requirement |
|---|---|
| R13.1 | User accounts with login. |
| R13.2 | Roles at minimum: **owner**, **manager/admin**, **staff**. R7.1 depends on this distinction existing. |
| R13.3 | Staff cannot edit past transactions (R7.1) and should not see other owners' margin figures — confirm with the user. |
| R13.4 | Access is scoped per company (R1.1) — a user may hold different roles in each. |
| R13.5 | Every transaction records who entered it. |

### R14 — Data safety

A single local machine holding the numbers that family money is settled on.

| ID | Requirement |
|---|---|
| R14.1 | Automated local backup, scheduled, with a restore path that has been tested. |
| R14.2 | Backup to removable or network storage — a backup on the same disk is not a backup. |
| R14.3 | Export of all raw data in an open format (R5.7's "raw data can be pulled" — this is also the exit route if they ever leave). |

---

## 4. The PPN mechanic driving R3.4 and R6.2

Established during the interview. This is why purchases must be tracked, not just sales.

For a PKP, PPN is meant to pass through: you remit *output minus input*. But you can only subtract the input **if the supplier gave you a faktur**. No faktur, no credit — the 11% becomes real cost.

Worked example: buy at base 100.000, sell at base 150.000. Ideal margin 50.000.

**If you ARE PKP:**

| Supplier faktur | Buyer takes faktur | You pay | You receive | To DJP | **Margin** |
|---|---|---|---|---|---|
| ✅ | ✅ | 111.000 | 166.500 | 5.500 | **50.000** |
| ✅ | ❌ | 111.000 | 150.000 | 5.500 | **33.500** |
| ❌ | ✅ | 111.000 | 166.500 | 16.500 | **39.000** |
| ❌ | ❌ | 111.000 | 150.000 | 16.500 | **22.500** |

**If you are NOT PKP** — cannot charge PPN, cannot issue faktur, cannot credit input:

| Supplier | You pay | You receive | To DJP | **Margin** |
|---|---|---|---|---|
| non-PKP | 100.000 | 150.000 | 0 | **50.000** |
| PKP | 111.000 | 150.000 | 0 | **39.000** |
| *buyer wants faktur* | — | — | — | **lost sale** |

> **Pricing assumption in rows 2 and 4.** These assume the seller kept a non-PKP-style price of 150.000 and absorbed the PPN. If the shelf price were instead treated as PPN-inclusive, output PPN would be 150.000 × 11/111 ≈ 14.865 and the margin would land slightly differently. The table shows the worse case, which is what actually happens when a business registers as PKP and forgets to reprice. Both behaviours must be supported.

**Consequences for the build:**

- Same item, same prices → margin ranges 22.500 to 50.000 purely on paperwork. The user cannot see this today.
- A FIFO layer bought *with* faktur costs 100.000/unit. The same layer bought *without* costs 111.000/unit. **The layer must carry faktur status or every downstream margin is wrong** (R3.4).
- A PKP owes PPN on every taxable sale whether or not the buyer wanted a faktur. Retail prices for the PKP entity should be PPN-inclusive.
- A supplier's cheaper price without faktur may be more expensive in reality. The system should make this comparable.
- The same logic governs **which entity should buy** a given item. Purchasing into the non-PKP entity and transferring to the PKP entity later forfeits the input credit for good — see §6.2.

---

## 5. Owner margin — scope boundary

The user confirmed money **does** change hands between owners based on this number (monthly), and confirmed the scope is **gross margin only**:

```
revenue per owner − COGS per owner = margin per owner
```

Shared costs (listrik, gaji, sewa) are **out of scope** — handled outside the application. The user rejected Olsera's cost tracking specifically because it couldn't be tied to an owner, not because they reject cost tracking in principle.

**Therefore:** this report must be labelled *Laporan Margin per Owner*, not *Laba Rugi*. It has not paid rent. Naming it "P/L" invites someone to plan around a number that isn't profit.

Because real money moves on it:

- Every figure must drill down to the individual transactions behind it.
- FIFO layer attribution determines whose margin a sale belongs to — errors here move money between family members.
- Refund/void timing across a settlement boundary needs a decided rule (see §7).

---

## 6. Flagged to the user — not yet resolved

These are consequences of stated requirements that the user may not have considered. **Surface before building, don't decide unilaterally.**

**6.1 — Related-party transfer at cost (both directions).** Transferring at cost between two entities under common family control is a related-party transaction regardless of PKP status. Pasal 18 UU PPh is an arm's-length rule, not a VAT rule — it applies either way, reinforced by PMK 22/2020 and PP 20/2026 Pasal 58's aggregation rule.

The specific concern for PKP → non-PKP: stock moving at cost into the untaxed entity, which then sells at retail margin, shifts profit from a taxed entity to an untaxed one at a price no independent party would accept.

Not a reason to refuse the feature. A reason to tell them plainly and let them take it to their konsultan pajak.

**6.2 — The two directions fail differently, and neither is free.**

| Direction | At transfer | When the receiving entity sells |
|---|---|---|
| **PKP → non-PKP** | Taxable delivery. PPN applies and the non-PKP receiver **cannot credit it** — it becomes cost. | Sells clean, no PPN owed. |
| **non-PKP → PKP** | Nothing. No PPN, no faktur possible. | **PKP owes full output PPN with zero input credit.** Margin drops ~11% — row 3 of the §4 table. |

So the direction that looks free at transfer time is the one that costs more later. Neither is cost-neutral, which is what makes "no markup" (R4.3) misleading as a mental model.

**The actionable consequence (R4.5):** once stock passes through the non-PKP entity, the input credit is **destroyed permanently** — the chain cannot be reconnected afterwards. Stock destined to be sold by the PKP entity should be purchased directly by the PKP entity from the supplier, with a faktur.

The system should surface this at the moment of transfer, not in a report afterwards, because by then the money is already gone.

**6.3 — One-time payment vs. tax features.** R8.2 and R6.5 pull against each other. Tax rules are a *maintenance commitment*, not a build-once artifact — PPN's 12%×11/12 mechanism, PP 20/2026, and the Coretax rollout all landed within eighteen months. A perpetual-licence product with stale tax rates is worse than no tax feature.

Possible resolutions worth putting to them: perpetual licence with optional paid updates; or sell the engine with tax rates as a user-editable config the owner's consultant maintains. The second fits the effective-dated design and the "verify with your konsultan pajak" stance.

**6.4 — Expiry dates.** Alat kesehatan commonly carries expiry. FIFO gives correct *cost* but says nothing about which batch to physically pick; these diverge when a later-purchased batch expires sooner. Not a v1 requirement — but a nullable expiry field on the cost layer is free now and painful to retrofit.

---

## 7. Known risks

| Risk | Detail |
|---|---|
| **No period locking** (R7.3) | Nothing prevents October's figures shifting after October's money was split. Logs let you find out; they don't prevent it. Accepted by the user; revisit if it bites. |
| **Refund across settlement boundary** | A November refund of an October sale — whose margin, which month? Undecided. Reducing October reopens a settled period; booking it to November keeps the settlement honest but the report technically wrong. Needs a decided rule. |
| **Olsera migration fidelity** | Live stock and history must come across. Whatever Olsera's export offers constrains what's possible. Not yet investigated. |
| **Inter-company stock drift today** | Olsera records the transaction without moving stock, so current quantities are already wrong. Reconciliation frequency unknown ("maybe weekly/daily"). Migration inherits this. |
| **"Simple accounting" is undefined** | R5.7 defers it. Acceptable for now; will need pinning before the reporting phase. |

---

## 8. Explicitly out of scope

Rejected by the user or deferred:

- Marketing features
- Staff management
- Operational-cost tracking (as a general ledger of expenses — see §5)
- Consignment / legal ownership modelling — owner is an **attribution tag only**
- Markup on inter-company transfers
- FEFO / batch-expiry picking
- Shared-cost allocation between owners
- e-Faktur / Coretax API integration
- Multi-outlet cloud sync
- Payment gateway processing

---

## 9. Scope change from the previous spec

The earlier `docs/SPEC.md` modelled the **sales side only** — PPN Keluaran, no purchases. These requirements make that insufficient:

- §4 shows margin cannot be computed without purchase-side faktur status.
- R3 requires FIFO cost layers, which require purchase records.
- R5.3/R5.5 require purchases and payables.
- R4 requires stock movement between entities.

The previous spec's tax engine and omzet clock remain valid and reusable. The domain around them roughly doubles.

**Priority order implied by the interview** — the user's pain, ranked:

1. Multi-owner margin (money moves on it; currently faked with categories)
2. Inter-company stock movement (currently broken; data is drifting)
3. Purchase-side PPN tracking (invisible margin leak, every day)
4. Reports
5. Omzet threshold clock (matters once a year)

Note this inverts the earlier plan, which led with the omzet clock.

**But build order is not priority order.** R9–R14 are prerequisites — there is no margin report without purchases, no purchases without suppliers, no owner split without owner records. The ranking above says what earns the switch; the foundations still get built first, they just shouldn't be polished at the expense of items 1–3.

---

## 10. Deployment model — LAN server

**Decided.** One machine runs everything; other devices are browsers on the shop network.

```
   Server machine (Mac Mini / mini-PC)
   ├── application binary + database
   ├── thermal printer + cash drawer (attached here)
   └── serves on the local network, e.g. 192.168.1.x:8080
              │   shop WiFi / LAN — no internet required
     ┌────────┼─────────┬──────────┐
   cashier   admin    owner      owner
```

**"Offline" means no internet, not no network.** Their internet is unreliable; their own router is not. Separating those two is what makes this simple.

**Why it holds at this scale.** Under 1,000 transactions/day is roughly one write per 30 seconds in business hours, with fewer than 10 concurrent readers. That is orders of magnitude below where a single-machine database needs help.

**Explicitly rejected: per-device databases with sync.** It buys marginal uptime for conflict resolution, clock skew, and ambiguous merge semantics on FIFO layers — two devices consuming the same cost layer has no clean answer. Not a problem this business has.

**Consequences.**

| | |
|---|---|
| Server down | Everyone stops. Accepted (R8.7); mitigated by backup + restore drill. |
| Network down | Clients stop; server data stays intact. |
| Printing | Printer is attached to the server, which is where the cashier sits. |
| Addressing | Static IP or mDNS required (R8.6). |

**Still open:** owners viewing margin from *home* rather than the shop is a remote-access question, not a multi-device one. If needed, a mesh VPN on the server is the boring answer. Ask where each person actually sits when they use the system.

**Note:** a separate proposal will compare this local model against a hosted/online model. This decision covers v1 build only, not the long-term product shape.

---

## 11. Still open

- Does every product have an owner, or is company-owned stock common enough to need its own reporting treatment beyond a bucket? (R2.2 assumes a bucket suffices.)
- What does Olsera's export actually contain?
- How is inter-company stock drift reconciled today, and how long does it take?
- Refund-across-settlement rule (§7).
- Do they track expiry at all (§6.4)?
- Concrete definition of "simple accounting report" (R5.7).

**Surfaced during review, not yet put to the user:**

- **Where does each person physically sit** when they use the system? Determines whether remote access is needed on top of the LAN model (§10).
- **Does an inter-company transfer create hutang/piutang between the two entities**, or is it purely a stock movement with no money owed? At cost, either is defensible, and it changes the ledger.
- **Can staff see other owners' margin figures? (R13.3)** In a family business this is sensitive in both directions.
- **What are the credit terms** on piutang — net 30, case by case? Needed for aging (R5.8).
- **Which FIFO layer does a sales return restore to? (R12.1)** The original layer, or a new one at the return date? Affects whose margin moves.
- **Is the UI Bahasa Indonesia? (R9.12)** Assumed yes; never confirmed.
- **Do they need faktur pajak *generated*,** or is recording its existence enough? Current scope assumes the latter — they issue fakturs elsewhere. Worth verifying, because if they need generation, e-Faktur/Coretax comes back into scope and the one-time-payment tension (§6.3) sharpens considerably.
