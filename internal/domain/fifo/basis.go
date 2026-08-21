package fifo

import (
	"fmt"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Acquisition is what was actually paid for a batch of stock, plus the two
// facts that decide how much of that payment is cost.
//
// The amounts arrive already computed — this package does not calculate PPN,
// which is domain/tax's job against effective-dated rules (INV-4). What happens
// here is only the decision about what the payment *means*.
type Acquisition struct {
	// BuyerIsPKP is whether the buying entity is VAT-registered. A non-PKP
	// entity can never credit input PPN, whatever paperwork it holds
	// (SPEC §2.3).
	BuyerIsPKP bool

	// FakturReceived is whether the supplier actually handed over the faktur
	// pajak (INV-9). Not whether one was promised, invoiced, or is in the post.
	// The paper is what makes the PPN creditable; nothing else does.
	FakturReceived bool

	// GrossPaid is the total handed to the supplier for this batch, PPN
	// included.
	GrossPaid money.IDR

	// PPNPaid is the PPN component of GrossPaid.
	PPNPaid money.IDR
}

// Basis is the cost decision for one acquisition: what goes on the layer, and
// what goes to the tax position instead.
type Basis struct {
	// CostTotal goes on the layer as cost_total_idr, and through it into every
	// margin figure the family settles on.
	CostTotal money.IDR

	// PPNPaid goes on the layer as ppn_paid_idr — recorded whether or not it
	// was creditable, so the decision can be re-examined later.
	PPNPaid money.IDR

	// CreditablePPN is the input PPN this purchase contributes to the PPN
	// position (SPEC §2.4). Zero unless a PKP entity holds the faktur — that
	// filter is the whole point of the report.
	CreditablePPN money.IDR
}

// CostBasis decides how much of what was paid is cost and how much is
// recoverable tax (SPEC §3.2, INV-9).
//
// This is the rule that makes purchase tracking worth building at all. Three
// cases, same supplier, same price:
//
//	buyer     faktur   paid       CostTotal   because
//	PKP       yes      111.000    100.000     the 11.000 is creditable — recoverable, not cost
//	PKP       no       111.000    111.000     nothing to credit against, so it is cost
//	non-PKP   either   111.000    111.000     can never credit, so it is always cost
//
// The business cannot see this today. Their current system records one price
// and one cost, so purchases made without a faktur look ~11% cheaper than they
// are, and the margin on those goods is overstated by the same amount — in the
// one report family members split money on. Getting this wrong does not produce
// a rounding error; it produces an argument.
//
// The rule reduces to: everything that is not creditable is cost. Note it turns
// on the *entity*, not the supplier or the product, so the same purchase routed
// to the other company yields a different cost basis. That asymmetry is real
// and is why R4.5 makes transferring into the PKP entity a warned action.
func CostBasis(a Acquisition) (Basis, error) {
	switch {
	case a.GrossPaid.IsNegative():
		return Basis{}, fmt.Errorf("%w: gross paid is negative (%s)", ErrInvalidAcquisition, a.GrossPaid)
	case a.PPNPaid.IsNegative():
		return Basis{}, fmt.Errorf("%w: PPN paid is negative (%s)", ErrInvalidAcquisition, a.PPNPaid)
	case a.PPNPaid > a.GrossPaid:
		return Basis{}, fmt.Errorf(
			"%w: PPN paid (%s) exceeds the total paid (%s), so the goods would have a negative cost",
			ErrInvalidAcquisition, a.PPNPaid, a.GrossPaid,
		)
	}

	// Only a PKP entity holding the faktur can credit the input PPN
	// (SPEC §2.3). A non-PKP entity may well receive a faktur from a PKP
	// supplier — it is recorded, and it changes nothing.
	creditable := money.Zero
	if a.BuyerIsPKP && a.FakturReceived {
		creditable = a.PPNPaid
	}

	return Basis{
		CostTotal:     a.GrossPaid.Sub(creditable),
		PPNPaid:       a.PPNPaid,
		CreditablePPN: creditable,
	}, nil
}
