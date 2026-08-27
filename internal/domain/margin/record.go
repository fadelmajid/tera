package margin

import (
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// OwnerID is the family member a movement is attributed to, or [Company].
//
// Aliased from fifo rather than redeclared: whose stock moved is decided when
// the layer is drawn, and INV-8 should have exactly one definition in the
// codebase. A second type spelled the same way is how two packages end up
// disagreeing about what an empty string means.
type OwnerID = fifo.OwnerID

// Company is the bucket for stock with no owner attribution (R2.2). Its own
// line on the report, beside the named owners — never a residual.
const Company = fifo.Company

// Draw is one stock_consumption row: a slice of one FIFO layer leaving, and
// what that slice cost.
//
// This is the authority for COGS (SPEC §4.1) and the bottom of the drill-down
// (SPEC §4.2). Quantity and cost are negative on a reversal — a return putting
// goods back on the layer they came from (D-010) — so a period's figures are a
// plain sum and need no special case.
type Draw struct {
	ID      string
	LayerID string
	// ProductID and OwnerID come from the layer, not from the sale. The layer
	// is where ownership was recorded when the goods were bought.
	ProductID string
	OwnerID   OwnerID

	QtyOut int64
	Cost   money.IDR

	// LayerAcquiredAt is when the layer arrived — the reason FIFO drew this one
	// and not another, and the first thing anyone asks when a cost looks wrong.
	LayerAcquiredAt time.Time
	// LayerSource is PURCHASE, TRANSFER_IN, ADJUSTMENT or OPENING.
	LayerQtyIn     int64
	LayerCostTotal money.IDR
	LayerSource    string

	// FakturReceived is INV-9 surfacing on the drill-down. Without the faktur
	// the PPN paid on this layer was never creditable and is real cost, so the
	// same purchase price yields a layer ~11% more expensive. It is on this row
	// because "why is my margin lower" is often answered here and nowhere else.
	FakturReceived bool

	// ReversesID names the draw this row gives back, empty on a draw.
	ReversesID string
}

// IsReversal reports whether the row puts stock back rather than taking it.
func (d Draw) IsReversal() bool { return d.ReversesID != "" }

func (d Draw) validate(context string) error {
	switch {
	case strings.TrimSpace(d.ID) == "":
		return fmt.Errorf("%w: %s has a draw with no id", ErrInvalidRecord, context)
	case strings.TrimSpace(d.LayerID) == "":
		return fmt.Errorf("%w: %s draw %s names no layer", ErrInvalidRecord, context, d.ID)
	case strings.TrimSpace(d.ProductID) == "":
		return fmt.Errorf("%w: %s draw %s names no product", ErrInvalidRecord, context, d.ID)
	case d.QtyOut == 0:
		return fmt.Errorf("%w: %s draw %s moved nothing", ErrInvalidRecord, context, d.ID)
	case d.IsReversal() && d.QtyOut > 0:
		return fmt.Errorf("%w: %s draw %s reverses %s but takes stock out", ErrInvalidRecord, context, d.ID, d.ReversesID)
	case !d.IsReversal() && d.QtyOut < 0:
		return fmt.Errorf("%w: %s draw %s puts stock back without naming what it reverses", ErrInvalidRecord, context, d.ID)
	}
	return nil
}

// SaleLine is one line of a finalised sale, as the report reads it.
type SaleLine struct {
	ID          string
	ProductID   string
	ProductCode string
	ProductName string

	// OwnerID is the attribution snapshotted at the moment of sale. Re-tagging
	// a product later must not move money that has already been settled, which
	// is why this is read from the line and not from the product.
	OwnerID OwnerID

	Qty int64

	// Revenue is what this line earned: net of the line discount and of this
	// line's share of any invoice-level discount, and net of PPN.
	//
	// Net of PPN because COGS comes off a stock layer that is already net of
	// creditable PPN (SPEC §3.2). Subtracting a tax-inclusive revenue from a
	// tax-exclusive cost would overstate the margin by the rate, in the one
	// report family members settle real money on. Under inclusive pricing that
	// makes Revenue smaller than what the customer handed over; PPN below is
	// the difference, so the drill-down can answer "why isn't this the invoice
	// total".
	Revenue money.IDR

	// PPN is the output PPN inside what the customer paid for this line
	// (SPEC §2.3). Carried for the drill-down and never added to margin: it is
	// owed to the state and shows up in the PPN position report instead.
	// Zero on a sale rung by the non-PKP entity, and on anything sold before
	// the tax engine landed.
	PPN money.IDR

	// RecordedCOGS is the cost figure stored on the line when the sale was
	// rung. It is not used to compute anything — COGS comes from the draws —
	// but [Compute] checks the two agree. They are written in the same
	// transaction from the same numbers, so a disagreement means something
	// corrupted them since, and that is worth stopping for.
	RecordedCOGS money.IDR
}

// Sale is one finalised sale with every layer it drew.
//
// Voided sales do not belong here at all: a void says the sale did not happen,
// and its reversals share the sale's movement id, so including one would net to
// zero revenue against zero cost and add a phantom row to the drill-down.
type Sale struct {
	ID           string
	InvoiceNo    string
	BusinessDate string
	OccurredAt   time.Time
	CustomerName string

	Lines []SaleLine
	// Draws are every consumption this sale made, across all its lines.
	// Attribution to a line is by product, not by a stored line id: what the
	// report needs is whose stock left and what it cost, and the layer carries
	// both.
	Draws []Draw
}

// ReturnLine is one line of goods coming back.
type ReturnLine struct {
	ID         string
	SaleLineID string

	ProductID   string
	ProductCode string
	ProductName string
	// OwnerID is copied from the sale line the goods went out on, so a return
	// lands in the same person's bucket the revenue did.
	OwnerID OwnerID

	Qty int64

	// Refund, PPNReversed and COGSReversed are positive magnitudes: this much
	// money went back to the customer, this much of it was PPN, and this much
	// cost came off the books. The signed records are the reversal draws;
	// carrying magnitudes here keeps every caller from having to remember which
	// way each one points.
	//
	// Refund is the whole sum handed back, tax included -- it is what the
	// customer received and what the document says. The margin reversal is
	// Refund less PPNReversed, for the same reason Revenue is net of PPN.
	Refund       money.IDR
	PPNReversed  money.IDR
	COGSReversed money.IDR
}

// Return is goods coming back after the sale (R12.1, D-010).
//
// It carries both dates because the two are what SPEC §4.4 is a choice
// between. Neither is derivable from the other once the months differ.
type Return struct {
	ID            string
	SaleID        string
	SaleInvoiceNo string

	// BusinessDate is when the goods came back.
	BusinessDate string
	// SaleBusinessDate is when the original sale happened.
	SaleBusinessDate string

	Reason string

	Lines []ReturnLine
	// Draws are the reversal consumptions: negative quantity, negative cost,
	// each naming the draw it gives back.
	Draws []Draw
}

// EffectiveDate is the business date this return counts on under the rule in
// force — the whole of SPEC §4.4, in one line.
func (r Return) EffectiveDate(rule ReturnPeriodRule) string {
	if rule == AtSaleDate {
		return r.SaleBusinessDate
	}
	return r.BusinessDate
}

// CrossesPeriod reports whether the goods came back in a different calendar
// month than they were sold in.
//
// Flagged on the report whichever rule is in force: these are the rows whose
// placement is a judgement call, and a family settling money should be able to
// see them rather than discover them.
func (r Return) CrossesPeriod() bool {
	const monthPrefix = len("2006-01")
	if len(r.BusinessDate) < monthPrefix || len(r.SaleBusinessDate) < monthPrefix {
		return false
	}
	return r.BusinessDate[:monthPrefix] != r.SaleBusinessDate[:monthPrefix]
}
