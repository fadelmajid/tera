package tax

import (
	"errors"
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Seller is the company ringing the sale.
//
// IsPKP is a separate argument rather than a field on the cart because it is a
// fact about the company, not about the basket, and because the zero value of a
// bool tucked inside a cart is "not registered for VAT" — the exact silent
// failure TASKS 5.4 exists to prevent. Here it has to be supplied.
type Seller struct {
	EntityID string
	IsPKP    bool
}

// Line is one cart line, already priced and discounted.
type Line struct {
	// Ref identifies the line back in the caller's world — a sale_line id, or
	// an index while the sale is still being built. Echoed onto the result and
	// never used in arithmetic.
	Ref string

	// Amount is the line's net, after every discount, line-level and allocated.
	//
	// Under inclusive pricing this is what the customer pays for the line and
	// the tax is inside it. Under exclusive pricing it is the base the tax is
	// added to. Which one it means is the rule's Inclusive flag, not something
	// the caller decides per line.
	Amount money.IDR

	// Exempt marks a line no PPN is levied on, while the rest of the cart is
	// taxed normally — the mixed cart of SPEC §7 criterion 2.
	//
	// Nothing sets this yet. There is no exemption flag on the product because
	// nobody has said which of this catalogue is exempt, and inventing a list
	// of exempt alat kesehatan would be worse than not having one. The field
	// exists so that answer lands as data rather than as surgery on the engine.
	Exempt bool
}

// Cart is a basket ready to be priced.
type Cart struct {
	// BusinessDate selects the rule in force (INV-4), already resolved in the
	// entity's timezone by the caller (INV-5).
	BusinessDate string

	// FakturIssued is whether the buyer took a faktur pajak.
	//
	// It is recorded and echoed onto the result. It reaches no arithmetic in
	// this package, and that is the point: a PKP owes output PPN on the
	// delivery of taxable goods whether or not a faktur was issued (SPEC §2.3).
	// Letting the two collapse into each other is the most common way a newly
	// registered business loses margin without noticing, so the tax functions
	// below are not given this field at all.
	FakturIssued bool

	Lines []Line
}

// LineTax is one line's share of the tax.
type LineTax struct {
	Ref    string
	Amount money.IDR
	// DPP is the taxable base — dasar pengenaan pajak. On an untaxed or exempt
	// line it is the whole amount, carrying no tax meaning: it is what keeps
	// DPP + Tax = Total true on every line, taxed or not.
	DPP money.IDR
	Tax money.IDR
	// Total is what this line contributes to the invoice: the amount under
	// inclusive pricing, the amount plus tax under exclusive.
	Total money.IDR
	// Taxed is false for an exempt line and for every line of a non-PKP sale.
	Taxed bool
}

// Result is a priced cart.
//
// Σ Lines.DPP + Σ Lines.Tax == GrandTotal, exactly, with no drift (SPEC §2.2).
// Calculate refuses to return a result where it does not hold.
type Result struct {
	Lines []LineTax

	DPP        money.IDR
	Tax        money.IDR
	GrandTotal money.IDR

	// Taxed is whether any PPN was levied at all.
	Taxed bool

	// Rule is the rule that was applied, zero-valued when Taxed is false. The
	// service snapshots it onto the sale rather than storing a reference to the
	// config row, so changing a rate later cannot move a settled figure (INV-3).
	Rule Rule

	SellerIsPKP  bool
	FakturIssued bool

	// AccruedWithoutFaktur is a PKP sale that owes output PPN with no faktur
	// issued to the buyer (SPEC §2.3).
	//
	// Not a warning and not an error — an ordinary walk-in sale looks exactly
	// like this. It is surfaced because the liability is invisible on the
	// receipt and the counter-party has no paperwork to reconcile against, so
	// the PPN position report is the only place it ever shows up. The flag is
	// what lets that report separate it out instead of burying it in a total.
	AccruedWithoutFaktur bool
}

// Calculate prices a cart under the rules in force on its business date
// (TASKS 5.3).
//
// The two formulas are SPEC §2.2:
//
//	exclusive   tax = round(base × rate)          total = base + tax
//	inclusive   dpp = round(price / (1 + rate))   tax   = price − dpp
//
// The inclusive case subtracts. It never recomputes the tax independently and
// adds, because a separately rounded tax opens a one-rupiah gap between the
// shelf price and the sum of its parts, and a till whose receipt does not add up
// is a till the cashier stops trusting.
//
// PKP status is enforced here, not left to the caller:
//
//   - A non-PKP entity charges nothing and cannot issue a faktur (SPEC §2.3,
//     TASKS 5.5). A rule already in force against a non-PKP entity is a
//     contradiction and is refused rather than quietly ignored.
//   - A PKP entity with no rule in force is refused too. The liability accrues
//     whether or not anything was charged, so pricing the sale at zero would
//     take the shortfall out of margin silently (TASKS 5.4).
func Calculate(seller Seller, cart Cart, rules RuleSet) (Result, error) {
	if seller.EntityID == "" {
		return Result{}, fmt.Errorf("%w: no selling entity", ErrInvalidCart)
	}
	if err := cart.validate(); err != nil {
		return Result{}, err
	}
	// An empty rule set has no entity to disagree with; a populated one must be
	// this seller's. Pricing entity A's cart with entity B's rules is not a
	// near miss when one of them is PKP and the other legally cannot charge.
	if id := rules.EntityID(); id != "" && id != seller.EntityID {
		return Result{}, fmt.Errorf("%w: seller is %s, rules are %s", ErrEntityMismatch, seller.EntityID, id)
	}

	rule, inForce := rules.Effective(PPN, cart.BusinessDate)

	if !seller.IsPKP {
		if cart.FakturIssued {
			return Result{}, fmt.Errorf("%w: entity %s", ErrNonPKPFaktur, seller.EntityID)
		}
		if inForce {
			return Result{}, &NonPKPChargeError{
				EntityID: seller.EntityID, BusinessDate: cart.BusinessDate,
				RuleID: rule.ID, LegalRef: rule.LegalRef, ValidFrom: rule.ValidFrom,
			}
		}
		return untaxed(seller, cart), nil
	}

	if !inForce {
		return Result{}, &NoEffectiveRuleError{
			EntityID: seller.EntityID, Type: PPN, BusinessDate: cart.BusinessDate,
		}
	}

	var (
		lines []LineTax
		err   error
	)
	switch rule.Level {
	case LevelInvoice:
		lines, err = perInvoice(cart.Lines, rule)
	case LevelLine:
		lines = perLine(cart.Lines, rule)
	default:
		// Unreachable: Validate rejected every other level when the rule set
		// was built. Present so a level added later without a branch here
		// fails loudly instead of silently charging nothing.
		return Result{}, fmt.Errorf("%w %s: calculation level %q has no implementation", ErrInvalidRule, rule.ID, rule.Level)
	}
	if err != nil {
		return Result{}, err
	}

	out := Result{
		Lines: lines, Taxed: true, Rule: rule,
		SellerIsPKP: true, FakturIssued: cart.FakturIssued,
	}
	for _, l := range lines {
		out.DPP = out.DPP.Add(l.DPP)
		out.Tax = out.Tax.Add(l.Tax)
		out.GrandTotal = out.GrandTotal.Add(l.Total)
	}
	out.Taxed = out.Tax.IsPositive()
	out.AccruedWithoutFaktur = out.Tax.IsPositive() && !cart.FakturIssued

	if err := out.assertNoDrift(); err != nil {
		return Result{}, err
	}
	return out, nil
}

// untaxed prices a cart nobody may charge tax on: every non-PKP sale
// (SPEC §2.3, TASKS 5.5).
//
// There is no rate to get wrong here because no rate is consulted. That is what
// "enforced, not conventional" means — not a branch that happens to pass zero
// to the tax arithmetic, but a path the tax arithmetic is not on.
func untaxed(seller Seller, cart Cart) Result {
	out := Result{
		Lines:       make([]LineTax, 0, len(cart.Lines)),
		SellerIsPKP: seller.IsPKP,
	}
	for _, l := range cart.Lines {
		out.Lines = append(out.Lines, LineTax{
			Ref: l.Ref, Amount: l.Amount, DPP: l.Amount, Total: l.Amount,
		})
		out.DPP = out.DPP.Add(l.Amount)
		out.GrandTotal = out.GrandTotal.Add(l.Amount)
	}
	return out
}

// perLine rounds on each line and sums.
//
// The sum is exact either way. Inclusive: each line's tax is its own amount
// minus its own DPP, so the line totals are the amounts and the grand total is
// their sum. Exclusive: each line's total is its amount plus its own rounded
// tax. No line is rounded twice and nothing is reconciled afterwards.
func perLine(lines []Line, rule Rule) []LineTax {
	out := make([]LineTax, 0, len(lines))
	for _, l := range lines {
		if l.Exempt {
			out = append(out, LineTax{Ref: l.Ref, Amount: l.Amount, DPP: l.Amount, Total: l.Amount})
			continue
		}
		lt := LineTax{Ref: l.Ref, Amount: l.Amount, Taxed: true}
		if rule.Inclusive {
			lt.DPP = rule.dppFromInclusive(l.Amount)
			lt.Tax = l.Amount.Sub(lt.DPP) // subtract; never recompute (SPEC §2.2)
		} else {
			lt.DPP = l.Amount
			lt.Tax = rule.tax(l.Amount)
		}
		lt.Total = lt.DPP.Add(lt.Tax)
		out = append(out, lt)
	}
	return out
}

// perInvoice rounds once, on the invoice, and allocates the result back to the
// lines.
//
// The order matters. The invoice figure is computed first and is authoritative —
// it is what goes on the faktur pajak — and the lines are then carved out of it
// by money.Allocate, which hands the rounding remainder to the largest lines
// rather than losing it. Rounding each line independently and summing would give
// a different invoice total from the one on the faktur, which is the drift
// SPEC §2.2 forbids.
func perInvoice(lines []Line, rule Rule) ([]LineTax, error) {
	out := make([]LineTax, len(lines))
	taxable := make([]int, 0, len(lines))
	weights := make([]int64, 0, len(lines))

	var base money.IDR
	for i, l := range lines {
		out[i] = LineTax{Ref: l.Ref, Amount: l.Amount, DPP: l.Amount, Total: l.Amount}
		if l.Exempt {
			continue
		}
		out[i].Taxed = true
		taxable = append(taxable, i)
		weights = append(weights, int64(l.Amount))
		base = base.Add(l.Amount)
	}

	// Nothing to divide: an all-exempt cart, or a cart discounted to zero. Both
	// are already correct above — DPP is the amount and the tax is nil — and
	// allocating against zero weights would only produce an error about it.
	if len(taxable) == 0 || base.IsZero() {
		return out, nil
	}

	if rule.Inclusive {
		invoiceDPP := rule.dppFromInclusive(base)
		shares, err := money.Allocate(invoiceDPP, weights)
		if err != nil {
			return nil, fmt.Errorf("tax: allocate invoice DPP: %w", err)
		}
		for n, i := range taxable {
			out[i].DPP = shares[n]
			out[i].Tax = out[i].Amount.Sub(shares[n]) // subtract, per line as per invoice
			out[i].Total = out[i].Amount
		}
		return out, nil
	}

	invoiceTax := rule.tax(base)
	shares, err := money.Allocate(invoiceTax, weights)
	if err != nil {
		return nil, fmt.Errorf("tax: allocate invoice PPN: %w", err)
	}
	for n, i := range taxable {
		out[i].DPP = out[i].Amount
		out[i].Tax = shares[n]
		out[i].Total = out[i].Amount.Add(shares[n])
	}
	return out, nil
}

// errDrift is the failure SPEC §2.2's property test exists to catch, asserted on
// every real cart rather than only on generated ones.
var errDrift = errors.New("tax: the parts do not sum to the whole")

// assertNoDrift refuses a result whose parts do not sum to its whole.
//
// It should never fire. It is here because the cost of the check is nothing at
// under a thousand transactions a day, and the cost of it being needed and
// absent is a receipt that does not add up in front of a customer.
func (r Result) assertNoDrift() error {
	var dpp, tax, total money.IDR
	for _, l := range r.Lines {
		dpp = dpp.Add(l.DPP)
		tax = tax.Add(l.Tax)
		total = total.Add(l.Total)
		if l.DPP.Add(l.Tax) != l.Total {
			return fmt.Errorf("%w: line %s has DPP %s + PPN %s against a total of %s",
				errDrift, l.Ref, l.DPP, l.Tax, l.Total)
		}
	}
	if dpp.Add(tax) != total || total != r.GrandTotal {
		return fmt.Errorf("%w: DPP %s + PPN %s against a grand total of %s",
			errDrift, dpp, tax, r.GrandTotal)
	}
	return nil
}

func (c Cart) validate() error {
	if _, err := time.Parse(DateFormat, c.BusinessDate); err != nil {
		return fmt.Errorf("%w: business date %q is not a YYYY-MM-DD date", ErrInvalidCart, c.BusinessDate)
	}
	if len(c.Lines) == 0 {
		return fmt.Errorf("%w: no lines", ErrInvalidCart)
	}
	for i, l := range c.Lines {
		if l.Amount.IsNegative() {
			// A negative line would be a refund, and a refund reverses the tax
			// snapshotted on the original sale rather than computing a fresh
			// one against today's rate (INV-3). It does not come through here.
			return fmt.Errorf("%w: line %d (%s) is negative at %s", ErrInvalidCart, i+1, l.Ref, l.Amount)
		}
	}
	return nil
}
