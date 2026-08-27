// Package margin computes gross margin per owner from the layers a sale
// actually drew.
//
// This is the highest-stakes output in the system. Family members settle money
// between themselves monthly on these figures (R2.4), so a wrong number is not
// a wrong report — it is an argument between people who eat dinner together.
// The package is written accordingly: it refuses to produce a figure it cannot
// justify rather than producing a plausible one.
//
// # Gross margin, not profit
//
//	margin(owner, period) = Σ revenue − Σ COGS
//
// COGS is the sum of the actual [Draw] records — the specific FIFO layers the
// sale consumed and what each slice cost (SPEC §4.1). Never an average, never
// a unit cost multiplied back out. An average would also make "which layer"
// undecidable in the drill-down, which is the whole point of the screen.
//
// Shared costs — listrik, gaji, sewa — are out of scope (REQUIREMENTS §5) and
// are settled outside the application. The report this produces is
// *Laporan Margin per Owner*, never *Laba Rugi*: it has not paid rent, and
// naming it profit invites someone to plan around a number that isn't.
//
// # Attribution comes from two independent places, and they must agree
//
// Revenue is attributed by the owner snapshotted onto the sale line at the
// moment of sale. COGS is attributed by the owner on the stock layer, recorded
// when the goods were bought. Both are facts, written at different times by
// different people, and [Compute] checks that they agree for every product on
// every sale. They can only disagree if something upstream is broken — and on
// this report, a silent 11% is exactly what nobody would notice.
//
// A null owner is the company bucket, reported as its own line beside the
// named owners (R2.2, INV-8). It is a real attribution, not a default.
//
// # Drill-down is a hard requirement, not a feature
//
// Every figure decomposes: owner total → the sales behind it → per product →
// the individual layers each drew, with what each cost and whether a faktur
// pajak backed it (SPEC §4.2). There are no summary-only views here, so the
// output type carries the whole tree rather than totals a caller has to go and
// re-derive. When a family member asks why theirs is lower this month, the
// answer has to already be on screen.
//
// # Returns
//
// Which period a return belongs to when it crosses a settlement boundary is a
// fact about how a family settles, not about accounting (SPEC §4.4). Resolved
// as [AtReturnDate] in D-012 — it never restates money that has already been
// divided — with [AtSaleDate] still implemented and the choice held per company
// by the service layer. [ReturnPeriodUnset] is not a default here, it is an
// error: this package will not decide it for a caller who has not.
//
// Either way returns are reported as their own line, never netted silently into
// revenue, because "your margin dropped" and "your margin dropped because half
// of March came back in April" are different answers. And the month a return
// was *sold* in reports it too, as [OwnerReport.LaterReturns] — context
// excluded from every total, so a settled month keeps its figures and still
// says what came back afterwards.
//
// Pure. No database, no HTTP, no clock: business dates arrive already resolved
// in the entity's timezone (D-005), because this package does not know the
// entity's zone and must not guess (INV-5).
package margin
