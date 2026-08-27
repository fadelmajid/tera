package margin

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidPeriod is returned for a malformed or inverted reporting window.
	ErrInvalidPeriod = errors.New("margin: invalid period")

	// ErrPolicyUnset is returned when no returns-period rule was supplied.
	//
	// Deliberately not defaultable. Which period a cross-boundary return lands
	// in decides whose money moves and when (SPEC §4.4); picking one here would
	// mean this package quietly chose it on the user's behalf.
	ErrPolicyUnset = errors.New("margin: returns-period rule is not set")

	// ErrUnknownReturnRule is returned for a stored rule string outside the two.
	ErrUnknownReturnRule = errors.New("margin: unknown returns-period rule")

	// ErrUnknownOwner is returned when a sale or layer names an owner the
	// caller supplied no name for.
	//
	// Refused rather than rendered as an id. A settlement screen showing a UUID
	// where a family member's name belongs is not a cosmetic problem.
	ErrUnknownOwner = errors.New("margin: owner has no name")

	// ErrOwnerMismatch is returned when a sale line and the layers it drew
	// disagree about whose product it was.
	//
	// INV-8. Revenue is attributed from the sale line, COGS from the stock
	// layer, and both are snapshots written at different times. If they ever
	// disagree, one family member's revenue is sitting against another's cost
	// and no total on the page is trustworthy.
	ErrOwnerMismatch = errors.New("margin: owner attribution disagrees between revenue and cost")

	// ErrCOGSMismatch is returned when the cost recorded on a sale line does
	// not equal the layer draws behind it.
	ErrCOGSMismatch = errors.New("margin: recorded cost does not match the layers drawn")

	// ErrOrphanDraw is returned when a layer draw belongs to no line of the
	// sale that made it.
	ErrOrphanDraw = errors.New("margin: layer draw belongs to no sale line")

	// ErrInvalidRecord is returned for a structurally impossible input row.
	ErrInvalidRecord = errors.New("margin: invalid record")
)

// OwnerMismatchError names both sides of a disagreement, so the report can say
// which sale and which product to look at rather than only that something is
// wrong.
type OwnerMismatchError struct {
	SaleID    string
	InvoiceNo string
	ProductID string
	// LineOwner is who the sale line says sold it; LayerOwner is who the stock
	// layer says owned it.
	LineOwner  OwnerID
	LayerOwner OwnerID
}

func (e *OwnerMismatchError) Error() string {
	return fmt.Sprintf(
		"margin: sale %s (%s) product %s: revenue is attributed to %s but the stock drawn belongs to %s",
		e.SaleID, e.InvoiceNo, e.ProductID, e.LineOwner, e.LayerOwner,
	)
}

func (e *OwnerMismatchError) Unwrap() error { return ErrOwnerMismatch }
