// Package service orchestrates the domain against the store.
//
// It owns what the domain deliberately does not: database transaction
// boundaries, audit log writes, and the sequencing of a business operation
// (ARCHITECTURE §2). It calls into domain packages; they never call back.
//
// # Transaction boundaries are the point
//
// A sale writes the sale, its lines, the stock consumptions, the tax snapshot,
// and the omzet ledger row in a single database transaction (ARCHITECTURE §4).
// A partial commit corrupts stock and margin at the same time.
//
// An inter-company transfer consumes the source layers and creates the
// destination layer in one transaction, or neither happens (SPEC §3.4). This is
// the exact flow Olsera gets wrong — it records the transaction without moving
// the stock, which is why the user's quantities are drifting from reality today.
//
// # Immutability and audit
//
// A finalised transaction is never mutated. Corrections are compensating
// records (INV-2). Every edit to historical data writes an audit_log row with
// actor, timestamp, before and after (INV-10) — the user declined period
// locking, so this log is the entire mitigation.
//
// Every mutating operation is idempotent on the caller's client_request_id
// (INV-6).
package service
