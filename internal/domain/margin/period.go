package margin

import (
	"fmt"
	"time"
)

// DateFormat is the business-date form every dated table stores: 'YYYY-MM-DD'
// in the entity's timezone (D-005).
//
// Dates arrive here already resolved. This package never turns an instant into
// a day: that conversion needs the entity's zone and its book-year start, and
// guessing at either is how a 31 December sale lands in the wrong year (INV-5).
const DateFormat = "2006-01-02"

// Period is an inclusive range of business dates — normally one calendar month,
// because that is the cadence money is settled on (R2.4).
type Period struct {
	// From and To are inclusive 'YYYY-MM-DD' business dates.
	From string
	To   string
}

// Month builds the settlement window for one calendar month.
func Month(year int, month time.Month) Period {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	return Period{From: first.Format(DateFormat), To: last.Format(DateFormat)}
}

// Contains reports whether a business date falls inside the period.
//
// String comparison, not parsing: ISO-8601 dates sort lexicographically, which
// is the same reason the column is TEXT rather than an integer. Validate does
// the parsing once, at the edge.
func (p Period) Contains(businessDate string) bool {
	return businessDate >= p.From && businessDate <= p.To
}

// String renders the window the way the report heading does.
func (p Period) String() string { return p.From + " … " + p.To }

// Validate rejects a malformed or inverted window.
func (p Period) Validate() error {
	if _, err := time.Parse(DateFormat, p.From); err != nil {
		return fmt.Errorf("%w: from %q is not a YYYY-MM-DD date", ErrInvalidPeriod, p.From)
	}
	if _, err := time.Parse(DateFormat, p.To); err != nil {
		return fmt.Errorf("%w: to %q is not a YYYY-MM-DD date", ErrInvalidPeriod, p.To)
	}
	if p.From > p.To {
		return fmt.Errorf("%w: %q is after %q", ErrInvalidPeriod, p.From, p.To)
	}
	return nil
}
