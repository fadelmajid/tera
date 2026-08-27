# Go-live — TASKS 8.5

**R12.4: stock opname is required at go-live given the existing drift.** This is
the procedure, and the order matters more than any single step.

The business is switching systems mid-life. What it owns on day one has to be
typed in, because it cannot be derived from a purchase history this system does
not have (R11.5). Everything downstream — FIFO layers, owner margin, the omzet
clock — is built on whatever is entered here.

## Why the count comes first, and why it is not optional

Their current quantities are **known to be wrong**. Olsera records an
inter-company transaction without moving the stock, so the two companies' counts
have been drifting apart for as long as they have been doing it
(REQUIREMENTS §2). Importing those quantities would import the drift and then
build FIFO layers on it, and every margin figure after that inherits the error.

A physical count is the only thing that resets it. It is also the last easy
moment to do one: after go-live, a count means closing the shop.

## The order

### 1. Master data first

Products, owners, suppliers, customers. Nothing else can be entered until
products exist.

- **Every product gets an owner** or is deliberately left in the company bucket
  (R2.2, INV-8). This is the field the whole margin report hangs on, and the
  business currently fakes it with categories — so this is where that gets
  fixed, not later.
- Set `sale_price_idr` to the shelf price.
- Barcodes where they exist; the scanner needs them.

### 2. Count the stock. On paper. With the shop closed.

Print a count sheet per owner (`GET /api/v1/opname/count-sheet`), and count what
is physically there.

Count **per company**. Goods sitting in the same room can belong to either
entity, and which one owns them decides which company's books they are in and
whose margin they eventually land in.

Do not reconcile against Olsera's figures while counting. Count what is there,
then compare afterwards — knowing the expected number changes what people see on
a shelf.

### 3. Enter opening stock

For each product and owner: quantity and **cost per unit** — see below.

Opening balances create FIFO layers with `source = OPENING`. They are the layers
every early sale will draw from, so their cost is every early margin figure.

#### What cost to use

The cost basis rule still applies (SPEC §3.2, INV-9). For each line, use what
the goods actually cost the company that owns them:

| Situation | Enter |
|---|---|
| Bought by the PKP company **with** a faktur | Price **excluding** PPN |
| Bought by the PKP company **without** a faktur | Price **including** PPN |
| Bought by the non-PKP company | Price **including** PPN |

If the faktur status of old stock is genuinely unknown, record it as **no
faktur** and note it. That is the conservative direction: it makes the stock
look slightly more expensive and the margin slightly thinner, which is the
error a family would rather find than the reverse.

### 4. Enter hutang and piutang

What the shop owes and is owed on day one (R11.5), with **due dates**. A balance
without an age is not actionable (R5.8), and an opening balance with no due date
lands in the "tanpa jatuh tempo" bucket on the aging report and stays there
until somebody fixes it.

### 5. Check the tax configuration

- Is the PKP company's PPN rule right — rate, DPP nilai lain, and whether shelf
  prices already include PPN? (Pengaturan → Pajak.)
- Is the omzet threshold right for both companies?
- Take `testdata/worked_examples/ppn_unverified.json` and
  `omzet_unverified.json` to the konsultan pajak **before** the first sale. Nine
  questions between them, and every answer is a config row rather than a
  release.

### 6. Reconcile against Olsera, and write down the differences

Now compare. Every difference is worth understanding, because each one is
either a counting mistake or a real drift that has been there for a while — and
the second kind is the reason for doing this.

Keep that list. It is the evidence for what the old system was doing wrong.

### 7. Walk the demo script

`docs/DEMO.md` is twelve scenarios covering every feature, with the figures the
seeded demo data produces. Run it once against a throwaway database before
go-live: it is the cheapest way to find a screen that does not explain itself,
and it doubles as the script for showing the family what they are getting.

### 8. Run the two drills

- **Offline drill** (`docs/OFFLINE-DRILL.md`) — a sale completes with the
  internet unplugged, and the receipt prints.
- **Restore drill** (`docs/RESTORE-DRILL.md`) — a backup restores onto the spare
  laptop and the figures come back.

Neither is passed by reading it. Both are cheaper to fail now than in March.

### 9. First real day

Open a cash session, trade, close it, and read the Z-report against the drawer.

Then, at the end of the first week: run the margin report and have the family
look at it. If the owner attribution is wrong, that is the week to find out —
not the month money moves on it.

## What is deliberately not migrated

**No transaction history.** No past sales, no past purchases, no historical
margin. Only balances as at go-live.

This is not a limitation of the importer; it is a consequence of what Olsera
exports. See TASKS 8.4 and the note in `docs/DECISIONS.md` — their exports are
report-shaped and carry no per-transaction cost, so historical FIFO layers and
historical margin cannot be reconstructed from them at all. A margin report
covering a period before go-live would be a fabrication.

The old system's reports remain the record for the period before go-live. Keep
them.
