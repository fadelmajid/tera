package tax

import (
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Masa is a masa pajak: the period PPN is computed, paid, and reported over.
//
// One calendar month, in the entity's timezone (UU KUP Pasal 1 angka 7; INV-5).
// Not the book year, which is what the Rp 4.8 billion threshold is measured over
// (SPEC §5.1) — the two windows are different and confusing them puts turnover
// in the wrong year.
type Masa struct {
	Year  int
	Month time.Month
}

// ParseMasa reads a 'YYYY-MM' masa pajak.
func ParseMasa(s string) (Masa, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Masa{}, fmt.Errorf("%w: %q is not a YYYY-MM month", ErrInvalidMasa, s)
	}
	return Masa{Year: t.Year(), Month: t.Month()}, nil
}

// String renders the masa as 'YYYY-MM'.
func (m Masa) String() string { return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month)) }

// Range is the inclusive business-date window the masa covers.
//
// Built with UTC and then formatted, which is safe because only the calendar
// arithmetic is wanted here: the dates being ranged over were already resolved
// in the entity's zone when they were written (D-005), so this never converts an
// instant and never needs a location to do it with.
func (m Masa) Range() (from, to string) {
	first := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC)
	return first.Format(DateFormat), first.AddDate(0, 1, -1).Format(DateFormat)
}

// Next is the following masa pajak.
func (m Masa) Next() Masa {
	t := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	return Masa{Year: t.Year(), Month: t.Month()}
}

// Position is one entity's PPN position for one masa pajak (SPEC §2.4).
//
//	output PPN  = Σ tax on sales
//	input PPN   = Σ tax on purchases WHERE faktur_received = true
//	payable     = output − input
//
// The filter on the input side is the whole point of tracking purchases. Input
// PPN is creditable only against a faktur pajak (UU PPN Pasal 13 jo. Pasal 9);
// without one the rupiah went into the cost of the goods instead (SPEC §3.2),
// and counting it here as well would claim the same money twice — once as a
// credit against output PPN, once as a lower COGS in the margin the family
// settles on.
//
// The output side has no such filter, and the split below is why this type
// carries two fields where the formula has one term.
type Position struct {
	Masa Masa

	// OutputWithFaktur is output PPN on sales the buyer took a faktur for.
	OutputWithFaktur money.IDR

	// OutputWithoutFaktur is output PPN on sales that issued no faktur — the
	// ordinary walk-in.
	//
	// It is owed identically. The liability attaches to the delivery of taxable
	// goods, not to the paperwork (SPEC §2.3), so this is a real debt with no
	// document anywhere to remind anyone it exists. It is split out rather than
	// summed in because it is the figure a newly registered business is
	// surprised by, and a number nobody can see is a number nobody plans for.
	OutputWithoutFaktur money.IDR

	// OutputReversed is output PPN given back by sales returns in this masa.
	OutputReversed money.IDR

	// InputCreditable is input PPN on purchases where the supplier's faktur was
	// actually received (INV-9).
	InputCreditable money.IDR

	// InputReversed is credited input PPN handed back by purchase returns.
	InputReversed money.IDR

	// InputNonCreditable is PPN paid to suppliers that no faktur ever arrived
	// for. Reported so the figure is visible and never netted into the credit —
	// it is cost, and it is already sitting in the stock layers.
	InputNonCreditable money.IDR
}

// Output is PPN keluaran for the masa, net of returns.
func (p Position) Output() money.IDR {
	return p.OutputWithFaktur.Add(p.OutputWithoutFaktur).Sub(p.OutputReversed)
}

// Input is creditable PPN masukan for the masa, net of returns.
func (p Position) Input() money.IDR {
	return p.InputCreditable.Sub(p.InputReversed)
}

// Payable is output minus creditable input.
//
// Negative is a real and ordinary outcome — a month that bought more than it
// sold. The excess is carried forward to the next masa or claimed back at the
// end of the book year (UU PPN Pasal 9 ayat (4)); it is not clamped to zero
// here, because a clamped figure would quietly forget money the business is
// owed.
func (p Position) Payable() money.IDR { return p.Output().Sub(p.Input()) }

// IsOverpaid reports a lebih bayar: more creditable input than output.
func (p Position) IsOverpaid() bool { return p.Payable().IsNegative() }
