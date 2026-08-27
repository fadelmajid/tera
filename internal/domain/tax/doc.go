// Package tax computes PPN. Pure functions over a cart, a set of effective-dated
// rules, and the selling entity's PKP status (SPEC §2).
//
// # Rates are never hardcoded
//
// Every rate, DPP factor, threshold, and rounding mode arrives as a tax_rule
// value read from config (INV-4). Indonesian tax law changed three times in
// eighteen months. The DPP nilai lain is carried as an exact fraction —
// 11/12, never 0.916666… (SPEC §2.1).
//
// # Inclusive pricing subtracts
//
//	dpp = round(price / (1 + effective_rate))
//	tax = price - dpp
//
// Subtract. Never recompute the tax independently and add: that opens a
// one-rupiah gap between the shelf price and the sum of its parts, which is the
// class of bug that makes a cashier stop trusting the till (SPEC §2.2).
//
// # PKP status changes behaviour, and it is enforced here
//
// A PKP entity owes output PPN on every taxable sale whether or not the buyer
// took a faktur — the most common way a newly-PKP business loses margin without
// noticing. A non-PKP entity charges no PPN, issues no faktur, and credits no
// input PPN, ever (SPEC §2.3).
//
// Tax is snapshotted onto the transaction at sale time and never recomputed
// from current config afterwards (INV-3); this package computes, the service
// layer persists the snapshot.
//
// Where a legal rule is encoded, cite the regulation in a comment.
//
// # The PPN position
//
// Per masa pajak — one calendar month in the entity's timezone — output PPN
// less creditable input PPN (SPEC §2.4). The filter on the input side is the
// whole point of tracking purchases: only a faktur makes input PPN creditable,
// and without one the rupiah went into the cost of the goods instead.
//
// TASKS 5.3–5.7 and 5.9.
package tax
