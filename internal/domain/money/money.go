package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// IDR is a whole number of rupiah.
//
// Rupiah has no circulating subunit, so every amount the business actually
// deals in is an integer (SPEC §1). The type is distinct from int64 on purpose:
// a quantity, a basis-point rate, and an amount of money are all int64 in
// memory and must never be interchangeable by accident.
//
// int64 holds roughly 9.2 quintillion rupiah. The PKP threshold is 4.8 billion,
// so overflow is not a scenario this business can reach and is not guarded.
type IDR int64

// Zero is the additive identity, spelled out so comparisons read as intent.
const Zero IDR = 0

var (
	// ErrNegativeWeight is returned when Allocate is given a negative weight.
	ErrNegativeWeight = errors.New("money: allocation weight is negative")
	// ErrNoWeight is returned when Allocate is given nothing to allocate across.
	ErrNoWeight = errors.New("money: allocation weights sum to zero")
	// ErrFractional is returned when a fractional value is decoded into IDR.
	ErrFractional = errors.New("money: rupiah is a whole number, refusing fractional value")
	// ErrMalformed is returned when a string cannot be read as rupiah.
	ErrMalformed = errors.New("money: malformed rupiah value")
)

// --- arithmetic -------------------------------------------------------------

// Add returns a + b.
func (a IDR) Add(b IDR) IDR { return a + b }

// Sub returns a - b.
func (a IDR) Sub(b IDR) IDR { return a - b }

// MulQty returns a multiplied by a whole quantity. Quantities are counts of
// physical units, so this stays in integer arithmetic end to end.
func (a IDR) MulQty(qty int64) IDR { return IDR(int64(a) * qty) }

// Neg returns -a. Reversals are compensating records (INV-2), so this is how a
// return or void expresses itself, not by mutating the original.
func (a IDR) Neg() IDR { return -a }

// Abs returns the magnitude of a.
func (a IDR) Abs() IDR {
	if a < 0 {
		return -a
	}
	return a
}

// IsZero reports whether a is exactly zero.
func (a IDR) IsZero() bool { return a == 0 }

// IsPositive reports whether a is greater than zero.
func (a IDR) IsPositive() bool { return a > 0 }

// IsNegative reports whether a is less than zero.
func (a IDR) IsNegative() bool { return a < 0 }

// Sum totals any number of amounts.
func Sum(vals ...IDR) IDR {
	var total IDR
	for _, v := range vals {
		total += v
	}
	return total
}

// Min returns the smaller of a and b.
func Min(a, b IDR) IDR {
	if a < b {
		return a
	}
	return b
}

// Max returns the larger of a and b.
func Max(a, b IDR) IDR {
	if a > b {
		return a
	}
	return b
}

// --- the decimal boundary ---------------------------------------------------

// Decimal lifts an amount into exact decimal arithmetic.
//
// Everything fractional — percentage discounts, inclusive-price division, FIFO
// unit costs — happens in decimal and is rounded back exactly once, at the
// boundary, by FromDecimal. A decimal is never stored and never serialised
// (SPEC §1).
func (a IDR) Decimal() decimal.Decimal { return decimal.NewFromInt(int64(a)) }

// FromDecimal rounds a decimal back to whole rupiah, half away from zero.
//
// Half-up is the rounding mode the tax rules specify (SPEC §2.1,
// tax_rule.rounding_mode). Round once, here, at the edge — rounding at every
// intermediate step is how a total stops matching the sum of its parts.
func FromDecimal(d decimal.Decimal) IDR { return IDR(d.Round(0).IntPart()) }

// MulRatio multiplies by the exact fraction num/den and rounds once.
//
// The DPP nilai lain is a fraction — PPN non-luxury is 11/12 — and it is
// carried as one all the way through. There is no 0.916666… anywhere in this
// codebase (SPEC §2.1).
func (a IDR) MulRatio(num, den int64) IDR {
	if den == 0 {
		panic("money: MulRatio denominator is zero")
	}
	return FromDecimal(a.Decimal().Mul(decimal.NewFromInt(num)).Div(decimal.NewFromInt(den)))
}

// Percent applies a rate given in basis points and rounds once.
// 12% is 1200 basis points; rates are integers so no float ever appears.
func (a IDR) Percent(bp int64) IDR { return a.MulRatio(bp, 10_000) }

// --- allocation -------------------------------------------------------------

// Allocate splits total across weights so that the parts sum to exactly total.
//
// Integer division loses rupiah: three ways on Rp 100 gives 33 + 33 + 33 = 99.
// The missing rupiah are handed out one at a time, largest fractional remainder
// first, ties broken by position so the result is deterministic.
//
// This is what keeps an invoice-level figure equal to the sum of its lines
// (SPEC §2.2's property: Σ dpp + Σ tax == grand_total, zero drift). Weights are
// typically line amounts or quantities.
func Allocate(total IDR, weights []int64) ([]IDR, error) {
	if len(weights) == 0 {
		return nil, ErrNoWeight
	}

	var sumW int64
	for _, w := range weights {
		if w < 0 {
			return nil, ErrNegativeWeight
		}
		sumW += w
	}
	if sumW == 0 {
		return nil, ErrNoWeight
	}

	// Work on the magnitude so truncation behaves the same for refunds as for
	// sales, then restore the sign. Truncating a negative rounds toward zero,
	// which would scatter the remainder differently.
	sign := int64(1)
	mag := int64(total)
	if mag < 0 {
		sign, mag = -1, -mag
	}

	parts := make([]IDR, len(weights))
	remainders := make([]int64, len(weights))
	var handedOut int64

	// The product goes through decimal rather than int64.
	//
	// total x weight overflows a signed 64-bit integer well inside the range
	// this system trades in: a single invoice near the Rp 4,8 miliar PKP
	// threshold, allocated against a weight of the same size, is about 2,4e19
	// against a ceiling of 9,2e18. The overflow wraps negative, every remainder
	// comes out below the sentinel the loop below starts from, and the
	// allocation panics rather than returning a wrong figure — which is the
	// better of the two failures, but neither belongs in a till.
	//
	// QuoRem gives the exact integer quotient and the exact remainder, so the
	// arithmetic is what it always was and only its range has changed.
	magD, sumD := decimal.NewFromInt(mag), decimal.NewFromInt(sumW)
	for i, w := range weights {
		share, rem := magD.Mul(decimal.NewFromInt(w)).QuoRem(sumD, 0)
		parts[i] = IDR(sign * share.IntPart())
		// The fractional part, scaled by sumW. Strictly below sumW, so it fits
		// even when the product it came from did not.
		remainders[i] = rem.IntPart()
		handedOut += share.IntPart()
	}

	// Distribute what truncation dropped, largest remainder first.
	for left := mag - handedOut; left > 0; left-- {
		best, bestRem := -1, int64(-1)
		for i, rem := range remainders {
			if rem > bestRem {
				best, bestRem = i, rem
			}
		}
		parts[best] += IDR(sign)
		remainders[best] = -1 // spent; never wins again
	}

	return parts, nil
}

// --- formatting -------------------------------------------------------------

// String renders the amount the way it is written in Indonesia: Rp 1.234.567,
// thousands separated by full stops, no decimal part.
func (a IDR) String() string {
	sign := ""
	n := int64(a)
	if n < 0 {
		sign, n = "-", -n
	}
	return sign + "Rp " + group(strconv.FormatInt(n, 10))
}

// Plain renders the digits with separators but no Rp prefix, for table columns
// that carry the unit in the header.
func (a IDR) Plain() string {
	sign := ""
	n := int64(a)
	if n < 0 {
		sign, n = "-", -n
	}
	return sign + group(strconv.FormatInt(n, 10))
}

func group(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// Parse reads rupiah written the Indonesian way, with or without the Rp prefix
// and full-stop thousands separators: "Rp 1.234.567", "1.234.567", "-1234567".
//
// A comma is the decimal separator in Indonesian, so anything containing one is
// fractional and is refused rather than silently truncated (INV-1).
func Parse(s string) (IDR, error) {
	t := strings.TrimSpace(s)

	neg := false
	if strings.HasPrefix(t, "-") { // "-Rp 1.000"
		neg, t = true, strings.TrimSpace(t[1:])
	}
	if len(t) >= 2 && strings.EqualFold(t[:2], "Rp") {
		t = strings.TrimSpace(t[2:])
	}
	if strings.HasPrefix(t, "-") { // "Rp -1.000"
		neg, t = !neg, strings.TrimSpace(t[1:])
	}

	if strings.Contains(t, ",") {
		return 0, fmt.Errorf("%w: %q", ErrFractional, s)
	}
	t = strings.ReplaceAll(t, ".", "")
	t = strings.ReplaceAll(t, " ", "")

	if t == "" {
		return 0, fmt.Errorf("%w: %q", ErrMalformed, s)
	}
	for _, r := range t {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: %q", ErrMalformed, s)
		}
	}

	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrMalformed, s)
	}
	if neg {
		n = -n
	}
	return IDR(n), nil
}

// --- JSON -------------------------------------------------------------------

// MarshalJSON emits a bare JSON integer.
//
// Rp 4.8 billion is far inside MAX_SAFE_INTEGER, so the browser receives it
// losslessly and types it as a branded IDR (SPEC §1). Never a float, never a
// pre-formatted string the client has to parse back.
func (a IDR) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(a), 10)), nil
}

// UnmarshalJSON accepts a JSON integer and refuses anything fractional.
//
// This is INV-1 enforced at the wire, not merely intended: a client that sends
// 1000.5 gets an error rather than a silent truncation to 1000 that nobody
// notices until a settlement doesn't balance.
func (a *IDR) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	if t == "null" {
		return nil
	}
	t = strings.Trim(t, `"`) // tolerate a quoted integer; still no fractions

	// A JSON number containing '.', 'e' or 'E' is fractional or scientific.
	// Detected by inspection rather than by parsing it as a float — this
	// package exists so that no float ever touches an amount of money.
	if strings.ContainsAny(t, ".eE") {
		return fmt.Errorf("%w: %s", ErrFractional, t)
	}

	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrMalformed, t)
	}
	*a = IDR(n)
	return nil
}
