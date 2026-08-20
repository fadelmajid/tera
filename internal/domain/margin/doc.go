// Package margin computes gross margin per owner over a period (SPEC §4).
//
// This is the highest-stakes output in the system. Family members settle money
// between themselves monthly on this figure. If it is wrong, people who eat
// dinner together argue about it.
//
//	margin(owner, period) = Σ (revenue - COGS) over sales of that owner's products
//
// COGS comes from the actual stock_consumption rows — the specific layers
// drawn and what each cost — never an average (SPEC §4.1).
//
// # Gross margin, not profit
//
// Shared costs (listrik, gaji, sewa) are out of scope and handled outside the
// application (REQUIREMENTS §5). The report is labelled Laporan Margin per
// Owner and never Laba Rugi: it has not paid rent, and naming it P/L invites
// someone to plan around a number that is not profit.
//
// # Drill-down is a hard requirement
//
// Every figure expands to the sales behind it, and every sale to the layers it
// consumed with their costs (SPEC §4.2). No summary-only views, no aggregate
// the user cannot decompose. When a family member asks why theirs is lower this
// month, the answer has to be on screen.
//
// Stock with a null owner is the company bucket, reported alongside the named
// owners (R2.2).
//
// # Open
//
// Returns across a settlement boundary are undecided (SPEC §4.4, TASKS 3.4).
// A November return of an October sale either reopens a settled period or makes
// October's report technically wrong. Implement whichever the user chooses,
// configurably, and show returns as a distinct line either way. Do not silently
// pick one.
//
// TASKS 3.1–3.6.
package margin
