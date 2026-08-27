package omzet_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/omzet"
)

func jakarta(t *testing.T, startMonth int) omzet.Calendar {
	t.Helper()

	cal, err := omzet.NewCalendar("Asia/Jakarta", startMonth)
	if err != nil {
		t.Fatalf("calendar: %v", err)
	}
	return cal
}

// TestTheBookYearBoundaryIsResolvedInTheEntitysZone is TASKS 7.3 and INV-5.
//
// The threshold resets between book years, so an instant landing on the wrong
// side of midnight lands against the wrong year's counter. WIB is UTC+7, which
// means every instant between midnight and 07:00 local is still the previous
// day in UTC — and on 1 January that is the previous *book year*.
//
// SPEC §5.4 names 23:30 on 31 December. That one is safe in either zone, which
// is exactly why it is not sufficient on its own: the case that catches a UTC
// bug is the one seven hours later.
func TestTheBookYearBoundaryIsResolvedInTheEntitysZone(t *testing.T) {
	t.Parallel()

	cal := jakarta(t, 1)
	wib := cal.Location

	tests := []struct {
		name     string
		at       time.Time
		wantDate string
		wantYear int
	}{
		{
			// SPEC §5.4's case. 16:30 UTC, so both zones agree — the sale
			// belongs to the closing year and does.
			name:     "23:30 WIB on 31 December closes the old book year",
			at:       time.Date(2026, 12, 31, 23, 30, 0, 0, wib),
			wantDate: "2026-12-31", wantYear: 2026,
		},
		{
			// The one that matters. 17:30 UTC on 31 December: read in UTC this
			// sale lands in book year 2026, against a counter that has already
			// closed, and the new year opens one sale short.
			name:     "00:30 WIB on 1 January opens the new one",
			at:       time.Date(2027, 1, 1, 0, 30, 0, 0, wib),
			wantDate: "2027-01-01", wantYear: 2027,
		},
		{
			name:     "06:59 WIB on 1 January is still the new year",
			at:       time.Date(2027, 1, 1, 6, 59, 0, 0, wib),
			wantDate: "2027-01-01", wantYear: 2027,
		},
		{
			// The same instant expressed in UTC, to prove the conversion is
			// the zone's and not the literal's.
			name:     "the same instant written as UTC resolves identically",
			at:       time.Date(2026, 12, 31, 17, 30, 0, 0, time.UTC),
			wantDate: "2027-01-01", wantYear: 2027,
		},
		{
			name:     "midday in the middle of the year",
			at:       time.Date(2026, 7, 15, 12, 0, 0, 0, wib),
			wantDate: "2026-07-15", wantYear: 2026,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cal.BusinessDate(tt.at); got != tt.wantDate {
				t.Errorf("business date = %s, want %s", got, tt.wantDate)
			}
			got, err := cal.BookYearAt(tt.at)
			if err != nil {
				t.Fatalf("BookYearAt: %v", err)
			}
			if got != tt.wantYear {
				t.Errorf("book year = %d, want %d", got, tt.wantYear)
			}
		})
	}
}

// TestANonJanuaryBookYear is TASKS 7.3's second half.
//
// A book year is labelled by the year it opens in, which is how a tahun buku is
// named: April 2026 to March 2027 is book year 2026, and a sale in February
// 2027 belongs to it rather than to 2027.
func TestANonJanuaryBookYear(t *testing.T) {
	t.Parallel()

	cal := jakarta(t, 4) // opens 1 April

	tests := []struct {
		date string
		want int
	}{
		{"2026-03-31", 2025}, // the last day of the previous book year
		{"2026-04-01", 2026}, // the first day of this one
		{"2026-12-31", 2026}, // a calendar-year boundary that is not a book-year one
		{"2027-01-01", 2026},
		{"2027-03-31", 2026}, // its last day
		{"2027-04-01", 2027},
	}

	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			t.Parallel()

			got, err := cal.BookYear(tt.date)
			if err != nil {
				t.Fatalf("BookYear: %v", err)
			}
			if got != tt.want {
				t.Errorf("book year = %d, want %d", got, tt.want)
			}
		})
	}

	if got := cal.BookYearWindow(2026); got.From != "2026-04-01" || got.To != "2027-03-31" {
		t.Errorf("book year 2026 runs %s, want 2026-04-01 … 2027-03-31", got)
	}
	// And the years abut with no gap and no overlap, which is what makes
	// "cumulative, reset annually" a partition rather than a hope.
	prev, next := cal.BookYearWindow(2025), cal.BookYearWindow(2026)
	if prev.To >= next.From {
		t.Errorf("book years %s and %s overlap", prev, next)
	}
	day, err := time.Parse(omzet.DateFormat, prev.To)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if day.AddDate(0, 0, 1).Format(omzet.DateFormat) != next.From {
		t.Errorf("a day falls between book years %s and %s", prev, next)
	}
}

func TestBookYearWindowForAJanuaryStart(t *testing.T) {
	t.Parallel()

	cal := jakarta(t, 1)
	if got := cal.BookYearWindow(2026); got.From != "2026-01-01" || got.To != "2026-12-31" {
		t.Errorf("book year 2026 runs %s, want the calendar year", got)
	}
	// A leap year still ends on 31 December.
	if got := cal.BookYearWindow(2028); got.To != "2028-12-31" {
		t.Errorf("book year 2028 ends %s", got.To)
	}
}

func TestCalendarRejectsWhatCannotBeTrue(t *testing.T) {
	t.Parallel()

	if _, err := omzet.NewCalendar("Mars/Olympus", 1); !errors.Is(err, omzet.ErrInvalidCalendar) {
		t.Errorf("unknown timezone: %v", err)
	}
	for _, month := range []int{0, 13, -1} {
		if _, err := omzet.NewCalendar("Asia/Jakarta", month); !errors.Is(err, omzet.ErrInvalidCalendar) {
			t.Errorf("start month %d: %v", month, err)
		}
	}

	cal := jakarta(t, 1)
	if _, err := cal.BookYear("31 Desember 2026"); !errors.Is(err, omzet.ErrInvalidDate) {
		t.Errorf("malformed date: %v", err)
	}
}
