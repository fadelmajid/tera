package omzet

import (
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// EventType is what put a row in the ledger (SPEC §5.1).
type EventType string

// The events that move turnover.
const (
	// Sale is turnover arriving. Positive.
	Sale EventType = "SALE"
	// Void says a sale did not happen. Negative, and dated to the original
	// sale's day: the turnover has to be removed from where it was counted,
	// which may be a different book year from the day somebody noticed
	// (SPEC §5.4).
	Void EventType = "VOID"
	// Refund and Return are goods coming back after the sale happened.
	// Negative, and dated to the day they came back — the turnover did occur,
	// and is being reduced now (D-012 decides the same question for margin).
	Refund EventType = "REFUND"
	Return EventType = "RETURN"
	// Adjustment is a correction with no transaction behind it.
	Adjustment EventType = "ADJUSTMENT"
)

// Valid reports whether e is one of the five.
func (e EventType) Valid() bool {
	switch e {
	case Sale, Void, Refund, Return, Adjustment:
		return true
	default:
		return false
	}
}

// Entry is one append-only ledger row (SPEC §5.1).
//
// Append-only, mirroring the FIFO layers: a correction is another row, never an
// edit. The threshold is a legal event with a date attached, and a ledger that
// can be edited cannot answer when it was crossed.
type Entry struct {
	ID string

	// BookYear is denormalised at write time from EffectiveDate, in the
	// entity's zone (D-005). Carried rather than derived on read so a query can
	// filter a year without every row being parsed — and cross-checked against
	// the calendar on every read, because a row whose stored year disagrees
	// with its date is the exact bug INV-5 is about.
	BookYear int

	// EffectiveDate is the day this counts on, which is not always the day it
	// was written. A void in January of a December sale carries December's
	// date (SPEC §5.4).
	EffectiveDate string

	Type EventType

	// Amount is signed: positive for turnover, negative for a reversal.
	Amount money.IDR

	// SourceTxnID is the sale, return or void behind the row, so every figure
	// decomposes into the documents that produced it.
	SourceTxnID string
}

// validate checks one row against the calendar it was written under.
func (e Entry) validate(cal Calendar) error {
	if !e.Type.Valid() {
		return fmt.Errorf("%w %s: event type %q is not one of the five",
			ErrInvalidEntry, e.ID, e.Type)
	}
	if _, err := time.Parse(DateFormat, e.EffectiveDate); err != nil {
		return fmt.Errorf("%w %s: effective date %q is not a YYYY-MM-DD date",
			ErrInvalidEntry, e.ID, e.EffectiveDate)
	}

	// The integrity check INV-5 earns. A row stamped book year 2026 whose date
	// resolves to 2027 means something wrote it in the wrong zone or under a
	// different book-year start, and every figure built on it is against the
	// wrong window. Refusing is the only honest answer: the threshold resets
	// between those two years.
	want, err := cal.BookYear(e.EffectiveDate)
	if err != nil {
		return err
	}
	if e.BookYear != want {
		return fmt.Errorf(
			"%w %s: stored as book year %d but %s falls in book year %d",
			ErrInvalidEntry, e.ID, e.BookYear, e.EffectiveDate, want)
	}

	// Turnover is positive, reversals are negative. A sale that reduces
	// turnover or a void that increases it is a sign flip somewhere upstream,
	// and it would move the crossing date.
	switch e.Type {
	case Sale:
		if e.Amount.IsNegative() {
			return fmt.Errorf("%w %s: a sale of %s reduces turnover", ErrInvalidEntry, e.ID, e.Amount)
		}
	case Void, Refund, Return:
		if e.Amount.IsPositive() {
			return fmt.Errorf("%w %s: a %s of %s increases turnover", ErrInvalidEntry, e.ID, e.Type, e.Amount)
		}
	case Adjustment:
		// Signed either way by definition; that is what makes it an
		// adjustment rather than one of the four above.
	}
	return nil
}
