package fifo_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
)

func drawOf(id string, qty int64, cost money.IDR, reversed int64) fifo.Draw {
	return fifo.Draw{ID: id, LayerID: "layer-1", QtyOut: qty, Cost: cost, QtyReversed: reversed}
}

func reverseReq(qty int64) fifo.ReverseRequest {
	return fifo.ReverseRequest{Qty: qty, MovementID: "return-1", OccurredAt: day(31)}
}

// D-010: the goods go back to the layer they came from, as an appended row with
// a negative quantity and cost. Never a new layer, never an edit.
func TestReverseAppendsANegativeDrawAgainstTheOriginalLayer(t *testing.T) {
	t.Parallel()

	got, err := fifo.Reverse(reverseReq(2), drawOf("draw-1", 5, 50_000, 0))
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}

	if got.LayerID != "layer-1" {
		t.Errorf("layer = %q, want the layer the goods came from", got.LayerID)
	}
	if got.QtyOut != -2 {
		t.Errorf("qty_out = %d, want -2 so remaining rises", got.QtyOut)
	}
	if got.Cost != -20_000 {
		t.Errorf("cost = %s, want %s", got.Cost, money.IDR(-20_000))
	}
	if got.ReversesID != "draw-1" {
		t.Errorf("reverses = %q, want the draw being undone", got.ReversesID)
	}
	if got.MovementID != "return-1" {
		t.Errorf("movement = %q, want the return that caused it", got.MovementID)
	}
}

// TestReversingADrawInStepsGivesBackExactlyWhatItTook is the precision
// property, on the reversal side.
//
// A draw of 7 units costing Rp 100.000 is Rp 14.285,714… each. Returning it two
// units at a time, or one, or all at once, must give back Rp 100.000 and not a
// rupiah more or less — otherwise a return quietly invents or destroys margin
// in the report the family settles on.
func TestReversingADrawInStepsGivesBackExactlyWhatItTook(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		qtyOut  int64
		cost    money.IDR
		pattern []int64
	}{
		{"seven at a hundred thousand, one at a time", 7, 100_000, []int64{1, 1, 1, 1, 1, 1, 1}},
		{"seven at a hundred thousand, in threes", 7, 100_000, []int64{3, 3, 1}},
		{"seven at a hundred thousand, all at once", 7, 100_000, []int64{7}},
		{"three at ten thousand", 3, 10_000, []int64{1, 1, 1}},
		{"eleven at one rupiah", 11, 1, []int64{5, 5, 1}},
		{"thirteen at 999.999", 13, 999_999, []int64{4, 4, 4, 1}},
		{"free goods reverse to nothing", 5, 0, []int64{2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				reversed int64
				total    money.IDR
			)
			for i, take := range tc.pattern {
				got, err := fifo.Reverse(reverseReq(take), drawOf("d", tc.qtyOut, tc.cost, reversed))
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if got.QtyOut != -take {
					t.Fatalf("step %d returned %d units, want %d", i, -got.QtyOut, take)
				}
				reversed += take
				total = total.Add(got.Cost)
			}

			if reversed != tc.qtyOut {
				t.Fatalf("reversed %d of %d units", reversed, tc.qtyOut)
			}
			if total != tc.cost.Neg() {
				t.Errorf("gave back %s in total, want exactly %s (%s adrift)",
					total, tc.cost.Neg(), total.Add(tc.cost))
			}
		})
	}
}

// Returning four of three units sold is data entry to reject, not a correction
// (D-010). Left unchecked the layer's remaining quantity would rise above what
// it ever held.
func TestReverseRefusesMoreThanTheDrawTook(t *testing.T) {
	t.Parallel()

	_, err := fifo.Reverse(reverseReq(4), drawOf("draw-1", 3, 30_000, 0))
	if !errors.Is(err, fifo.ErrOverReversal) {
		t.Fatalf("got %v, want ErrOverReversal", err)
	}

	var over *fifo.OverReversalError
	if !errors.As(err, &over) {
		t.Fatalf("error %v does not carry the detail the UI needs", err)
	}
	if over.Requested != 4 || over.Taken != 3 || over.Remaining() != 3 {
		t.Errorf("requested %d taken %d remaining %d; want 4, 3, 3",
			over.Requested, over.Taken, over.Remaining())
	}

	// And it counts what has already gone back, not just the original size.
	_, err = fifo.Reverse(reverseReq(2), drawOf("draw-1", 3, 30_000, 2))
	if !errors.Is(err, fifo.ErrOverReversal) {
		t.Fatalf("got %v, want ErrOverReversal after 2 of 3 were already returned", err)
	}
	if errors.As(err, &over) && over.Remaining() != 1 {
		t.Errorf("remaining = %d, want 1", over.Remaining())
	}

	// Exactly the remainder is fine.
	if _, err := fifo.Reverse(reverseReq(1), drawOf("draw-1", 3, 30_000, 2)); err != nil {
		t.Errorf("refused the last unit of a draw: %v", err)
	}
}

func TestReverseRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  fifo.ReverseRequest
		draw fifo.Draw
		want error
	}{
		{"zero quantity", reverseReq(0), drawOf("d", 5, 50_000, 0), fifo.ErrInvalidRequest},
		{
			"a negative quantity is not how a return is expressed",
			reverseReq(-2), drawOf("d", 5, 50_000, 0), fifo.ErrInvalidRequest,
		},
		{
			"no movement to attribute it to",
			fifo.ReverseRequest{Qty: 1, OccurredAt: day(31)}, drawOf("d", 5, 50_000, 0),
			fifo.ErrInvalidRequest,
		},
		{"no draw id", reverseReq(1), drawOf("", 5, 50_000, 0), fifo.ErrInvalidDraw},
		{
			"reversing a reversal",
			reverseReq(1), drawOf("d", -5, -50_000, 0), fifo.ErrInvalidDraw,
		},
		{
			"a draw that already gave back more than it took",
			reverseReq(1), drawOf("d", 3, 30_000, 4), fifo.ErrInvalidDraw,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := fifo.Reverse(tc.req, tc.draw); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// A layer drawn down and then partly returned reports the right remaining
// quantity with no special case: remaining = qty_in - sum of qty_out, and a
// negative draw simply raises it (D-010).
func TestAReversedDrawRaisesTheLayerRemainder(t *testing.T) {
	t.Parallel()

	// 10 in, 4 drawn, 3 of those returned: 6 + 3 = 9 on hand.
	layer := lay("l", 1, ownerBudi, 10, 4-3, 100_000)

	if got := layer.Remaining(); got != 9 {
		t.Errorf("remaining = %d, want 9", got)
	}
	// And the cost still reconciles: one unit's worth is out.
	if got := layer.CostConsumed(); got != 10_000 {
		t.Errorf("cost consumed = %s, want %s", got, money.IDR(10_000))
	}
}
