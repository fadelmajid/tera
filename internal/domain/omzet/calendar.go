package omzet

import (
	"fmt"
	"time"
)

// DateFormat is the business-date form every dated table stores: 'YYYY-MM-DD'
// in the entity's timezone (D-005).
const DateFormat = "2006-01-02"

// Calendar resolves instants and dates into the book year they belong to.
//
// This is the one package that does convert an instant to a day, unlike
// domain/margin and domain/aging which take dates already resolved. It has to:
// the book year is the window the Rp 4.8 billion threshold is measured over
// (SPEC §5.1), and deciding which side of a boundary a sale falls on is exactly
// the question INV-5 exists about. Putting that conversion anywhere else would
// mean two places knew where a year ends.
type Calendar struct {
	// Location is the entity's timezone. Never UTC, and never the server's:
	// 23:30 on 31 December in Jakarta is 16:30 UTC on 31 December, but 00:30 on
	// 1 January in Jakarta is 17:30 UTC on 31 December — and read in UTC that
	// sale lands in the closing book year instead of the opening one, against a
	// threshold that resets between them.
	Location *time.Location

	// StartMonth is the month the book year opens, 1–12. January for most, but
	// it is configurable and the schema has carried the column since migration
	// 001 (SPEC §5.4).
	StartMonth int
}

// NewCalendar builds a calendar from an IANA zone name and a start month.
func NewCalendar(timezone string, startMonth int) (Calendar, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return Calendar{}, fmt.Errorf("%w: timezone %q is not known", ErrInvalidCalendar, timezone)
	}
	cal := Calendar{Location: loc, StartMonth: startMonth}
	if err := cal.Validate(); err != nil {
		return Calendar{}, err
	}
	return cal, nil
}

// Validate rejects a calendar that could not be true.
func (c Calendar) Validate() error {
	if c.Location == nil {
		return fmt.Errorf("%w: no timezone", ErrInvalidCalendar)
	}
	if c.StartMonth < 1 || c.StartMonth > 12 {
		return fmt.Errorf("%w: book year starts in month %d", ErrInvalidCalendar, c.StartMonth)
	}
	return nil
}

// BusinessDate is the day an instant falls on, in the entity's zone (INV-5).
func (c Calendar) BusinessDate(t time.Time) string {
	return t.In(c.Location).Format(DateFormat)
}

// BookYear is the book year a business date belongs to.
//
// Labelled by the year it opens in, which is how a tahun buku is named: a book
// year running April 2026 to March 2027 is book year 2026, and a sale in
// February 2027 belongs to it.
func (c Calendar) BookYear(businessDate string) (int, error) {
	day, err := time.Parse(DateFormat, businessDate)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a YYYY-MM-DD date", ErrInvalidDate, businessDate)
	}
	if int(day.Month()) >= c.StartMonth {
		return day.Year(), nil
	}
	return day.Year() - 1, nil
}

// BookYearAt is the book year an instant falls in, resolved in the entity's
// zone. The two conversions in one call, because doing them separately is where
// somebody eventually resolves the date in one zone and the year in another.
func (c Calendar) BookYearAt(t time.Time) (int, error) {
	return c.BookYear(c.BusinessDate(t))
}

// Window is an inclusive range of business dates.
type Window struct {
	From string
	To   string
}

// Contains reports whether a business date falls inside the window. String
// comparison: ISO-8601 dates sort lexicographically.
func (w Window) Contains(businessDate string) bool {
	return businessDate >= w.From && businessDate <= w.To
}

// String renders the window the way a report heading does.
func (w Window) String() string { return w.From + " … " + w.To }

// BookYearWindow is the span of one book year.
//
// For a January start, book year 2026 is 2026-01-01 to 2026-12-31. For an April
// start, book year 2026 is 2026-04-01 to 2027-03-31.
func (c Calendar) BookYearWindow(year int) Window {
	from := time.Date(year, time.Month(c.StartMonth), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0).AddDate(0, 0, -1)
	return Window{From: from.Format(DateFormat), To: to.Format(DateFormat)}
}

// Today is the current business date in the entity's zone.
func (c Calendar) Today(now time.Time) string { return c.BusinessDate(now) }

// endOfMonth is the last day of the month a date falls in.
func endOfMonth(day time.Time) time.Time {
	first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 1, 0).AddDate(0, 0, -1)
}
