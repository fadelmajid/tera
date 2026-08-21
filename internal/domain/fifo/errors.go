package fifo

import (
	"errors"
	"fmt"
)

// Sentinel errors, for callers that need to branch on the kind of failure
// rather than read its detail. Match with errors.Is; reach the detail with
// errors.As on the concrete types below.
//
// Messages here are English because they are code (CLAUDE.md). The UI is in
// Bahasa Indonesia and builds its own wording from the exported fields — never
// by showing Error() to a cashier.
var (
	// ErrInsufficientStock means the owner's layers could not cover the
	// request. It is always an error surfaced to the user and never a fallback
	// to another owner's stock (INV-8, SPEC §3.3).
	ErrInsufficientStock = errors.New("fifo: insufficient stock")

	// ErrInvalidRequest means the consumption request was malformed.
	ErrInvalidRequest = errors.New("fifo: invalid consumption request")

	// ErrInvalidLayer means a layer handed to the domain could not be true.
	ErrInvalidLayer = errors.New("fifo: invalid stock layer")

	// ErrInvalidAcquisition means the amounts paid for a batch of stock could
	// not be true.
	ErrInvalidAcquisition = errors.New("fifo: invalid acquisition")

	// ErrInvalidDraw means the consumption being reversed could not be true.
	ErrInvalidDraw = errors.New("fifo: invalid draw")

	// ErrOverReversal means more was sent back than the draw ever took.
	ErrOverReversal = errors.New("fifo: reversal exceeds what was drawn")
)

// OverReversalError reports an attempt to give back more than was taken.
//
// Returning four of three units sold is data entry to reject, not a correction
// (D-010). Left unchecked it would raise the layer's remaining quantity above
// what it ever held, and the margin reversal would exceed the margin.
type OverReversalError struct {
	DrawID  string
	LayerID string
	// Requested is what this reversal asked for.
	Requested int64
	// Taken is what the original draw took, and AlreadyBack is how much of it
	// earlier reversals have given back.
	Taken       int64
	AlreadyBack int64
}

// Error implements error.
func (e *OverReversalError) Error() string {
	return fmt.Sprintf(
		"fifo: cannot return %d units against draw %s: it took %d and %d has already gone back, leaving %d",
		e.Requested, e.DrawID, e.Taken, e.AlreadyBack, e.Taken-e.AlreadyBack,
	)
}

// Unwrap lets errors.Is(err, ErrOverReversal) match.
func (e *OverReversalError) Unwrap() error { return ErrOverReversal }

// Remaining is how much of the draw could still legitimately be reversed.
func (e *OverReversalError) Remaining() int64 { return e.Taken - e.AlreadyBack }

// InsufficientStockError reports that an owner's layers hold less than was
// asked for.
//
// This is the error that must never quietly become a draw on someone else's
// stock. Family members settle real money on these figures monthly (R2.4), so
// covering Budi's sale from Sari's layers moves money between two people who
// did not agree to it. Refusing is the correct behaviour even when the shelf
// visibly holds the goods.
//
// OtherOwnersAvailable exists to make the refusal explicable: "there are 40 on
// the shelf but only 3 are yours" is an answer a shopkeeper can act on, whereas
// a bare "out of stock" in front of a full shelf is the kind of thing that
// makes people stop trusting the system and start keeping a parallel notebook.
type InsufficientStockError struct {
	EntityID  string
	ProductID string
	OwnerID   OwnerID
	Requested int64
	// Available is the owner's own remaining stock, summed across their layers.
	Available int64
	// OtherOwnersAvailable is the same product held in the same entity under a
	// different attribution. Counted for the message only. Never drawn from.
	OtherOwnersAvailable int64
}

// Error implements error.
func (e *InsufficientStockError) Error() string {
	msg := fmt.Sprintf(
		"fifo: insufficient stock for owner %s on product %s in entity %s: requested %d, available %d",
		e.OwnerID, e.ProductID, e.EntityID, e.Requested, e.Available,
	)
	if e.OtherOwnersAvailable > 0 {
		msg += fmt.Sprintf(
			" (a further %d is held by other owners and is not drawn from)",
			e.OtherOwnersAvailable,
		)
	}
	return msg
}

// Unwrap lets errors.Is(err, ErrInsufficientStock) match.
func (e *InsufficientStockError) Unwrap() error { return ErrInsufficientStock }

// Short is the quantity the request fell short by.
func (e *InsufficientStockError) Short() int64 { return e.Requested - e.Available }
