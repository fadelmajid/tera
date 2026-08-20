// Package omzet tracks gross turnover against the Rp 4.8 billion PKP threshold
// (SPEC §5). Pure functions over a slice of ledger entries.
//
// # The window is the book year, not a rolling twelve months
//
// The threshold is measured per book year, cumulative, reset annually
// (PMK 197/2013). Common guidance says "rolling 12 months" and is wrong. Two
// figures are produced and the UI must label them distinctly:
//
//   - book-year cumulative — the legally binding number, drives the alarm
//   - trailing twelve months — a momentum estimate only, never the legal number
//
// # Crossing is sticky
//
// The alarm walks OK → WATCH (≥70%) → WARN (≥90%) → CROSSED (≥ threshold). A
// later refund dropping the cumulative back under the threshold does not
// un-cross it: the legal event already happened.
//
// On crossing, emit both dates. Registration is due by the end of the current
// book year (PMK 164/2023 Pasal 17(3)); the VAT obligation starts in the first
// tax period of the following book year (Pasal 18). The gap between those two
// dates is the most misunderstood part of the rule and most of this feature's
// value.
//
// # Time is entity-local
//
// Book year and business date are resolved in the entity's timezone, honouring
// a configurable book_year_start_month — never UTC (INV-5). 23:30 WIB on 31
// December lands in the closing book year. A void in January of a December sale
// decrements the prior book year, because effective_date is the original sale's.
//
// Both entities are tracked, but the non-PKP one is the only one that can still
// cross; the PKP entity is already registered.
//
// TASKS 7.7–7.9.
package omzet
