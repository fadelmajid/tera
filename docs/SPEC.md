# SPEC — Tera

Technical specification for the four subsystems that carry real risk. Everything else is ordinary CRUD and doesn't need specifying here.

Read `docs/REQUIREMENTS.md` first — this document assumes it.

---

## 1. Money

`int64` rupiah everywhere. Rupiah has no circulating subunit; prices are whole numbers.

Intermediates — percentage discounts, inclusive-price division, FIFO unit costs — use `shopspring/decimal`, rounded once at the boundary back to `int64`. Never store a decimal. Never let a float touch JSON.

TypeScript: rupiah crosses the wire as a JSON number (Rp 4.8B is far under `MAX_SAFE_INTEGER`), typed as a branded `type IDR = number & { __brand: 'IDR' }` so it can't be mixed with a rate or a quantity.

**Unit costs are the exception worth care.** A FIFO layer of 7 units at Rp 100.000 total is Rp 14.285,71/unit. Store the **layer total and quantity**, derive unit cost when needed, and let the last consumption absorb the remainder. Storing a rounded unit cost and multiplying loses rupiah on every draw.

---

## 2. Tax engine

### 2.1 `tax_rule` — effective-dated config

**Never UPDATE a rate. INSERT a new row and close the old one's `valid_to`.** (INV-4)

| Field | Notes |
|---|---|
| `entity_id` | |
| `tax_type` | `PPN`, `PPNBM` |
| `rate_bp` | Basis points. 12% = `1200`. Integer. |
| `dpp_factor_num` / `dpp_factor_den` | The DPP nilai lain as an **exact fraction**. PPN non-luxury = `11/12`. No `0.916666...` anywhere. |
| `is_inclusive` | Is the listed price already tax-inclusive? |
| `calculation_level` | `LINE` or `INVOICE` — where rounding happens |
| `rounding_mode`, `rounding_unit` | `HALF_UP`, whole rupiah |
| `valid_from`, `valid_to` | |
| `legal_ref` | e.g. `PMK 131/2024` — displayed in the admin UI so the owner's consultant can verify it |

Seeded defaults (August 2026):

```
PPN non-luxury   rate_bp=1200  dpp=11/12  → effective 11%
PPnBM luxury     rate_bp=1200  dpp=1/1    → 12%
```

PB1/PBJT is not applicable here (medical supplies, not F&B). Don't build it.

### 2.2 Calculation

**Exclusive:**
```
dpp   = base
tax   = round(base × rate_bp/10000 × dpp_num/dpp_den)
total = base + tax
```

**Inclusive:**
```
effective_rate = rate_bp/10000 × dpp_num/dpp_den
dpp   = round(price / (1 + effective_rate))
tax   = price − dpp        ← subtract. Do not recompute independently.
```

The subtraction matters. Computing tax separately and adding lets a one-rupiah gap open between the shelf price and the sum of its parts — the class of bug that makes a cashier stop trusting the till.

**Property test:** for any generated cart, `Σ dpp + Σ tax == grand_total` exactly, zero drift.

### 2.3 PKP-dependent behaviour

The two entities behave differently and this must be driven by config, not branches scattered through the code.

| | PKP entity | Non-PKP entity |
|---|---|---|
| Charge PPN on sales | Yes, on every taxable sale | **Never** — legally cannot |
| Issue faktur | When the buyer needs one | Cannot |
| Credit input PPN | Only with a supplier faktur | **Never** |

A PKP owes output PPN **whether or not the buyer took a faktur**. A sale with no faktur issued still accrues the liability. This is not optional and is the single most common way a newly-PKP business loses margin silently.

### 2.4 PPN position

Per entity, per masa pajak (calendar month):

```
output PPN  = Σ tax on sales
input PPN   = Σ tax on purchases WHERE faktur_received = true   ← the filter is the whole point
payable     = output − input
```

Purchases without a faktur contribute **nothing** here. Their PPN went into the cost layer instead (§3.2).

---

## 3. FIFO inventory

### 3.1 Append-only layers (INV-7)

Two tables, both append-only. Never decrement a layer in place.

```
stock_layer
  id, entity_id, product_id, owner_id (nullable = company),
  acquired_at, source ENUM(PURCHASE, TRANSFER_IN, ADJUSTMENT, RETURN),
  source_doc_id,
  qty_in           int,
  cost_total_idr   int64,     -- net of creditable PPN. See 3.2
  faktur_received  bool,      -- INV-9
  ppn_paid_idr     int64,     -- what was actually paid in PPN
  expiry_date      date null  -- captured, not acted on. See REQUIREMENTS §6.4

stock_consumption
  id, layer_id, movement_id,
  qty_out          int,
  cost_idr         int64,     -- the slice of cost_total this draw took
  occurred_at
```

Remaining quantity on a layer is `qty_in − Σ qty_out`. Compute it; don't store it. If performance ever demands a cached balance (it won't at 1,000 SKUs), derive it into a view, never into a mutable column.

### 3.2 Cost is net of *creditable* PPN — and that depends on the entity

This is the rule that makes purchase tracking necessary at all.

| Buying entity | Faktur received | Paid | `cost_total_idr` | Why |
|---|---|---|---|---|
| PKP | ✅ | 111.000 | **100.000** | 11.000 is creditable — recoverable, not cost |
| PKP | ❌ | 111.000 | **111.000** | Nothing to credit. It's cost. |
| Non-PKP | either | 111.000 | **111.000** | Can never credit. Always cost. |

Same supplier, same price, three different cost bases. Get this wrong and every margin figure is wrong by 11%.

### 3.3 Consumption

Oldest `acquired_at` first, within the same `(entity, product, owner)`.

**Owner scoping is not optional.** A sale of Budi's product must draw from Budi's layers. Drawing across owners moves money between family members. If a sale requests more than an owner's layers hold, that is an **error surfaced to the user**, not a silent fallback to another owner's stock. (INV-8)

Ties on `acquired_at` break by `id` for determinism.

### 3.4 Inter-company transfer

One movement, two entities, at cost (R4.3):

1. Consume layers in the source entity → cost known.
2. Create a layer in the destination entity with `source = TRANSFER_IN`, `cost_total_idr` = the consumed cost.
3. Carry the **owner attribution** across. The family member owning the goods doesn't change because the goods crossed a company boundary.
4. `faktur_received` on the new layer:
   - **PKP → non-PKP**: PPN applies on the delivery; the receiver can't credit it. Layer cost = transfer cost + PPN.
   - **non-PKP → PKP**: no faktur possible. `faktur_received = false`. **The receiving PKP entity now holds stock with zero input credit.** Warn at the moment of transfer (R4.5) — afterwards the money is gone and nothing can be done.

Both directions are atomic: source consumption and destination layer commit in one database transaction, or neither happens. This is the exact bug Olsera has.

---

## 4. Owner margin

The highest-stakes output in the system. Real money is settled on it monthly (R2.4).

### 4.1 Definition — margin, not profit

```
margin(owner, period) = Σ (revenue − COGS) over sales of that owner's products
```

COGS comes from `stock_consumption.cost_idr` — the actual layers drawn, not an average.

Shared costs are **out of scope** (REQUIREMENTS §5). This is why the report is labelled *Laporan Margin per Owner* and never *Laba Rugi*. It has not paid rent.

### 4.2 Drill-down is a hard requirement

Every figure on the report must expand to the individual sales, and each sale to the individual FIFO layers it consumed and what each cost. When a family member asks "why is mine lower this month," the answer must be on screen.

No summary-only views. No aggregate the user can't decompose.

### 4.3 Unowned stock

`owner_id` is nullable. Null = company-owned, reported as its own bucket alongside the named owners (R2.2).

### 4.4 Returns across a settlement boundary

**Undecided — must be answered before building the report** (REQUIREMENTS §7).

A November return of an October sale: reducing October reopens a period whose money was already split; booking it to November keeps the settlement honest but makes October's report technically wrong.

Implement whichever the user chooses, but make the choice **explicit and configurable**, and show returns as a distinct line in the margin report either way. Do not silently pick one.

---

## 5. Omzet clock

Lower priority than the above (it matters once a year) but the rules are exact, so get them right.

### 5.1 The window is book-year, not rolling

The Rp 4.8B threshold is measured **per book year, cumulative, reset annually** (PMK 197/2013). Common guidance says "rolling 12 months" and is wrong.

Ledger, append-only, mirroring the FIFO pattern:

```
omzet_ledger
  entity_id, book_year, effective_date,
  event_type ENUM(SALE, VOID, REFUND, RETURN, ADJUSTMENT),
  signed_amount_idr,     -- negative for reversals
  source_txn_id
```

Two views, **labelled distinctly in the UI**:

- **Book-year cumulative** — the legally binding figure. Drives the alarm.
- **Trailing 12 months** — a momentum estimate only. "At this pace you'll cross around March." Never presented as the legal number.

### 5.2 Alarm

| State | Trigger |
|---|---|
| `OK` | < 70% |
| `WATCH` | ≥ 70% |
| `WARN` | ≥ 90% |
| `CROSSED` | ≥ Rp 4.8B |

`CROSSED` must emit **both dates**:

- **Register by** — end of the current book year (PMK 164/2023 Pasal 17(3))
- **VAT obligation starts** — first tax period of the *following* book year (Pasal 18)

The gap between those two dates is the most misunderstood part of this rule and most of the feature's value.

Crossing is **sticky within a book year**. A later refund dropping the cumulative back under Rp 4.8B does not un-cross it — the legal event already happened.

### 5.3 Which entity

Both are tracked, but the **non-PKP entity** is the one that can still cross. The PKP entity is already registered.

### 5.4 Edge cases requiring tests

- 23:30 WIB on 31 December lands in the closing book year (INV-5).
- Book year not starting in January (configurable).
- A void in January of a December sale decrements the **prior** book year — `effective_date` is the original sale's date.
- Omzet base gross vs net of VAT: default **net** for the PKP entity, configurable, stated in the disclaimer.

---

## 6. Audit log

Every edit to historical data (INV-10):

```
audit_log
  id, actor_user_id, occurred_at,
  entity_type, entity_id,
  action ENUM(CREATE, UPDATE, DELETE, VOID, ADJUST),
  before_json, after_json,
  reason  -- required for stock adjustments (R12.5)
```

The user declined period locking (R7.3). This log is the entire mitigation — it doesn't prevent October's numbers moving after October's money was split, but it makes it findable. Make sure it's queryable by period and by actor, or it's decoration.

---

## 7. Acceptance criteria

1. Full sale completes on the LAN with the internet disconnected; receipt prints, drawer opens.
2. Tax reconciles to hand-worked examples to the rupiah: exclusive PPN, inclusive PPN, mixed cart.
3. Property test: no cart produces a rounding gap between grand total and the sum of its parts.
4. A PKP sale with no faktur issued still accrues output PPN.
5. Purchase with faktur and purchase without faktur, same supplier price, produce **different layer costs** and therefore different margins.
6. A sale never draws from another owner's layers; insufficient stock surfaces an error.
7. Inter-company transfer moves stock atomically and preserves owner attribution.
8. Transfer into the PKP entity warns about input-credit loss **before** committing.
9. Margin report drills from an owner's monthly total down to individual FIFO layer costs.
10. Simulated book year crossing Rp 4.8B in month 7 produces correct `CROSSED` state with both dates.
11. Changing the PPN rate in config alters no historical transaction.
12. Every historical edit appears in the audit log with before/after.
