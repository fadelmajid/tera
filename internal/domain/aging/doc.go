// Package aging ages outstanding hutang and piutang. Pure functions over a
// slice of balances and a date to age them against (R5.5, R5.6, R5.8).
//
// # A balance without an age is not actionable
//
// That is R5.8's wording and it is the whole of this package. "PT Medika Jaya
// owes Rp 14.000.000" is a fact; "Rp 11.000.000 of it has been overdue for
// eleven weeks" is something a person can act on this morning. The report is
// built to be read in that order — worst first — rather than alphabetically.
//
// # Nothing here invents a credit term
//
// An invoice with no due date is not aged. It goes into its own bucket, is
// counted, and is reported as what it is: a document nobody recorded a term
// for. Ageing it from the invoice date would assume payment on delivery and
// report a supplier as ninety days late when they never gave a term at all.
//
// Which terms this business actually works on is still an open question
// (REQUIREMENTS §11: "net 30, case by case?"). Until it is answered, the honest
// report is one that says how many documents it could not age, and this package
// makes that number impossible to hide.
//
// # Partial payments
//
// Outstanding is the invoice less what has been paid against it, computed by
// the caller from append-only payment rows (migration 008). Ageing runs on the
// outstanding figure, not the invoice: an invoice half paid and a month late is
// half the problem it was.
//
// An over-payment — outstanding below zero — cannot be aged, because it is not
// a debt. It is reported separately as a credit rather than dropped, since
// money paid twice is exactly the kind of thing a ledger should not lose.
//
// # Time
//
// Dates arrive as 'YYYY-MM-DD' business dates already resolved in the entity's
// timezone (D-005, INV-5). This package never converts an instant to a day.
//
// TASKS 6.4–6.5.
package aging
