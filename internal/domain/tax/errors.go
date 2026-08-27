package tax

import (
	"errors"
	"fmt"
)

// Sentinel errors, for callers that need to branch on the kind of failure
// rather than read its detail. Match with errors.Is; reach the detail with
// errors.As on the concrete types below.
//
// Messages here are English because they are code (CLAUDE.md). The UI is in
// Bahasa Indonesia and builds its own wording from the exported fields.
var (
	// ErrInvalidRule means a tax_rule row could not be true.
	ErrInvalidRule = errors.New("tax: invalid tax rule")

	// ErrRuleOverlap means two rules of the same type claim the same day. A
	// rate is changed by inserting a new row and closing the old one's
	// valid_to (SPEC §2.1); an overlap is the half-done version of that, and
	// it would leave the rate applied to a sale depending on row order.
	ErrRuleOverlap = errors.New("tax: overlapping tax rules")

	// ErrNoEffectiveRule means a PKP entity had no rule in force on the day.
	ErrNoEffectiveRule = errors.New("tax: no tax rule in force")

	// ErrNonPKPCharge means a non-PKP entity was about to charge PPN.
	ErrNonPKPCharge = errors.New("tax: a non-PKP entity cannot charge PPN")

	// ErrNonPKPFaktur means a non-PKP entity was about to issue a faktur.
	ErrNonPKPFaktur = errors.New("tax: a non-PKP entity cannot issue a faktur pajak")

	// ErrInvalidCart means the cart handed to Calculate could not be true.
	ErrInvalidCart = errors.New("tax: invalid cart")

	// ErrEntityMismatch means a cart was about to be priced with another
	// company's rules. The two entities here are taxed differently, so this is
	// not a near miss.
	ErrEntityMismatch = errors.New("tax: rules belong to another entity")

	// ErrInvalidMasa means a masa pajak could not be parsed.
	ErrInvalidMasa = errors.New("tax: invalid masa pajak")
)

// NoEffectiveRuleError reports a PKP entity with no tax rule covering the day.
//
// Refusing is deliberate and it is the whole of TASKS 5.4. The alternative —
// treating a missing rule as zero — rings the sale through at no PPN while the
// liability accrues anyway, because a PKP owes output PPN on the delivery of
// taxable goods whether or not anything was charged or any faktur was issued
// (UU PPN — UU 8/1983 s.t.d.t.d. UU 7/2021 — Pasal 4 jo. Pasal 11; SPEC §2.3).
// The shortfall then comes out of margin, silently, and is only discovered at
// the masa pajak filing. A till that stops and says "no PPN rule is in force on
// this date" costs an hour; the silent version costs 11% of turnover.
type NoEffectiveRuleError struct {
	EntityID     string
	Type         Type
	BusinessDate string
}

// Error implements error.
func (e *NoEffectiveRuleError) Error() string {
	return fmt.Sprintf(
		"tax: entity %s is PKP but no %s rule is in force on %s: a PKP owes output PPN on the delivery whether or not it is charged, so this sale cannot be priced",
		e.EntityID, e.Type, e.BusinessDate,
	)
}

// Unwrap lets errors.Is(err, ErrNoEffectiveRule) match.
func (e *NoEffectiveRuleError) Unwrap() error { return ErrNoEffectiveRule }

// NonPKPChargeError reports a non-PKP entity holding a rule that is already in
// force.
//
// The two facts contradict each other and only a person can say which is
// stale. Either the entity registered and nobody flipped is_pkp — in which case
// it has been under-charging since valid_from — or the rule was staged for a
// registration that has not happened, with a valid_from that has now passed.
//
// Pre-staging is legitimate and supported: a rule dated to the first tax period
// of the following book year is exactly what PMK 164/2023 Pasal 18 asks for
// after a crossing. It stops being legitimate on the day it takes effect.
type NonPKPChargeError struct {
	EntityID     string
	BusinessDate string
	RuleID       string
	LegalRef     string
	ValidFrom    string
}

// Error implements error.
func (e *NonPKPChargeError) Error() string {
	return fmt.Sprintf(
		"tax: entity %s is marked non-PKP but rule %s (%s) has been in force since %s: on %s the two cannot both be true",
		e.EntityID, e.RuleID, e.LegalRef, e.ValidFrom, e.BusinessDate,
	)
}

// Unwrap lets errors.Is(err, ErrNonPKPCharge) match.
func (e *NonPKPChargeError) Unwrap() error { return ErrNonPKPCharge }

// RuleOverlapError reports two rules of the same type covering the same day.
type RuleOverlapError struct {
	Type    Type
	EarlyID string
	LateID  string
	// From and To are the overlapping span, inclusive.
	From string
	To   string
}

// Error implements error.
func (e *RuleOverlapError) Error() string {
	return fmt.Sprintf(
		"tax: %s rules %s and %s both cover %s..%s: close the earlier rule's valid_to before the later one's valid_from",
		e.Type, e.EarlyID, e.LateID, e.From, e.To,
	)
}

// Unwrap lets errors.Is(err, ErrRuleOverlap) match.
func (e *RuleOverlapError) Unwrap() error { return ErrRuleOverlap }
