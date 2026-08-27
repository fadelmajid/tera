package fifo

import (
	"fmt"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Direction is which way stock crosses a company boundary, in the only terms
// that change the answer: whether each side is PKP.
//
// The two directions fail differently and neither is free (REQUIREMENTS §6.2),
// which is what makes "transfer at cost, no markup" (R4.3) a misleading mental
// model. This type exists so that consequence is computed once, in a pure
// function with tests, rather than re-derived at each screen that needs it.
type Direction struct {
	FromIsPKP bool
	ToIsPKP   bool
}

// TaxableDelivery reports whether the sending company must charge PPN on the
// delivery.
//
// A PKP delivering goods is making a taxable delivery whoever the buyer is —
// including a company under the same family (SPEC §2.3). A non-PKP cannot
// charge PPN at all, which is not a choice it gets to make.
func (d Direction) TaxableDelivery() bool { return d.FromIsPKP }

// CanIssueFaktur reports whether the sending company is able to issue a faktur
// pajak for the delivery. Only a PKP can.
func (d Direction) CanIssueFaktur() bool { return d.FromIsPKP }

// DestroysInputCredit reports whether stock crossing this way permanently
// forfeits input PPN credit. R4.5, REQUIREMENTS §6.2.
//
// non-PKP → PKP is the case. The sender cannot issue a faktur, so the receiving
// PKP entity acquires stock with no input credit against it, and will owe full
// output PPN when it sells — roughly 11% of margin, gone. The chain cannot be
// reconnected afterwards: there is no document that can be produced later to
// make an already-completed non-PKP delivery creditable.
//
// Stock destined to be sold by the PKP entity should be bought directly by the
// PKP entity, from a supplier who issues a faktur. That is a decision that has
// to be made before the goods move, which is why this is surfaced at the moment
// of transfer and not in a report afterwards.
func (d Direction) DestroysInputCredit() bool { return !d.FromIsPKP && d.ToIsPKP }

// String names the direction the way the warning copy does.
func (d Direction) String() string {
	return label(d.FromIsPKP) + " → " + label(d.ToIsPKP)
}

func label(isPKP bool) string {
	if isPKP {
		return "PKP"
	}
	return "non-PKP"
}

// Transfer is one movement of stock across a company boundary, priced.
type Transfer struct {
	Direction

	// Cost is what the consumed layers actually cost in the source company —
	// the transfer price, since transfers are at cost with no markup (R4.3).
	Cost money.IDR

	// PPN is charged on the delivery when the sender is PKP. Supplied rather
	// than computed: no rate is hardcoded anywhere (INV-4), and the tax engine
	// that would derive it is a later phase.
	PPN money.IDR

	// FakturIssued is whether the sender actually issued a faktur pajak for the
	// delivery, which is what decides whether the receiver can credit the PPN —
	// and only if the receiver is PKP at all (SPEC §3.2).
	FakturIssued bool

	// ForfeitedInputPPN is the input PPN already paid on the stock being moved,
	// carried on the source layers, which can never now be credited by anyone.
	// Zero unless [Direction.DestroysInputCredit]; informational, and the
	// figure the warning quotes.
	ForfeitedInputPPN money.IDR
}

// DestinationBasis is what the receiving company's new layer costs, and why.
//
// A transfer is an acquisition on the receiving side, so this is the same rule
// as a purchase (SPEC §3.2) and defers to [CostBasis] for it. What the
// direction adds is which inputs are even legal.
func (t Transfer) DestinationBasis() (Basis, error) {
	if err := t.validate(); err != nil {
		return Basis{}, err
	}
	return CostBasis(Acquisition{
		BuyerIsPKP:     t.ToIsPKP,
		FakturReceived: t.FakturIssued,
		GrossPaid:      t.Cost.Add(t.PPN),
		PPNPaid:        t.PPN,
	})
}

func (t Transfer) validate() error {
	switch {
	case t.Cost.IsNegative():
		return fmt.Errorf("%w: transfer cost is negative (%s)", ErrInvalidAcquisition, t.Cost)
	case t.PPN.IsNegative():
		return fmt.Errorf("%w: transfer PPN is negative (%s)", ErrInvalidAcquisition, t.PPN)
	// SPEC §2.3, enforced rather than conventional: a non-PKP cannot charge PPN
	// and cannot issue a faktur. Letting either through here would create a
	// creditable layer out of a delivery that legally cannot support one.
	case !t.TaxableDelivery() && t.PPN.IsPositive():
		return fmt.Errorf("%w: a %s delivery cannot carry PPN (%s)", ErrInvalidAcquisition, t.Direction, t.PPN)
	case !t.CanIssueFaktur() && t.FakturIssued:
		return fmt.Errorf("%w: a %s sender cannot issue a faktur pajak", ErrInvalidAcquisition, t.Direction)
	case t.ForfeitedInputPPN.IsNegative():
		return fmt.Errorf("%w: forfeited input PPN is negative (%s)", ErrInvalidAcquisition, t.ForfeitedInputPPN)
	}
	return nil
}
