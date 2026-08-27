package aging

import (
	"errors"
	"fmt"
)

// Sentinel errors. Messages are English because they are code (CLAUDE.md); the
// UI is in Bahasa Indonesia and builds its own wording from the exported
// fields.
var (
	// ErrInvalidInput means the request could not be true.
	ErrInvalidInput = errors.New("aging: invalid request")

	// ErrInvalidItem means one of the balances could not be true.
	ErrInvalidItem = errors.New("aging: invalid balance")
)

// InconsistentBalanceError reports a document whose outstanding figure does not
// equal its invoice less what has been paid.
//
// The database derives outstanding in a view rather than storing it, precisely
// so the two cannot drift (migration 008). Checking it again here costs nothing
// and turns a corrupted read into a refusal rather than an aging report that
// chases a supplier for the wrong amount.
type InconsistentBalanceError struct {
	ItemID      string
	DocumentNo  string
	Amount      int64
	Paid        int64
	Outstanding int64
}

// Error implements error.
func (e *InconsistentBalanceError) Error() string {
	return fmt.Sprintf(
		"aging: document %s (%s) is %d less %d paid, which is %d, but reports %d outstanding",
		e.ItemID, e.DocumentNo, e.Amount, e.Paid, e.Amount-e.Paid, e.Outstanding,
	)
}

// Unwrap lets errors.Is(err, ErrInvalidItem) match.
func (e *InconsistentBalanceError) Unwrap() error { return ErrInvalidItem }
