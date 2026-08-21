package fifo

import (
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Draw is a consumption that already happened, as the caller reads it back from
// storage. A reversal names one of these; a return that corresponds to no
// actual sale is a bug, not a stock increase (D-010).
type Draw struct {
	ID      string
	LayerID string
	// QtyOut and Cost are what this draw originally took, positive.
	QtyOut int64
	Cost   money.IDR
	// QtyReversed is how much of it has already been given back, summed over
	// the reversals that name it. Derived by the caller, like Layer.QtyConsumed.
	QtyReversed int64
}

// Remaining is how much of this draw could still be reversed.
func (d Draw) Remaining() int64 { return d.QtyOut - d.QtyReversed }

// costAt returns the cost of the first qty units of the draw.
//
// The same prefix construction as Layer.CostAt, and for the same reason:
// reversing three of seven units twice must give back exactly what was taken,
// not a rounded third each time.
func (d Draw) costAt(qty int64) money.IDR {
	if d.QtyOut == 0 {
		return money.Zero
	}
	return d.Cost.MulRatio(qty, d.QtyOut)
}

// ReverseRequest asks for part of an earlier draw to be given back.
type ReverseRequest struct {
	// Qty is positive: how many units come back. The negative sign belongs to
	// the record that gets written, not to the request.
	Qty        int64
	MovementID string
	OccurredAt time.Time
}

// Reverse gives units back to the layer they came from. D-010, TASKS 2.9.
//
// The goods return to the layer they were drawn from, not to a new layer at
// today's cost. That is what makes the margin reverse exactly: a new layer
// would reverse revenue in full while reversing cost at a different figure,
// quietly inventing margin in the one report family members settle money on.
// It also keeps owner attribution automatic — the original layer belongs to
// someone, so the reversal lands in that person's bucket without anyone
// deciding whose it is (INV-8).
//
// The result is an appended Consumption with a negative QtyOut and a negative
// Cost, never an edit to the layer or to the original draw (INV-7, INV-2). The
// derivation needs no special case for it: remaining = qty_in - Σ qty_out
// simply rises again.
//
// Reversing more than the draw took is refused. Returning four of three units
// sold is data entry to reject, not a correction.
func Reverse(req ReverseRequest, draw Draw) (Consumption, error) {
	switch {
	case strings.TrimSpace(req.MovementID) == "":
		return Consumption{}, fmt.Errorf("%w: movement id is empty, so the reversal could not be traced back to what caused it", ErrInvalidRequest)
	case req.Qty <= 0:
		return Consumption{}, fmt.Errorf("%w: reversal quantity must be positive, got %d", ErrInvalidRequest, req.Qty)
	case req.OccurredAt.IsZero():
		return Consumption{}, fmt.Errorf("%w: occurred-at is zero", ErrInvalidRequest)
	case strings.TrimSpace(draw.ID) == "":
		return Consumption{}, fmt.Errorf("%w: the draw being reversed has no id", ErrInvalidDraw)
	case strings.TrimSpace(draw.LayerID) == "":
		return Consumption{}, fmt.Errorf("%w: draw %s names no layer", ErrInvalidDraw, draw.ID)
	case draw.QtyOut <= 0:
		return Consumption{}, fmt.Errorf("%w: draw %s took %d units; only a draw can be reversed, not another reversal", ErrInvalidDraw, draw.ID, draw.QtyOut)
	case draw.Cost.IsNegative():
		return Consumption{}, fmt.Errorf("%w: draw %s has a negative cost (%s)", ErrInvalidDraw, draw.ID, draw.Cost)
	case draw.QtyReversed < 0:
		return Consumption{}, fmt.Errorf("%w: draw %s reports %d already reversed", ErrInvalidDraw, draw.ID, draw.QtyReversed)
	case draw.QtyReversed > draw.QtyOut:
		return Consumption{}, fmt.Errorf("%w: draw %s has %d reversed against %d taken", ErrInvalidDraw, draw.ID, draw.QtyReversed, draw.QtyOut)
	}

	if req.Qty > draw.Remaining() {
		return Consumption{}, &OverReversalError{
			DrawID:      draw.ID,
			LayerID:     draw.LayerID,
			Requested:   req.Qty,
			Taken:       draw.QtyOut,
			AlreadyBack: draw.QtyReversed,
		}
	}

	// The slice of the draw these units represent, by the same prefix
	// difference the draw itself was priced with. Reversing the whole draw in
	// any number of steps gives back exactly what it took, to the rupiah.
	cost := draw.costAt(draw.QtyReversed + req.Qty).Sub(draw.costAt(draw.QtyReversed))

	return Consumption{
		LayerID:    draw.LayerID,
		MovementID: req.MovementID,
		QtyOut:     -req.Qty,
		Cost:       cost.Neg(),
		OccurredAt: req.OccurredAt,
		ReversesID: draw.ID,
	}, nil
}
