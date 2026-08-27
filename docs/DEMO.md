# Demo & test walkthrough

Twelve scenarios covering every feature, in an order that tells a story rather
than an order that follows the menu. Each one says what to click and what
should appear, so it works as a **live demo** and as a **manual end-to-end
test** without being two documents.

Every figure below is what the seeded data actually produces. The seeder is
deterministic — same script, same numbers, every time — so if a figure on screen
disagrees with one here, that is a finding rather than drift. Write it down.

**Time:** the whole thing is about 25 minutes. Scenarios 1–4 are the ten-minute
version and carry the argument on their own.

---

## Setting up

```bash
make release

pkill -f 'bin/tera'; rm -rf ~/tera-coba && mkdir -p ~/tera-coba
TERA_DB_PATH=~/tera-coba/tera.db \
TERA_ADDR=0.0.0.0:8080 \
TERA_BACKUP_DIR=~/tera-coba/cadangan \
TERA_BACKUP_ALLOW_SAME_DISK=1 \
./bin/tera > ~/tera-coba/tera.log 2>&1 &

grep -o 'password=[^ ]*' ~/tera-coba/tera.log      # the first-run password
python3 scripts/demo-data.py --password '<that password>'
```

Then open **http://localhost:8080** and log in as `admin`.

The seeder drives the real API, so every figure it produced was computed by the
system rather than written into it — FIFO drawn oldest-first and owner-scoped,
tax priced against the rules in force on the day, omzet written inside each
sale's own transaction.

### What is in the data

| | |
|---|---|
| Companies | **PT Sehat Sentosa** (PKP, retail counter) · **PT Medika Nusantara** (non-PKP, wholesale) |
| Owners | Budi, Sari, Andi, and a company bucket for unowned stock |
| Period | February – 21 August 2026 |
| Volume | 10 products, 3 suppliers, 5 customers, ~130 sales, 20 purchases |

Switch companies from the **sidebar dropdown**. Most scenarios are at
PT Sehat Sentosa; scenario 5 is the other one.

---

## 1. The thesis: a faktur is worth 11% of your cost — 3 min

*The single most important thing this software does, and the thing their
current system cannot see at all.*

**Pembelian**, scroll to February. Two invoices:

| Invoice | Date | Faktur | Paid | Into inventory cost |
|---|---|---|---|---|
| `INV-CMP-0210` | 10 Feb | ✅ yes | Rp 27.417.000 | **Rp 24.700.000** |
| `INV-CMP-0224` | 24 Feb | ❌ no | Rp 27.417.000 | **Rp 27.417.000** |

Same supplier. Same goods. Same price. **Rp 2.717.000 difference in what the
stock actually cost**, because a faktur makes the PPN creditable and no faktur
makes it cost.

> **Say this:** "Olsera records one price and one cost. Both of these look
> identical there. Every glove from the second invoice is 11% more expensive
> than it appears, and every margin on it is overstated by the same amount."

---

## 2. …and it follows the goods all the way to the margin report — 4 min

*The payoff. Scenario 1 is a claim; this is the evidence.*

**Margin per owner** → set the range to **1–31 March 2026** → expand **Budi** →
find nota **`20260324-0003`** → expand it → expand **P-GLOVE**.

One line, 260 boxes, drawing **two FIFO layers**:

| From | Qty | Cost | Per box | Faktur |
|---|---|---|---|---|
| February's first delivery | 119 | Rp 7.378.000 | **Rp 62.000** | ✅ |
| February's second | 141 | Rp 9.703.620 | **Rp 68.820** | ❌ |

Rp 62.000 against Rp 68.820 — exactly 11% — **on the same product, in the same
sale**. FIFO emptied the older layer and took the rest from the next one.

> **Say this:** "This is why the layers are append-only. Every rupiah on this
> report traces to a specific delivery and whether its paperwork arrived."

**Also on this screen:**
- The title is *Laporan Margin per Owner*, never *Laba Rugi* — and a note says
  shared costs are excluded. It has not paid rent.
- **Perusahaan** is a real line beside the family: stock nobody owns.
- Revenue is **net of PPN**. The line reads *"Diterima dari pelanggan
  Rp X = penjualan Rp Y + PPN Rp Z"* — because cost is already net of
  creditable PPN, so revenue has to be too, or the subtraction compares unlike
  things.

**August totals, for reference:** Budi Rp 1.207.568 · Perusahaan Rp 564.460 ·
Sari Rp 461.986 · Andi Rp 181.711.

---

## 3. Owner stock is never borrowed — 2 min

*The rule that stops the software moving money between family members.*

**Kasir** → add a product belonging to **Sari** → set a quantity far larger than
the shelf holds → try to save.

It refuses, and the message names **whose** stock is short and how much is
there. It does **not** quietly draw from Budi's.

> **Say this:** "There may be forty on the shelf and only three of them Sari's.
> Selling Budi's to cover Sari's order moves money between two people who did
> not agree to it. The system stops rather than guessing."

---

## 4. PPN you owe on sales nobody asked a faktur for — 3 min

*The most common way a newly-PKP business quietly loses margin.*

**Pajak & PPN** → masa **2026-08**.

| Line | August |
|---|---|
| Output PPN, sales **with** a faktur | Rp 577.549 |
| Output PPN, sales **without** a faktur | **Rp 499.260** ← still owed |
| Input PPN creditable (faktur received) | Rp 3.987.500 |
| Input PPN **not** creditable | Rp 0 (August) — Rp 7.964.000 across the year |

> **Say this:** "Nobody is chasing that Rp 499.260. No customer has a document
> for it. It is only visible here, and it is owed either way — the liability
> attaches to handing over the goods, not to printing a faktur."

Scroll down: every figure expands to the invoices behind it. This report goes to
a konsultan pajak, and a number they cannot check is a number they will not
sign.

Note the caveat on every tax screen: *estimasi berdasarkan data di sistem ini.*

---

## 5. The Rp 4,8 miliar clock — 3 min

**Switch company to PT Medika Nusantara** (sidebar dropdown) → **Beranda**.

The banner reads **CROSSED**, and shows **two figures side by side**:

- **Kumulatif tahun buku — angka yang mengikat: Rp 4.984.770.000** (103,84%)
- **12 bulan terakhir — estimasi laju, bukan angka resmi**

> **Say this:** "Most guidance online says rolling twelve months. It is wrong —
> the threshold is per book year, cumulative, reset annually. So we show both
> and label which is which."

And **two dates**, which is the part almost everyone gets wrong:

| | |
|---|---|
| Crossed on | **14 July 2026** |
| Register as PKP by | **31 December 2026** |
| Must start charging PPN from | **1 January 2027** |

> **Say this:** "Charging PPN before January is as wrong as registering late.
> That gap is most of what this feature is for."

---

## 6. Inter-company transfer, and the credit it destroys — 3 min

*The flow Olsera gets wrong, and the one that can cost real money.*

Still at **PT Medika Nusantara** → **Transfer antar PT** → destination
**PT Sehat Sentosa** → add **Kursi Roda**, qty 5 → preview.

A **blocking modal** appears, quoting the actual rupiah about to be destroyed —
not a percentage of a guess. **Cancel it** and confirm nothing moved.

Then look at the history: `TRF-20260808-0001` moved 5 wheelchairs and forfeited
**Rp 688.600** of input PPN, permanently. The other direction
(`TRF-20260815-0001`, Sehat → Medika) forfeited **Rp 0**.

> **Say this:** "Olsera records the transaction and does not move the stock,
> which is why your two companies' quantities are drifting today. Here both
> sides commit together or neither does — and the direction that destroys tax
> credit says so before it happens, not after."

The server enforces this: without the acknowledgement the write is refused. The
modal exists so a person finds out in time to change their mind.

---

## 7. Hutang and piutang, aged — 2 min

**Hutang.** Four buckets, all populated:

| Bucket | Amount |
|---|---|
| Belum jatuh tempo | Rp 6.216.000 |
| 31–60 hari | Rp 5.827.500 |
| 61–90 hari | Rp 2.397.600 |
| **Tanpa jatuh tempo** | **Rp 4.500.000** ← 1 document |

Sorted **worst first** — the supplier to ring this morning is at the top, not
whoever is alphabetically first.

> **Say this:** "That last row is an opening balance from the old system with no
> term recorded. Most software folds those into 'current', which reports them as
> fine. It might be six months late — nobody knows. So we count it and say so."

Expand a supplier → open **Rincian** on the part-paid invoice → the individual
payments are there. "Rp 3.000.000 outstanding" answers nothing when the
supplier's question is which invoice last month's transfer was against.

**Piutang** is the same screen, other direction: Klinik Harapan Bunda is
37 days overdue on Rp 2.860.000.

---

## 8. Reports — 2 min

**Laporan**, three tabs sharing one date range.

- **Penjualan** — per day, per product, per payment method; margin per product;
  and the **voided sale from today** counted separately rather than hidden. A
  week with eleven voids is a training problem, invisible on a report that shows
  only what stuck.
- **Pembelian** — per supplier, with **how many of their invoices came with a
  faktur**. Comparing suppliers on price alone is comparing the wrong number.
- **Stok** — 10 lines, ~6.150 units, valued at cost per owner, flagging layers
  that arrived without a faktur. It says plainly that on-hand is **now**, not
  period-end: this system does not reconstruct historical balances, and a
  mislabelled figure would be worse than an honest one.

---

## 9. The till, end to end — 2 min

**Switch back to PT Sehat Sentosa.**

**Kasir** — ring a real sale. Two items, take cash, save. The receipt panel
shows *"Termasuk PPN Rp X atas DPP Rp Y"* and the three figures add up exactly.

> The receipt says "Termasuk", not "PPN". Under inclusive pricing the tax is
> already in the total, and a line reading "PPN" above "TOTAL" makes the receipt
> look like it does not add up — to the one person standing there with the money.

**Void it** (the till is still open) → **Laporan → Stok** → the quantity is back
exactly where it was.

**Sesi kas** — a closed session per month back to February, each with its own Z-report
and a small variance. Non-cash takings are listed but not counted into expected
cash.

---

## 10. Returns across a settlement boundary — 2 min

*The question that has no obviously right answer, decided explicitly.*

**Margin per owner** → **1–31 August**. Under **Budi** there is a return of nota
`20260715-0002`: **sold 15 July, came back 4 August**, Rp 95.000, flagged as
crossing the period.

Now set the range to **1–31 July**. The return appears under *"retur yang
kembali di periode lain"* — **as context, excluded from every total**.

> **Say this:** "October's money was already split between the family. Reducing
> October in November reopens a settlement. So the return counts in the month
> the goods came back, and the month of the sale still tells you it happened.
> The other rule is implemented too, and switchable — because which one is right
> is a fact about how your family settles, not about accounting."

---

## 11. Stock opname — 1 min

**Opname stok** → open the count from 18 August. Four lines counted, two with
variances, each carrying a reason code. Not yet posted.

> A variance without an explanation is exactly what this system exists to stop
> being normal — the reason code is required, and only the six documented codes
> are accepted.

---

## 12. It is your data — 2 min

**Ekspor data.** Before downloading anything, the screen lists what the archive
contains: **41 tables and views, ~1.900 rows, schema version 14** — read from the
database itself, not from a list maintained by hand.

Download it. Open a CSV in Excel. Read the `README.md` inside: rupiah are whole
integers, dates are `YYYY-MM-DD` in the company's timezone, empty means NULL.

> **Say this:** "If you ever leave this software, this is what you take. It is
> not a lock-in you have to negotiate your way out of, and the SQLite file is a
> byte-exact copy you can open with any tool."

**Backups**, if asked:

```bash
./bin/tera backups --dir ~/tera-coba/cadangan   # hourly, verified
make restore-drill                              # backup → restore → check figures
```

The drill restores into a directory that has never held a database and compares
the numbers. An untested backup is a rumour.

---

## The ten-minute version

Scenarios **1 → 2 → 4 → 5**. The faktur cost basis, its arrival in the margin
report, the PPN nobody asked for, and the threshold clock. Everything else is
supporting evidence.

## The thirty-second version

Open **Margin per owner → March → Budi → nota 20260324-0003 → P-GLOVE**, and
point at the two rows: **Rp 62.000 and Rp 68.820 per box, same product, same
sale**.

---

## Using this as a test pass

Work through 1–12 in order and note anything that disagrees with the figures
here. Things worth being suspicious about:

- Any total that does not equal the sum of the rows beneath it.
- A margin figure that changes when you reload without anything having changed.
- A refusal whose message does not say what to do instead.
- Any rupiah figure with a decimal point in it.
- A tax or threshold screen with no *konsultan pajak* caveat.

The automated suite covers the layers underneath — `make ci` runs 759 tests —
but no test can tell you whether a person who is not the developer can follow
what is on screen. That is what this pass is for.
