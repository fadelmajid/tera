package fifo

import (
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// OwnerID is the family member a stock layer is attributed to.
//
// It is an attribution tag, not legal ownership (CLAUDE.md). The zero value is
// the company bucket — stock_layer.owner_id NULL — which is a real, reported
// attribution alongside the named owners (SPEC §4.3, R2.2), not a slot for
// "unknown".
//
// INV-8 admits no third state, and this type cannot enforce that on its own: an
// owner that was never resolved arrives here as "" and is indistinguishable
// from a deliberate company attribution. The place to catch that is where a
// product's owner is looked up, before a Request is built. Said plainly so that
// nobody later reads Company as a safe default for a missing value.
type OwnerID string

// Company is the bucket for stock the business holds outright rather than on
// behalf of a named family member.
const Company OwnerID = ""

// IsCompany reports whether o is the company bucket rather than a named owner.
func (o OwnerID) IsCompany() bool { return o == Company }

// String renders the owner for error messages and logs.
func (o OwnerID) String() string {
	if o.IsCompany() {
		return "company"
	}
	return string(o)
}

// Layer is one acquisition of stock: a batch that arrived at a known cost, at a
// known time, attributed to exactly one owner.
//
// Layers are append-only (INV-7). Nothing in this package returns a modified
// Layer, and nothing should ever write one back with a different QtyIn or
// CostTotal. Consumption is recorded as separate rows that reference the layer,
// which is what lets a margin figure decompose months later into the specific
// batches a sale drew from — the audit trail owner settlement depends on.
type Layer struct {
	ID         string
	EntityID   string
	ProductID  string
	OwnerID    OwnerID
	AcquiredAt time.Time

	// QtyIn is what the batch brought in. It never changes.
	QtyIn int64

	// QtyConsumed is Σ qty_out over the consumptions already recorded against
	// this layer.
	//
	// It is derived by the caller from stock_consumption rows. There is
	// deliberately no such column on stock_layer (SPEC §3.1) — a stored
	// remaining-quantity is a mutable balance, and a mutable balance is exactly
	// the thing INV-7 exists to prevent. A return appends a negative qty_out
	// (D-010), so this can fall as well as rise.
	QtyConsumed int64

	// CostTotal is what the batch cost in total, net of creditable PPN.
	//
	// The total, not a unit cost — see CostAt. And "net of creditable PPN" is
	// doing real work: the same supplier price produces three different totals
	// here depending on the entity and the faktur (SPEC §3.2). CostBasis is
	// what decides it.
	CostTotal money.IDR

	// FakturReceived records whether the supplier gave a faktur pajak (INV-9).
	//
	// Carried on the layer, not just on the purchase, because it is the
	// justification for CostTotal and the justification has to survive to the
	// audit. Without it a layer's true cost is wrong by ~11% and every margin
	// downstream is wrong with it.
	FakturReceived bool

	// ExpiryDate is captured and not acted on. FEFO and batch-expiry picking
	// are out of scope (REQUIREMENTS §6.4, §8); consumption here is strictly
	// oldest-acquired-first. The field exists so the data is there if that ever
	// changes.
	ExpiryDate *time.Time
}

// Remaining is the quantity still on the layer: qty_in − Σ qty_out.
//
// Derived, never stored (SPEC §3.1).
func (l Layer) Remaining() int64 { return l.QtyIn - l.QtyConsumed }

// CostAt returns the cost of the first qty units of the layer.
//
// Unit cost is derived here and never stored. A 7-unit layer at Rp 100.000 is
// Rp 14.285,714…/unit; storing that rounded and multiplying it back out loses
// rupiah on every single draw (SPEC §1).
//
// Every draw's cost is the difference between two of these prefixes —
// CostAt(after) − CostAt(before) — and that is what makes the draws over a
// layer's whole life sum to exactly CostTotal. The intermediate terms cancel
// and what survives is CostAt(QtyIn) − CostAt(0), which is CostTotal by
// definition. No remainder is left over to lose.
//
// This is a stronger property than "the last draw absorbs the remainder", and
// deliberately so: at the moment of a draw nothing knows whether it is the last
// one. A layer half-drawn in March and finished in September is ordinary, and
// the two calls have no shared state to carry a remainder through. Anchoring
// each draw to a prefix of the layer instead means the books close to the
// rupiah whatever order, sizes, or months the draws happen in.
func (l Layer) CostAt(qty int64) money.IDR {
	if l.QtyIn == 0 {
		return money.Zero // guarded rather than dividing; validate() rejects it anyway
	}
	return l.CostTotal.MulRatio(qty, l.QtyIn)
}

// CostConsumed is the slice of CostTotal the draws so far have taken.
func (l Layer) CostConsumed() money.IDR { return l.CostAt(l.QtyConsumed) }

// CostRemaining is the slice of CostTotal still to be drawn.
//
// Exact by construction: it is what CostTotal minus every past draw must equal,
// because each past draw was a prefix difference.
func (l Layer) CostRemaining() money.IDR { return l.CostTotal.Sub(l.CostConsumed()) }

// validate rejects a layer that could not be true. Callers hand these in from
// the store; a layer that has been drawn beyond its size, or that has no
// acquisition time to order by, is a bug upstream and is refused loudly rather
// than costed into a margin report.
func (l Layer) validate() error {
	switch {
	case strings.TrimSpace(l.ID) == "":
		return fmt.Errorf("%w: id is empty", ErrInvalidLayer)
	case strings.TrimSpace(l.EntityID) == "":
		return fmt.Errorf("%w: layer %s has an empty entity id", ErrInvalidLayer, l.ID)
	case strings.TrimSpace(l.ProductID) == "":
		return fmt.Errorf("%w: layer %s has an empty product id", ErrInvalidLayer, l.ID)
	case l.AcquiredAt.IsZero():
		return fmt.Errorf("%w: layer %s has no acquisition time, so FIFO order is undefined", ErrInvalidLayer, l.ID)
	case l.QtyIn <= 0:
		return fmt.Errorf("%w: layer %s has qty_in %d, but a layer exists to bring stock in", ErrInvalidLayer, l.ID, l.QtyIn)
	case l.QtyConsumed < 0:
		return fmt.Errorf("%w: layer %s has Σ qty_out of %d, which cannot be negative", ErrInvalidLayer, l.ID, l.QtyConsumed)
	case l.QtyConsumed > l.QtyIn:
		return fmt.Errorf("%w: layer %s has %d drawn against qty_in %d, more than it ever held", ErrInvalidLayer, l.ID, l.QtyConsumed, l.QtyIn)
	case l.CostTotal.IsNegative():
		return fmt.Errorf("%w: layer %s has a negative cost total (%s)", ErrInvalidLayer, l.ID, l.CostTotal)
	}
	return nil
}
