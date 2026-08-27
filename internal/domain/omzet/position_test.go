package omzet_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/omzet"
)

// The seeded figure: Rp 4,8 miliar (PMK 197/2013).
const threshold = money.IDR(4_800_000_000)

func thresholds(t *testing.T, mutate ...func(*omzet.Threshold)) omzet.ThresholdSet {
	t.Helper()

	row := omzet.Threshold{
		ID: "th-1", EntityID: "entity-nonpkp", AmountIDR: threshold,
		WatchBP: 7_000, WarnBP: 9_000,
		RegisterBy: omzet.RegisterByEndOfBookYear,
		VATStarts:  omzet.VATStartsNextBookYear,
		ValidFrom:  "2014-01-01", LegalRef: "PMK 197/2013",
	}
	for _, m := range mutate {
		m(&row)
	}
	set, err := omzet.NewThresholdSet(row)
	if err != nil {
		t.Fatalf("threshold set: %v", err)
	}
	return set
}

// sale builds a turnover row on a date.
func sale(id, date string, amount money.IDR) omzet.Entry {
	return omzet.Entry{
		ID: id, BookYear: yearOf(date), EffectiveDate: date,
		Type: omzet.Sale, Amount: amount, SourceTxnID: "sale-" + id,
	}
}

// reversal builds a negative row of a given type on a date.
func reversal(id, date string, kind omzet.EventType, amount money.IDR) omzet.Entry {
	return omzet.Entry{
		ID: id, BookYear: yearOf(date), EffectiveDate: date,
		Type: kind, Amount: amount.Neg(), SourceTxnID: "rev-" + id,
	}
}

// yearOf is the January-start book year of a date, for building fixtures.
func yearOf(date string) int {
	var y, m, d int
	if _, err := fmt.Sscanf(date, "%4d-%2d-%2d", &y, &m, &d); err != nil {
		panic(err)
	}
	return y
}

func compute(t *testing.T, year int, asOf string, entries ...omzet.Entry) omzet.Position {
	t.Helper()

	got, err := omzet.Compute(omzet.Input{
		EntityID: "entity-nonpkp", Calendar: jakarta(t, 1), Thresholds: thresholds(t),
		BookYear: year, AsOf: asOf, Entries: entries,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return got
}

// TestTheBookYearCounterIsTheAuthoritativeFigure is SPEC §5.1.
//
// The threshold is cumulative per book year and resets annually (PMK 197/2013).
// Common guidance says rolling twelve months and is wrong, so the two figures
// are computed over different windows and returned under names that cannot be
// mistaken for each other.
func TestTheBookYearCounterIsTheAuthoritativeFigure(t *testing.T) {
	t.Parallel()

	got := compute(t, 2026, "2026-06-30",
		// Last book year. Inside the trailing twelve months, outside the
		// counter that matters.
		sale("a", "2025-09-01", 2_000_000_000),
		sale("b", "2025-12-31", 1_000_000_000),
		// This book year.
		sale("c", "2026-01-02", 500_000_000),
		sale("d", "2026-06-30", 300_000_000),
	)

	if got.Cumulative != 800_000_000 {
		t.Errorf("cumulative = %s, want only this book year's Rp 800.000.000", got.Cumulative)
	}
	// The estimate spans the boundary the legal figure resets on: that is the
	// whole difference between them, and it is why one of them is not the law.
	if got.Trailing12 != 3_800_000_000 {
		t.Errorf("trailing twelve = %s, want Rp 3.800.000.000", got.Trailing12)
	}
	if got.Trailing12Window.From != "2025-07-01" || got.Trailing12Window.To != "2026-06-30" {
		t.Errorf("trailing window = %s", got.Trailing12Window)
	}
	if got.Window.From != "2026-01-01" || got.Window.To != "2026-12-31" {
		t.Errorf("book year window = %s", got.Window)
	}

	// Read as a rolling twelve months this business looks 79% of the way to
	// registering. Read correctly it is 16%, and the difference is a year of
	// planning.
	if got.PercentBP != 1_666 {
		t.Errorf("percent = %d bp, want 1666", got.PercentBP)
	}
	if got.State != omzet.OK {
		t.Errorf("state = %s, want OK", got.State)
	}
	if got.Entries != 2 {
		t.Errorf("built from %d rows, want the 2 inside the book year", got.Entries)
	}
}

// TestTheAlarmWalksTheLadder is SPEC §5.2.
func TestTheAlarmWalksTheLadder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cumulative money.IDR
		want       omzet.State
	}{
		{"nothing sold", 0, omzet.OK},
		{"just under seventy per cent", 3_359_999_999, omzet.OK},
		{"exactly seventy per cent", 3_360_000_000, omzet.Watch},
		{"just under ninety", 4_319_999_999, omzet.Watch},
		{"exactly ninety per cent", 4_320_000_000, omzet.Warn},
		{"one rupiah under the threshold", 4_799_999_999, omzet.Warn},
		{"exactly the threshold", 4_800_000_000, omzet.Crossed},
		{"well past it", 6_000_000_000, omzet.Crossed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := compute(t, 2026, "2026-12-31", sale("a", "2026-03-01", tt.cumulative))
			if got.State != tt.want {
				t.Fatalf("%s at %s, want %s", got.State, tt.cumulative, tt.want)
			}
			if tt.want == omzet.Crossed && got.CrossedOn != "2026-03-01" {
				t.Errorf("crossed on %q, want the day it happened", got.CrossedOn)
			}
			if tt.want != omzet.Crossed && got.CrossedOn != "" {
				t.Errorf("not crossed but carries a crossing date of %q", got.CrossedOn)
			}
		})
	}
}

// TestCrossingIsStickyWhenARefundDropsItBackUnder is SPEC §5.2.
//
// The legal event happened. A business that reached Rp 4,8 miliar in September
// became obliged to register then, and goods coming back in November do not
// unhappen it — so the state stays CROSSED while the cumulative honestly falls
// below the line.
func TestCrossingIsStickyWhenARefundDropsItBackUnder(t *testing.T) {
	t.Parallel()

	got := compute(t, 2026, "2026-12-31",
		sale("a", "2026-01-15", 4_000_000_000),
		sale("b", "2026-09-15", 850_000_000), // 4.850.000.000 — over
		reversal("c", "2026-11-02", omzet.Return, 200_000_000),
	)

	if got.State != omzet.Crossed {
		t.Fatalf("state = %s, want CROSSED — a return does not undo a crossing", got.State)
	}
	if got.CrossedOn != "2026-09-15" {
		t.Errorf("crossed on %s, want 2026-09-15", got.CrossedOn)
	}
	// And the cumulative is reported honestly, below the threshold, rather than
	// held up at it to make the state look consistent.
	if got.Cumulative != 4_650_000_000 {
		t.Errorf("cumulative = %s, want Rp 4.650.000.000", got.Cumulative)
	}
	if !got.Remaining.IsPositive() {
		t.Errorf("remaining = %s, want a positive figure below the threshold", got.Remaining)
	}
	// The peak is what makes that pair explicable on screen: crossed in
	// September at Rp 4.850.000.000, standing at Rp 4.650.000.000 now.
	if got.Peak != 4_850_000_000 || got.PeakOn != "2026-09-15" {
		t.Errorf("peak = %s on %s, want Rp 4.850.000.000 on 2026-09-15", got.Peak, got.PeakOn)
	}
}

// TestAVoidOfTheCrossingSaleMeansTheYearNeverCrossed is the other side of the
// same rule, and the reason a void and a return are different event types.
//
// A void says the sale did not happen. Turnover that did not happen was never
// turnover, so it is removed from the day it was counted on — and if that day
// then never reached the threshold, the year genuinely never crossed. A return
// says the sale did happen and goods came back later, which is a different fact
// about a different day.
func TestAVoidOfTheCrossingSaleMeansTheYearNeverCrossed(t *testing.T) {
	t.Parallel()

	entries := []omzet.Entry{
		sale("a", "2026-01-15", 4_000_000_000),
		sale("b", "2026-09-15", 850_000_000),
	}
	if crossed := compute(t, 2026, "2026-12-31", entries...); crossed.State != omzet.Crossed {
		t.Fatalf("the fixture does not cross: %s", crossed.State)
	}

	// The void carries the original sale's date (SPEC §5.4), so it lands back
	// on the day it is undoing.
	voided := compute(t, 2026, "2026-12-31",
		append(entries, reversal("c", "2026-09-15", omzet.Void, 850_000_000))...)

	if voided.State == omzet.Crossed {
		t.Errorf("a voided sale still crossed the threshold on %s", voided.CrossedOn)
	}
	if voided.CrossedOn != "" {
		t.Errorf("crossed on %q, want no crossing at all", voided.CrossedOn)
	}
	if voided.Cumulative != 4_000_000_000 {
		t.Errorf("cumulative = %s, want the Rp 4.000.000.000 that did happen", voided.Cumulative)
	}
	// Rp 4.000.000.000 of Rp 4.800.000.000 is 83%: past the 70% watch line and
	// short of the 90% warning.
	if voided.State != omzet.Watch {
		t.Errorf("state = %s, want WATCH at 83%%", voided.State)
	}
}

// TestVoidingADecemberSaleInJanuaryDecrementsThePriorBookYear is TASKS 7.6 and
// SPEC §5.4.
//
// The void is written in January and counts in December, because effective_date
// is the original sale's. Getting this wrong takes turnover off the wrong side
// of a threshold that resets between the two — the new year opens short and the
// old one closes over.
func TestVoidingADecemberSaleInJanuaryDecrementsThePriorBookYear(t *testing.T) {
	t.Parallel()

	entries := []omzet.Entry{
		sale("a", "2026-06-01", 4_000_000_000),
		sale("b", "2026-12-20", 900_000_000), // crosses, on the last month of the year
		// Noticed and voided on 8 January, dated 20 December.
		reversal("c", "2026-12-20", omzet.Void, 900_000_000),
		sale("d", "2027-01-05", 100_000_000),
	}

	closing := compute(t, 2026, "2027-01-31", entries...)
	if closing.Cumulative != 4_000_000_000 {
		t.Errorf("2026 closed at %s, want Rp 4.000.000.000 after the void", closing.Cumulative)
	}
	if closing.State == omzet.Crossed {
		t.Errorf("2026 is still CROSSED on a sale that did not happen")
	}

	opening := compute(t, 2027, "2027-01-31", entries...)
	if opening.Cumulative != 100_000_000 {
		t.Errorf("2027 opened at %s, want only its own Rp 100.000.000 — the December void "+
			"must not land here", opening.Cumulative)
	}
	if opening.Entries != 1 {
		t.Errorf("2027 was built from %d rows, want 1", opening.Entries)
	}
}

// TestCrossingEmitsBothDates is TASKS 7.9 and SPEC §5.2.
//
// The gap between them is the point. A business told only the registration date
// registers and starts charging PPN it does not yet owe; told only the
// obligation date, it misses the registration entirely.
func TestCrossingEmitsBothDates(t *testing.T) {
	t.Parallel()

	got := compute(t, 2026, "2026-12-31", sale("a", "2026-07-14", 5_000_000_000))

	if got.State != omzet.Crossed || got.CrossedOn != "2026-07-14" {
		t.Fatalf("state %s crossed on %q", got.State, got.CrossedOn)
	}
	// PMK 164/2023 Pasal 17(3) as SPEC §5.2 reads it: the end of the book year
	// the crossing happened in.
	if got.RegisterBy != "2026-12-31" {
		t.Errorf("register by %s, want the end of the book year", got.RegisterBy)
	}
	// Pasal 18: the first tax period of the following book year.
	if got.VATStarts != "2027-01-01" {
		t.Errorf("VAT starts %s, want the first day of the next book year", got.VATStarts)
	}
	// Five and a half months apart, and neither is the other.
	if got.RegisterBy >= got.VATStarts {
		t.Errorf("the two dates do not leave a gap: %s and %s", got.RegisterBy, got.VATStarts)
	}
}

// TestBothDeadlinePoliciesAreImplemented, so answering the open question is a
// config row rather than a release.
//
// Which one is correct is not settled — SPEC §5.2 cites PMK 164/2023, which
// governs the final-PPh regime, while PKP registration itself is usually quoted
// from PMK 197/2013 Pasal 4 with a far shorter deadline. Both are implemented
// and the choice is a config row (INV-4), so answering the question is a row
// rather than a release.
func TestBothDeadlinePoliciesAreImplemented(t *testing.T) {
	t.Parallel()

	shorter := thresholds(t, func(th *omzet.Threshold) {
		th.RegisterBy = omzet.RegisterByEndOfFollowingMonth
		th.VATStarts = omzet.VATStartsAfterRegistration
	})

	got, err := omzet.Compute(omzet.Input{
		EntityID: "entity-nonpkp", Calendar: jakarta(t, 1), Thresholds: shorter,
		BookYear: 2026, AsOf: "2026-12-31",
		Entries: []omzet.Entry{sale("a", "2026-07-14", 5_000_000_000)},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Crossed 14 July, so the end of the month after is 31 August, and the
	// obligation runs from 1 September — four months earlier than the other
	// reading, on the same facts.
	if got.RegisterBy != "2026-08-31" {
		t.Errorf("register by %s, want 2026-08-31", got.RegisterBy)
	}
	if got.VATStarts != "2026-09-01" {
		t.Errorf("VAT starts %s, want 2026-09-01", got.VATStarts)
	}
}

// TestDeadlinesFollowANonJanuaryBookYear: both dates move with the book year,
// not with the calendar year.
func TestDeadlinesFollowANonJanuaryBookYear(t *testing.T) {
	t.Parallel()

	cal := jakarta(t, 4)
	got, err := omzet.Compute(omzet.Input{
		EntityID: "entity-nonpkp", Calendar: cal, Thresholds: thresholds(t),
		BookYear: 2026, AsOf: "2027-03-31",
		Entries: []omzet.Entry{{
			ID: "a", BookYear: 2026, EffectiveDate: "2026-11-20",
			Type: omzet.Sale, Amount: 5_000_000_000,
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if got.RegisterBy != "2027-03-31" {
		t.Errorf("register by %s, want the end of a book year that closes in March", got.RegisterBy)
	}
	if got.VATStarts != "2027-04-01" {
		t.Errorf("VAT starts %s, want the first day of the next book year", got.VATStarts)
	}
}

// TestCrossingInMonthSeven is TASKS 7.11: a simulated year with trade in every
// month, crossing in the seventh, checked end to end.
func TestCrossingInMonthSeven(t *testing.T) {
	t.Parallel()

	// Rp 700.000.000 a month. Seven of them is Rp 4,9 miliar, so the crossing
	// lands in July and not before: six months is Rp 4,2 miliar, which is 87%.
	var entries []omzet.Entry
	for month := 1; month <= 12; month++ {
		entries = append(entries, sale(
			fmt.Sprintf("m%02d", month),
			fmt.Sprintf("2026-%02d-20", month),
			700_000_000,
		))
	}

	// Read at the end of June the year is not there yet — and the clock counts
	// to the day it is read on, not to the end of a year that has not happened.
	june := compute(t, 2026, "2026-06-30", entries...)
	if june.State != omzet.Watch {
		t.Errorf("end of June: %s at %s, want WATCH at 87.5%%", june.State, june.Cumulative)
	}
	if june.CountedTo != "2026-06-30" {
		t.Errorf("end of June counted to %s", june.CountedTo)
	}
	if june.Cumulative != 4_200_000_000 || june.PercentBP != 8_750 {
		t.Errorf("end of June: %s (%d bp)", june.Cumulative, june.PercentBP)
	}
	if june.CrossedOn != "" {
		t.Errorf("end of June already crossed on %s", june.CrossedOn)
	}

	// Read at the end of the year, it crossed on the July sale and nothing
	// after it moves that date.
	got := compute(t, 2026, "2026-12-31", entries...)
	if got.State != omzet.Crossed {
		t.Fatalf("state = %s, want CROSSED", got.State)
	}
	if got.CrossedOn != "2026-07-20" {
		t.Errorf("crossed on %s, want the July sale", got.CrossedOn)
	}
	if got.Cumulative != 8_400_000_000 {
		t.Errorf("cumulative = %s, want a full year at Rp 700.000.000 a month", got.Cumulative)
	}
	if got.RegisterBy != "2026-12-31" || got.VATStarts != "2027-01-01" {
		t.Errorf("dates = %s and %s", got.RegisterBy, got.VATStarts)
	}
	// The next book year opens at zero. That is what "reset annually" means and
	// it is the half of the rule the rolling-twelve-months reading loses.
	next := compute(t, 2027, "2027-01-31", entries...)
	if !next.Cumulative.IsZero() || next.State != omzet.OK {
		t.Errorf("2027 opened at %s in state %s, want zero and OK", next.Cumulative, next.State)
	}
}

// TestARowStampedWithTheWrongBookYearIsRefused is the integrity check INV-5
// earns.
//
// book_year is denormalised at write time. A row whose stored year disagrees
// with what its date resolves to means something wrote it in the wrong zone or
// under a different book-year start — and the threshold resets between those
// two years, so every figure built on it is measured against the wrong window.
func TestARowStampedWithTheWrongBookYearIsRefused(t *testing.T) {
	t.Parallel()

	_, err := omzet.Compute(omzet.Input{
		EntityID: "entity-nonpkp", Calendar: jakarta(t, 1), Thresholds: thresholds(t),
		BookYear: 2026, AsOf: "2026-12-31",
		Entries: []omzet.Entry{{
			ID: "wrong", BookYear: 2026, EffectiveDate: "2027-01-05",
			Type: omzet.Sale, Amount: 1_000_000,
		}},
	})
	if !errors.Is(err, omzet.ErrInvalidEntry) {
		t.Fatalf("want ErrInvalidEntry, got %v", err)
	}
}

func TestComputeRejectsWhatCannotBeTrue(t *testing.T) {
	t.Parallel()

	base := func() omzet.Input {
		return omzet.Input{
			EntityID: "entity-nonpkp", Calendar: jakarta(t, 1), Thresholds: thresholds(t),
			BookYear: 2026, AsOf: "2026-12-31",
			Entries: []omzet.Entry{sale("a", "2026-03-01", 1_000_000)},
		}
	}

	tests := []struct {
		name   string
		mutate func(*omzet.Input)
		want   error
	}{
		{
			name:   "as-of is not a date",
			mutate: func(in *omzet.Input) { in.AsOf = "akhir tahun" },
			want:   omzet.ErrInvalidDate,
		},
		{
			name:   "no timezone",
			mutate: func(in *omzet.Input) { in.Calendar = omzet.Calendar{StartMonth: 1} },
			want:   omzet.ErrInvalidCalendar,
		},
		{
			// INV-4: a threshold is a legal figure and this package has no
			// fallback for one. An omzet clock running against a number nobody
			// configured is a number the owner plans around and cannot check.
			name:   "no threshold in force",
			mutate: func(in *omzet.Input) { in.Thresholds = omzet.ThresholdSet{} },
			want:   omzet.ErrNoThreshold,
		},
		{
			name:   "another company's threshold",
			mutate: func(in *omzet.Input) { in.EntityID = "entity-pkp" },
			want:   omzet.ErrInvalidThreshold,
		},
		{
			name:   "a sale that reduces turnover",
			mutate: func(in *omzet.Input) { in.Entries[0].Amount = -1 },
			want:   omzet.ErrInvalidEntry,
		},
		{
			name: "a void that increases it",
			mutate: func(in *omzet.Input) {
				in.Entries[0].Type, in.Entries[0].Amount = omzet.Void, 1
			},
			want: omzet.ErrInvalidEntry,
		},
		{
			name:   "an event nobody recognises",
			mutate: func(in *omzet.Input) { in.Entries[0].Type = "HIBAH" },
			want:   omzet.ErrInvalidEntry,
		},
		{
			// The caller declared a range that cannot cover both windows, so
			// the trailing figure would be quietly short — and a wrong momentum
			// estimate is worse than none.
			name: "entries that cannot cover both windows",
			mutate: func(in *omzet.Input) {
				// Mid-year, so the trailing twelve months reach back into the
				// previous book year and the declared range does not.
				in.AsOf = "2026-06-30"
				in.Supplied = omzet.Window{From: "2026-01-01", To: "2026-12-31"}
			},
			want: omzet.ErrInvalidEntry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := base()
			tt.mutate(&in)
			if _, err := omzet.Compute(in); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

// TestAnEmptyLedgerIsAReportNotAnError. A company that has not traded is at
// zero, not broken.
func TestAnEmptyLedgerIsAReportNotAnError(t *testing.T) {
	t.Parallel()

	got := compute(t, 2026, "2026-12-31")

	if !got.Cumulative.IsZero() || got.State != omzet.OK || got.Entries != 0 {
		t.Errorf("empty ledger produced %s / %s / %d rows", got.Cumulative, got.State, got.Entries)
	}
	if got.Remaining != threshold {
		t.Errorf("remaining = %s, want the whole threshold", got.Remaining)
	}
	// The threshold travels with the report so the screen can cite the
	// regulation rather than assert a number.
	if got.Threshold.LegalRef != "PMK 197/2013" {
		t.Errorf("the report does not carry its legal reference: %+v", got.Threshold)
	}
}
