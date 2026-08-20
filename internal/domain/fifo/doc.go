// Package fifo consumes stock layers oldest-first and reports the exact cost
// taken from each (SPEC §3).
//
// # Append-only
//
// Layers and consumptions are both append-only (INV-7). A layer is never
// decremented in place. Remaining quantity is derived as qty_in - Σ qty_out,
// never stored. That is what lets a margin figure decompose, months later, into
// the specific layers a sale drew from — the audit trail owner settlement
// depends on.
//
// # Consumption is owner-scoped, and running short is an error
//
// A sale of Budi's product draws only from Budi's layers (INV-8). If those
// layers do not hold enough, that is an error surfaced to the user — never a
// silent fall back to another owner's stock. Real money is settled between
// family members on these figures, so a cross-owner draw moves money between
// people. Ties on acquired_at break by id, for determinism (SPEC §3.3).
//
// # Cost basis depends on faktur status and the buying entity
//
// A layer records whether a faktur pajak was received (INV-9), because that is
// what decides whether the PPN paid was recoverable or real cost: PKP with
// faktur is net of the creditable PPN, PKP without faktur is not, and a
// non-PKP entity can never credit and so always carries the full amount
// (SPEC §3.2). Same supplier price, three different cost bases. Get this wrong
// and every margin downstream is wrong by ~11%.
//
// # Returns reverse the original draw
//
// A sales return restores stock to the layer it came from (R12.1, D-010) so
// that COGS reverses at exactly the cost taken and the margin reverses exactly.
// It is expressed as an appended consumption with a negative qty_out and cost,
// referencing both the layer and the draw being reversed — never as an edit to
// the layer, which stays append-only (INV-7). remaining = qty_in - Σ qty_out
// needs no special case for it.
//
// Which *period* a return lands in is a separate and still open question
// (SPEC §4.4).
//
// TASKS 1.2–1.4.
package fifo
