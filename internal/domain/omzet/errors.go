package omzet

import (
	"errors"
	"fmt"
)

// Sentinel errors. Messages are English because they are code (CLAUDE.md); the
// UI is in Bahasa Indonesia and builds its own wording from the exported
// fields.
var (
	// ErrInvalidCalendar means the entity's timezone or book-year start could
	// not be true.
	ErrInvalidCalendar = errors.New("omzet: invalid calendar")

	// ErrInvalidDate means a business date could not be parsed.
	ErrInvalidDate = errors.New("omzet: invalid business date")

	// ErrInvalidEntry means a ledger row could not be true.
	ErrInvalidEntry = errors.New("omzet: invalid ledger entry")

	// ErrNoThreshold means no threshold was in force on the day.
	//
	// Refused rather than defaulted. A threshold is a legal figure that has
	// changed before and will again; a hardcoded fallback is exactly what INV-4
	// forbids, and an omzet clock running against a rate nobody configured
	// would be a number the owner plans around and cannot check.
	ErrNoThreshold = errors.New("omzet: no threshold in force")

	// ErrInvalidThreshold means a threshold config row could not be true.
	ErrInvalidThreshold = errors.New("omzet: invalid threshold")
)

// NoThresholdError says which day had no threshold configured.
type NoThresholdError struct {
	BusinessDate string
	BookYear     int
}

// Error implements error.
func (e *NoThresholdError) Error() string {
	return fmt.Sprintf(
		"omzet: no PKP threshold is in force on %s (book year %d), so turnover cannot be measured against one",
		e.BusinessDate, e.BookYear,
	)
}

// Unwrap lets errors.Is(err, ErrNoThreshold) match.
func (e *NoThresholdError) Unwrap() error { return ErrNoThreshold }
