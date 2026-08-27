package tax

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// DateFormat is the business-date form every dated table stores: 'YYYY-MM-DD'
// in the entity's timezone (D-005).
//
// A rule's effectivity is a legal date, not an instant. PMK 131/2024 took effect
// on 1 January 2025 in Indonesia, not at some UTC moment, so a rule is selected
// by the sale's business_date — which the service has already resolved in the
// entity's zone (INV-5). This package never converts an instant to a day.
const DateFormat = "2006-01-02"

// Type is the kind of tax a rule levies.
type Type string

// The taxes this system knows about. PB1/PBJT is deliberately absent: it is a
// regional restaurant and entertainment tax and does not reach medical supplies
// (SPEC §2.1).
const (
	// PPN is Indonesian VAT.
	PPN Type = "PPN"
	// PPnBM is the luxury-goods surcharge. Seeded as config so the rate is on
	// file and citable, but nothing in an alat kesehatan catalogue is a luxury
	// good, so no line is levied under it. Levying it would need a per-product
	// luxury flag, which nobody has asked for.
	PPnBM Type = "PPNBM"
)

// Level is where rounding happens: on each line, or once on the invoice.
//
// It changes the answer by a rupiah or two on a multi-line cart and both are
// defensible, so it is config rather than a decision baked into the code. What
// is not negotiable either way is that the parts sum to the whole (SPEC §2.2) —
// under INVOICE the invoice figure is authoritative and the lines are allocated
// out of it, never rounded independently and hoped to agree.
type Level string

// Where the rounding happens.
const (
	LevelLine    Level = "LINE"
	LevelInvoice Level = "INVOICE"
)

// RoundingMode is how a fractional rupiah is resolved.
type RoundingMode string

// HalfUp rounds a half away from zero: Rp 16,50 becomes Rp 17.
//
// It is the only mode implemented. The column exists because INV-4 forbids
// hardcoding, not because a second mode is wanted — an unused-but-available
// rounding mode is a way for a report to disagree with a receipt. When a
// regulation names another one, add it here with the citation and a test.
const HalfUp RoundingMode = "HALF_UP"

// Rate is the effective tax rate as an exact fraction.
//
//	effective_rate = rate_bp/10000 × dpp_num/dpp_den
//
// PPN non-luxury is 1200/10000 × 11/12, which reduces to exactly 11/100.
//
// A fraction, never a decimal. The DPP nilai lain of 11/12 has no finite decimal
// form, so writing it as 0.916666… puts a number in the codebase that appears in
// no regulation and loses rupiah wherever it is multiplied (SPEC §2.1). Nothing
// in this package ever divides to produce a rate; it carries the fraction whole
// and divides once, at the rounding boundary.
type Rate struct {
	Num int64
	Den int64
}

// IsZero reports a rate that levies nothing.
func (r Rate) IsZero() bool { return r.Num == 0 }

// String renders the fraction, e.g. "11/100".
func (r Rate) String() string { return fmt.Sprintf("%d/%d", r.Num, r.Den) }

// Rule is one tax_rule row as a value type (SPEC §2.1).
//
// Every field arrives from config. Nothing here has a default in Go, because a
// default in Go is a hardcoded rate wearing a hat (INV-4). Indonesian PPN moved
// three times in eighteen months and the DPP nilai lain arrived as a way to hold
// the effective rate at 11% while the statutory rate went to 12% — the shape of
// the rule changed, not just its number.
type Rule struct {
	// ID is the tax_rule row id, carried so an error and a snapshot can name
	// the exact row a figure came from.
	ID string

	// EntityID is the company the rule belongs to. The two entities here are
	// taxed differently and a rule never crosses between them.
	EntityID string

	// Type is PPN or PPnBM.
	Type Type

	// RateBP is the statutory rate in basis points: 12% is 1200. An integer,
	// so no rate is ever a float.
	RateBP int64

	// DPPNum and DPPDen are the DPP nilai lain as an exact fraction — 11/12 for
	// non-luxury PPN, 1/1 where the DPP is the full price (SPEC §2.1).
	DPPNum int64
	DPPDen int64

	// Inclusive is whether the listed price already contains the tax. It
	// decides which of the two formulas in SPEC §2.2 applies, and it is
	// snapshotted onto the sale because a receipt reprinted next year has to
	// show the same breakdown it showed at the till.
	Inclusive bool

	// Level is where rounding happens: LINE or INVOICE.
	Level Level

	// Rounding and RoundingUnit are how a fraction resolves — HALF_UP to whole
	// rupiah in every rule seeded today. RoundingUnit is in rupiah: 1 is whole
	// rupiah, 100 would round to the nearest hundred.
	Rounding     RoundingMode
	RoundingUnit int64

	// ValidFrom and ValidTo are inclusive 'YYYY-MM-DD' business dates. An empty
	// ValidTo means still in force.
	//
	// Inclusive at both ends, deliberately. "Close the old row's valid_to"
	// (SPEC §2.1) means the old rate applied through that day and the new one
	// starts the next; treating ValidTo as exclusive would tax one day's sales
	// at the wrong rate, on the one day a person is most likely to check.
	ValidFrom string
	ValidTo   string

	// LegalRef is the regulation, e.g. "PMK 131/2024". Displayed in the admin
	// UI so the owner's konsultan pajak can verify the row without reading the
	// code, which is the only way anybody outside this repo can check that the
	// system is charging what the law says.
	LegalRef string
}

// Validate rejects a rule that could not be true.
//
// Every guard here is a rate that would otherwise be applied to real money. A
// tax engine that accepts nonsense and computes confidently is worse than one
// that refuses, because the refusal is visible on the day and the nonsense is
// visible at the filing.
func (r Rule) Validate() error {
	switch {
	case r.EntityID == "":
		return fmt.Errorf("%w: no entity", ErrInvalidRule)
	case r.Type != PPN && r.Type != PPnBM:
		return fmt.Errorf("%w %s: tax type is %q, want %s or %s", ErrInvalidRule, r.ID, r.Type, PPN, PPnBM)
	case r.RateBP < 0:
		return fmt.Errorf("%w %s: rate is negative (%d bp)", ErrInvalidRule, r.ID, r.RateBP)
	case r.RateBP > 10_000:
		// 10.000 bp is 100%. A rate above it would make the tax exceed the
		// goods, and under inclusive pricing it would make the DPP smaller
		// than half the shelf price without anyone noticing.
		return fmt.Errorf("%w %s: rate is %d bp, above 100%%", ErrInvalidRule, r.ID, r.RateBP)
	case r.DPPDen <= 0:
		return fmt.Errorf("%w %s: DPP denominator is %d", ErrInvalidRule, r.ID, r.DPPDen)
	case r.DPPNum <= 0:
		return fmt.Errorf("%w %s: DPP numerator is %d", ErrInvalidRule, r.ID, r.DPPNum)
	case r.DPPNum > r.DPPDen:
		// A DPP nilai lain is a reduction of the base — 11/12 of the price,
		// never more than the price. A factor above 1 would tax an amount
		// larger than the transaction.
		return fmt.Errorf("%w %s: DPP factor %d/%d is above 1", ErrInvalidRule, r.ID, r.DPPNum, r.DPPDen)
	case r.Level != LevelLine && r.Level != LevelInvoice:
		return fmt.Errorf("%w %s: calculation level is %q, want %s or %s", ErrInvalidRule, r.ID, r.Level, LevelLine, LevelInvoice)
	case r.Rounding != HalfUp:
		return fmt.Errorf("%w %s: rounding mode %q is not implemented; only %s is", ErrInvalidRule, r.ID, r.Rounding, HalfUp)
	case r.RoundingUnit < 1:
		return fmt.Errorf("%w %s: rounding unit is %d rupiah", ErrInvalidRule, r.ID, r.RoundingUnit)
	case r.Inclusive && r.RoundingUnit != 1:
		// Under inclusive pricing the price is exact and the DPP is what gets
		// rounded, so the tax absorbs the rounding: it is price − dpp and
		// nothing else (SPEC §2.2). Round the DPP to a unit larger than a
		// rupiah and it can land above the price the customer is paying, which
		// makes the tax negative — the till would be handing PPN back on a
		// sale. At a low enough rate it does so routinely rather than at an
		// edge. A shop that rounds to the nearest hundred rounds the shelf
		// price, which is upstream of this and already whole.
		return fmt.Errorf(
			"%w %s: an inclusive rule cannot round the DPP to %d rupiah; the tax is the remainder and would go negative",
			ErrInvalidRule, r.ID, r.RoundingUnit)
	case r.LegalRef == "":
		// A rule nobody can check against a regulation is a number somebody
		// typed. The consultant reads this column, not this file.
		return fmt.Errorf("%w %s: no legal_ref", ErrInvalidRule, r.ID)
	}

	if _, err := time.Parse(DateFormat, r.ValidFrom); err != nil {
		return fmt.Errorf("%w %s: valid_from %q is not a YYYY-MM-DD date", ErrInvalidRule, r.ID, r.ValidFrom)
	}
	if r.ValidTo != "" {
		if _, err := time.Parse(DateFormat, r.ValidTo); err != nil {
			return fmt.Errorf("%w %s: valid_to %q is not a YYYY-MM-DD date", ErrInvalidRule, r.ID, r.ValidTo)
		}
		if r.ValidTo < r.ValidFrom {
			return fmt.Errorf("%w %s: valid_to %s is before valid_from %s", ErrInvalidRule, r.ID, r.ValidTo, r.ValidFrom)
		}
	}
	return nil
}

// AppliesOn reports whether the rule was in force on a business date. Both ends
// inclusive; see ValidTo.
func (r Rule) AppliesOn(businessDate string) bool {
	return businessDate >= r.ValidFrom && (r.ValidTo == "" || businessDate <= r.ValidTo)
}

// EffectiveRate is the statutory rate applied to the DPP nilai lain, reduced.
//
// For the August 2026 seed: 1200/10000 × 11/12 = 13200/120000 = 11/100. The
// reduction is not cosmetic — it keeps the numbers small enough that the
// division at the rounding boundary is exact, and it makes the rate printable
// as the fraction a regulation would state.
func (r Rule) EffectiveRate() Rate {
	num := r.RateBP * r.DPPNum
	den := int64(10_000) * r.DPPDen
	if g := gcd(num, den); g > 1 {
		num, den = num/g, den/g
	}
	return Rate{Num: num, Den: den}
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// tax computes the tax added to an exclusive base:
//
//	tax = round(base × rate_bp/10000 × dpp_num/dpp_den)
//
// One rounding, at the end. The fraction is carried whole through the
// multiplication so nothing is rounded twice (SPEC §2.2).
func (r Rule) tax(base money.IDR) money.IDR {
	rate := r.EffectiveRate()
	return r.round(base.Decimal().Mul(decimal.NewFromInt(rate.Num)), rate.Den)
}

// dppFromInclusive extracts the DPP contained in a tax-inclusive price:
//
//	dpp = round(price / (1 + effective_rate))
//
// With the rate as the exact fraction n/d, 1 + n/d is (d+n)/d, so the division
// is price × d / (d+n) — one exact multiplication and one rounded division. For
// the August 2026 PPN rule that is price × 100/111.
//
// The caller subtracts to get the tax. It must never recompute it (SPEC §2.2).
func (r Rule) dppFromInclusive(price money.IDR) money.IDR {
	rate := r.EffectiveRate()
	return r.round(price.Decimal().Mul(decimal.NewFromInt(rate.Den)), rate.Den+rate.Num)
}

// round resolves numerator/denominator to whole rupiah under the rule's
// rounding mode and unit.
//
// DivRound rounds half away from zero at the precision given, which is HALF_UP
// for the positive amounts a sale deals in. Rounding to a unit is
// round(x/unit)×unit, so a unit of 1 is ordinary whole-rupiah rounding and the
// column stays honest about what it does.
//
// Validate has already rejected every other mode, so there is no branch here to
// get wrong; a mode this function cannot honour never reaches it.
func (r Rule) round(numerator decimal.Decimal, denominator int64) money.IDR {
	unit := decimal.NewFromInt(r.RoundingUnit)
	den := decimal.NewFromInt(denominator).Mul(unit)
	return money.FromDecimal(numerator.DivRound(den, 0).Mul(unit))
}
