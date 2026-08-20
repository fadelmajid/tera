// Package money is the rupiah type and its arithmetic.
//
// Rupiah is int64 and has no circulating subunit, so prices are whole numbers
// (SPEC §1). Money is never a float — not here, not in the store, not in JSON
// (INV-1).
//
// Intermediates that need fractions use shopspring/decimal and round exactly
// once, at the boundary back to int64. A decimal is never stored.
//
// # Unit costs are the case worth care
//
// A FIFO layer of 7 units at Rp 100.000 total is Rp 14.285,71 per unit. Store
// the layer total and quantity, derive the unit cost when needed, and let the
// final draw absorb the remainder. Storing a rounded unit cost and multiplying
// loses rupiah on every consumption (SPEC §1, TASKS 1.4).
//
// Formatting for the UI is Indonesian: Rp 1.234.567.
//
// TASKS 0.2.
package money
