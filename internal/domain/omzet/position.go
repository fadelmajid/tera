package omzet

import (
	"fmt"
	"sort"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// State is the alarm (SPEC §5.2).
type State string

// The alarm, in the order it walks.
const (
	OK      State = "OK"
	Watch   State = "WATCH"
	Warn    State = "WARN"
	Crossed State = "CROSSED"
)

// Input is one book year to measure.
type Input struct {
	EntityID string
	Calendar Calendar
	// Thresholds is the entity's effective-dated config. The figure is never a
	// constant in this package (INV-4).
	Thresholds ThresholdSet

	// BookYear is the year to report on, labelled by the year it opens in.
	BookYear int
	// AsOf is the business date the clock is read on, in the entity's zone.
	AsOf string

	// Supplied is the range of effective dates Entries covers.
	//
	// Declared by the caller and checked, because this package produces two
	// figures over two different windows and a caller who queried only the book
	// year would get a trailing-twelve-month number that is quietly short. A
	// wrong momentum estimate is worse than none: it is the figure somebody
	// plans a year around.
	Supplied Window
	Entries  []Entry
}

// Position is the omzet clock for one book year.
//
// The two figures are deliberately separate fields with deliberately different
// names, and the UI must keep them apart (SPEC §5.1). Cumulative is what the law
// measures. Trailing12 is a pace estimate and nothing more.
type Position struct {
	EntityID string
	BookYear int
	Window   Window
	AsOf     string
	// CountedTo is how far into the book year the figures run: AsOf while the
	// year is still open, the year's last day once it has closed.
	//
	// A clock read on 30 June shows turnover to 30 June. Summing the whole year
	// regardless would report a business as crossed months before it was, which
	// is a registration deadline arriving early and a year of planning built on
	// it.
	CountedTo string

	// Threshold is the config row this was measured against, carried so the
	// screen can cite the regulation rather than assert a number.
	Threshold Threshold

	// Cumulative is turnover for the book year to date: the legally binding
	// figure, and the one the alarm is driven by (SPEC §5.1).
	Cumulative money.IDR
	// PercentBP is Cumulative against the threshold in basis points — 7000 is
	// 70%. Integer, because a ratio in this system is not a float either.
	PercentBP int64
	// Remaining is what is left before the threshold. Negative once past it,
	// deliberately: clamping would hide by how much.
	Remaining money.IDR

	// Peak is the highest the running cumulative reached during the book year,
	// and PeakOn the day it did.
	//
	// This is what explains a sticky crossing. A year that crossed in September
	// and took returns in November reports a Cumulative below the threshold and
	// a State of CROSSED, and without the peak that reads like a bug.
	Peak   money.IDR
	PeakOn string

	// Trailing12 is turnover over the twelve months ending on AsOf, across book
	// years.
	//
	// A momentum estimate. Never the legal figure, never what the alarm reads,
	// and never to be shown without saying which of the two it is: it is the
	// number most guidance online quotes, and mistaking it for the binding one
	// is the mistake this feature exists to prevent.
	Trailing12       money.IDR
	Trailing12Window Window

	State State
	// CrossedOn is the day the running cumulative first reached the threshold,
	// empty when it has not. Once set for a book year it stays set: the legal
	// event happened, and a later refund does not unhappen it (SPEC §5.2).
	CrossedOn string
	// RegisterBy and VATStarts are set only when crossed, and the gap between
	// them is most of this feature's value (SPEC §5.2).
	RegisterBy string
	VATStarts  string

	// Entries is how many ledger rows the book-year figure was built from, so
	// the screen can say a figure of zero means no trade rather than no data.
	Entries int
}

// IsCrossed reports whether the threshold was reached in this book year.
func (p *Position) IsCrossed() bool { return p.State == Crossed }

// Compute measures one book year against the threshold in force (SPEC §5).
//
// # The window is the book year, not a rolling twelve months
//
// The threshold is cumulative per book year and resets annually (PMK 197/2013).
// Common guidance says rolling twelve months and is wrong, which is why this
// function returns both figures under names that cannot be confused and why the
// trailing one is documented as an estimate everywhere it appears.
//
// # Crossing is sticky, and falls out of the arithmetic rather than being
// bolted on
//
// The ledger is walked in effective-date order, and the threshold is tested
// against the cumulative at the *end* of each day. So:
//
//   - A refund in November cannot lower September's day-end total, and a year
//     that crossed in September stays crossed however much comes back later.
//   - A void carries the original sale's date (SPEC §5.4), so it lands back on
//     the day it is undoing. If removing it means that day never reached the
//     threshold, the year genuinely never crossed — the sale did not happen,
//     and turnover that did not happen never counted.
//
// The difference between those two is the difference between a sale that was
// reversed and a sale that never was, and it is why they are separate event
// types rather than one word for both.
func Compute(in Input) (Position, error) {
	if err := in.Calendar.Validate(); err != nil {
		return Position{}, err
	}
	if _, err := time.Parse(DateFormat, in.AsOf); err != nil {
		return Position{}, fmt.Errorf("%w: as-of %q is not a YYYY-MM-DD date", ErrInvalidDate, in.AsOf)
	}
	if in.Thresholds.EntityID() != "" && in.EntityID != "" &&
		in.Thresholds.EntityID() != in.EntityID {
		return Position{}, fmt.Errorf("%w: entity %s measured against %s's threshold",
			ErrInvalidThreshold, in.EntityID, in.Thresholds.EntityID())
	}

	window := in.Calendar.BookYearWindow(in.BookYear)
	trailing := trailingWindow(in.AsOf)

	// The book year to date, not the whole book year. Everything below counts
	// within this.
	counted := Window{From: window.From, To: min2(window.To, in.AsOf)}

	if err := in.checkSupplied(window, trailing); err != nil {
		return Position{}, err
	}

	// The threshold in force on the day being measured, clamped into the book
	// year: a closed year is measured against the figure that applied while it
	// ran, not against whatever is current now.
	measuredOn := clampDate(in.AsOf, window)
	threshold, ok := in.Thresholds.Effective(measuredOn)
	if !ok {
		return Position{}, &NoThresholdError{BusinessDate: measuredOn, BookYear: in.BookYear}
	}

	entries := make([]Entry, len(in.Entries))
	copy(entries, in.Entries)
	for _, e := range entries {
		if err := e.validate(in.Calendar); err != nil {
			return Position{}, err
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.EffectiveDate != b.EffectiveDate {
			return a.EffectiveDate < b.EffectiveDate
		}
		return a.ID < b.ID
	})

	out := Position{
		EntityID: in.EntityID, BookYear: in.BookYear, Window: window,
		AsOf: in.AsOf, CountedTo: counted.To, Threshold: threshold,
		Trailing12Window: trailing,
	}
	if counted.To < counted.From {
		// The book year has not opened yet. Everything is zero and nothing has
		// crossed, which is the honest answer rather than an error.
		out.CountedTo = ""
	}

	// One pass, day by day. The threshold is tested at each day's end, after
	// every row dated that day has been applied — which is what makes a
	// same-day void able to prevent a crossing while a later refund cannot
	// undo one.
	var running money.IDR
	for i := 0; i < len(entries); i++ {
		e := entries[i]

		if trailing.Contains(e.EffectiveDate) {
			out.Trailing12 = out.Trailing12.Add(e.Amount)
		}
		if !counted.Contains(e.EffectiveDate) {
			continue
		}

		running = running.Add(e.Amount)
		out.Entries++

		// Everything else on the same day first. This is what lets a same-day
		// void prevent a crossing while a later refund cannot undo one.
		if i+1 < len(entries) && entries[i+1].EffectiveDate == e.EffectiveDate {
			continue
		}

		if running > out.Peak {
			out.Peak, out.PeakOn = running, e.EffectiveDate
		}
		if out.CrossedOn == "" && reached(running, threshold.AmountIDR, 10_000) {
			out.CrossedOn = e.EffectiveDate
		}
	}

	out.Cumulative = running
	out.Remaining = threshold.AmountIDR.Sub(running)
	out.PercentBP = percentBP(running, threshold.AmountIDR)
	out.State = state(running, out.CrossedOn, threshold)

	if out.State == Crossed {
		out.RegisterBy, out.VATStarts = deadlines(in.Calendar, in.BookYear, out.CrossedOn, threshold)
	}
	return out, nil
}

// checkSupplied refuses a request whose entries cannot cover what it asks for.
func (in Input) checkSupplied(book, trailing Window) error {
	if in.Supplied.From == "" && in.Supplied.To == "" {
		// Not declared. Trust the caller and say so in the doc rather than
		// silently half-checking.
		return nil
	}
	need := Window{From: min2(book.From, trailing.From), To: max2(book.To, trailing.To)}
	if in.Supplied.From > need.From || in.Supplied.To < need.To {
		return fmt.Errorf(
			"%w: entries cover %s but book year %d and the trailing twelve months need %s",
			ErrInvalidEntry, in.Supplied, in.BookYear, need)
	}
	return nil
}

// trailingWindow is the twelve months ending on asOf, inclusive.
//
// Twelve months back and a day forward, so a year is 365 or 366 days and not
// 365 plus the one that started it — the estimate is only ever an estimate, but
// an off-by-one in its window is still an off-by-one.
func trailingWindow(asOf string) Window {
	day, err := time.Parse(DateFormat, asOf)
	if err != nil {
		return Window{From: asOf, To: asOf}
	}
	from := day.AddDate(-1, 0, 0).AddDate(0, 0, 1)
	return Window{From: from.Format(DateFormat), To: asOf}
}

// reached compares without dividing, so truncation never decides a state.
func reached(value, threshold money.IDR, atBP int64) bool {
	return int64(value)*10_000 >= atBP*int64(threshold)
}

func percentBP(value, threshold money.IDR) int64 {
	if threshold.IsZero() {
		return 0
	}
	return int64(value) * 10_000 / int64(threshold)
}

// state walks OK → WATCH → WARN → CROSSED, crossed first.
//
// Crossed is decided by whether the year ever reached the threshold, not by
// where the cumulative sits now — that is the whole of the stickiness rule
// (SPEC §5.2). The three below it read the current figure, because those are
// warnings about where the year is heading and a year that took returns really
// is further from the line than it was.
func state(cumulative money.IDR, crossedOn string, t Threshold) State {
	switch {
	case crossedOn != "":
		return Crossed
	case reached(cumulative, t.AmountIDR, t.WarnBP):
		return Warn
	case reached(cumulative, t.AmountIDR, t.WatchBP):
		return Watch
	default:
		return OK
	}
}

// deadlines derives the two dates a crossing produces (SPEC §5.2).
//
// The gap between them is the most misunderstood part of this rule and most of
// this feature's value: registration is one date, and the day PPN actually has
// to be charged is another and later one. A business told only the first
// registers and then starts collecting tax it does not yet owe; told only the
// second, it misses the registration.
//
// Which pair of policies is correct is not settled — see [RegisterByPolicy].
// Both are implemented and the choice is config.
func deadlines(cal Calendar, bookYear int, crossedOn string, t Threshold) (registerBy, vatStarts string) {
	crossed, err := time.Parse(DateFormat, crossedOn)
	if err != nil {
		return "", ""
	}

	switch t.RegisterBy {
	case RegisterByEndOfBookYear:
		// PMK 164/2023 Pasal 17(3), as SPEC §5.2 reads it.
		registerBy = cal.BookYearWindow(bookYear).To
	case RegisterByEndOfFollowingMonth:
		// PMK 197/2013 Pasal 4: the end of the month after the month the
		// threshold was passed.
		registerBy = endOfMonth(crossed.AddDate(0, 1, 0)).Format(DateFormat)
	}

	switch t.VATStarts {
	case VATStartsNextBookYear:
		// The first tax period of the following book year (Pasal 18).
		vatStarts = cal.BookYearWindow(bookYear + 1).From
	case VATStartsAfterRegistration:
		day, err := time.Parse(DateFormat, registerBy)
		if err != nil {
			return registerBy, ""
		}
		vatStarts = day.AddDate(0, 0, 1).Format(DateFormat)
	}
	return registerBy, vatStarts
}

func clampDate(day string, w Window) string {
	switch {
	case day < w.From:
		return w.From
	case day > w.To:
		return w.To
	default:
		return day
	}
}

func min2(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func max2(a, b string) string {
	if a > b {
		return a
	}
	return b
}
