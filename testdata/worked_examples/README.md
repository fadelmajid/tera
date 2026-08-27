# worked_examples

Table-driven fixtures asserting behaviour against hand-worked figures: DJP tax
cases, FIFO consumption scenarios, owner-margin months, omzet book-year crossings.

These exist because the alternative is trusting arithmetic nobody checked. The
tax cases in particular get verified against DJP sources and a konsultan pajak
before this touches real books.

**Where the expected rupiah figure is uncertain, leave a `TODO` with the
reasoning rather than inventing a number someone might trust** (TASKS 5.6).

## What is here

| File | Cases |
|---|---|
| `ppn_exclusive.json` | tax added on top: `tax = round(base x rate)` |
| `ppn_inclusive.json` | tax inside the shelf price: `dpp = round(price / (1 + rate))`, `tax = price - dpp` |
| `ppn_rounding_level.json` | the same basket rounded per line and per invoice |
| `ppn_mixed_cart.json` | taxed and exempt lines on one invoice |
| `ppn_pkp_status.json` | what a PKP owes with no faktur, and what a non-PKP entity may not do |
| `ppn_unverified.json` | **the open questions — no figures asserted** |

Run them with `go test ./internal/domain/tax/`. The outstanding questions print
as one list:

```bash
go test ./internal/domain/tax/ -run Unverified -v
```

The tax fixtures are loaded with `DisallowUnknownFields`, so a mistyped key
fails the suite rather than silently asserting nothing, and a case carrying
neither an expected figure, an expected error, nor a `todo` is refused.

Named cases worth having, from TASKS:

| Case | Task |
|---|---|
| Same supplier price, faktur vs no faktur → different layer costs | 1.7 |
| A sale never draws another owner's layers | 3.6 |
| Transfer is atomic and preserves owner attribution | 4.6 |
| PPN worked examples to the rupiah, exclusive and inclusive | 5.6 |
| `Σ dpp + Σ tax == grand_total`, zero drift | 5.7 |
| 23:30 WIB on 31 December; non-January book-year start | 7.3 |
| Voiding a December sale in January decrements the prior book year | 7.6 |
| Crossing Rp 4.8B in month 7 → correct state and both dates | 7.11 |
