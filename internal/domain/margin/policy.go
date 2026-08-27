package margin

import "fmt"

// ReturnPeriodRule decides which settlement period a return belongs to.
//
// SPEC §4.4. A November return of an October sale has two defensible homes:
//
//   - [AtReturnDate] leaves October exactly as it was settled and takes the
//     reversal out of November. The settlement already paid stays untouched;
//     October's report overstates what was really sold that month.
//   - [AtSaleDate] restates October so the month reads correctly, at the cost
//     of moving a figure the family already split money on. Nothing prevents
//     that — the user declined period locking (R7.3) — so it is visible only
//     in the audit log, after the fact.
//
// Resolved as [AtReturnDate] (D-012), because it never restates money that has
// already changed hands. Both stay implemented: which is right depends on how a
// family actually settles, which is a fact about them and not about accounting,
// and the service layer holds the per-entity choice.
//
// There is still no zero-value default *here*. A caller that has not named a
// rule has not decided one, and this package will not decide it for them.
type ReturnPeriodRule string

const (
	// ReturnPeriodUnset is the zero value and is never valid. A rule this
	// consequential is supplied explicitly or the report refuses to run.
	ReturnPeriodUnset ReturnPeriodRule = ""

	// AtReturnDate books a return into the period the goods came back.
	AtReturnDate ReturnPeriodRule = "RETURN_DATE"

	// AtSaleDate books a return back into the period of the original sale.
	AtSaleDate ReturnPeriodRule = "SALE_DATE"
)

// ParseReturnPeriodRule converts a stored rule string, refusing anything
// unrecognised rather than falling back — a rule that silently degrades moves
// money between family members.
func ParseReturnPeriodRule(s string) (ReturnPeriodRule, error) {
	switch ReturnPeriodRule(s) {
	case AtReturnDate:
		return AtReturnDate, nil
	case AtSaleDate:
		return AtSaleDate, nil
	default:
		return ReturnPeriodUnset, fmt.Errorf("%w: %q", ErrUnknownReturnRule, s)
	}
}

// Valid reports whether the rule is one of the two.
func (r ReturnPeriodRule) Valid() bool {
	return r == AtReturnDate || r == AtSaleDate
}

// Validate rejects an unset or unrecognised rule.
func (r ReturnPeriodRule) Validate() error {
	switch {
	case r == ReturnPeriodUnset:
		return ErrPolicyUnset
	case !r.Valid():
		return fmt.Errorf("%w: %q", ErrUnknownReturnRule, r)
	}
	return nil
}
