// Package domain holds the pure core of the system: tax, FIFO, margin, omzet,
// and the money type they all share.
//
// # The rule
//
// Domain packages import nothing from database/sql, net/http, or any store or
// transport package. Pure functions over value types (ARCHITECTURE §2). That is
// what makes the tax engine, FIFO consumption, and the margin calculation
// testable — and those three are the product.
//
// The rule is enforced, not merely agreed: see the depguard configuration in
// .golangci.yml. Dependencies point inward only. money is the sole shared leaf.
//
// # Money
//
// No float ever enters these packages (INV-1). Rupiah is int64. Intermediates
// that genuinely need fractions — percentage discounts, inclusive-price
// division, FIFO unit costs — use shopspring/decimal and round once at the
// boundary back to int64. forbidigo bans float32/float64 here.
package domain
