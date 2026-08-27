package tax_test

import (
	"math/rand/v2"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/tax"
)

// TASKS 5.7: for any cart, Σ dpp + Σ tax == grand_total, with zero drift.
//
// A worked example proves one basket. This proves the shape of the arithmetic
// over carts nobody would think to write down: a hundred lines of Rp 1, a cart
// discounted to nothing, prices that land on an exact half rupiah, exempt lines
// scattered through taxed ones. Rounding bugs live in exactly those places, and
// the symptom is a receipt whose parts do not add up to its total.
//
// Deterministic by construction. The generator is seeded with a fixed pair so a
// failure is reproducible from the case number alone -- a property test that
// fails once a fortnight in CI and never again teaches nobody anything.
const (
	propertySeed1 = 0x7e7a_0000_0000_0005 // "tera", TASKS 5.7
	propertySeed2 = 0x0000_0000_0000_2026
	propertyCases = 20_000
)

// generatedCart is one random basket plus the rule it is priced under.
type generatedCart struct {
	seller tax.Seller
	cart   tax.Cart
	rules  tax.RuleSet
	rule   tax.Rule
	isPKP  bool
}

func generateCart(t *testing.T, rng *rand.Rand) generatedCart {
	t.Helper()

	const entityID = "entity-under-test"

	// Rates worth generating: the August 2026 seed, the statutory rate on a
	// full DPP, an old 11% rate, and zero. A rate of zero is legitimate config
	// -- PPN dibebaskan is expressed that way -- and it is where an engine that
	// divides by the rate rather than by (1 + rate) falls over.
	rates := [][3]int64{
		{1200, 11, 12}, // PMK 131/2024: 12% on 11/12 -> effective 11%
		{1200, 1, 1},   // statutory 12% on the full price
		{1100, 1, 1},   // the pre-2025 11%
		{0, 1, 1},      // levies nothing
	}
	r := rates[rng.IntN(len(rates))]

	inclusive := rng.IntN(2) == 0

	// Whole rupiah is what every real rule uses. A coarser unit is generated
	// only for exclusive rules, because Validate refuses it on an inclusive one
	// — where the tax is the remainder, rounding the base to the nearest
	// hundred can push it past the price and hand PPN back. This test found
	// that; the guard is in Rule.Validate.
	unit := int64(1)
	if !inclusive {
		unit = []int64{1, 1, 1, 100}[rng.IntN(4)]
	}

	rule := tax.Rule{
		ID: "rule-generated", EntityID: entityID, Type: tax.PPN,
		RateBP: r[0], DPPNum: r[1], DPPDen: r[2],
		Inclusive:    inclusive,
		Level:        []tax.Level{tax.LevelLine, tax.LevelInvoice}[rng.IntN(2)],
		Rounding:     tax.HalfUp,
		RoundingUnit: unit,
		ValidFrom:    "2025-01-01",
		LegalRef:     "generated",
	}

	rules, err := tax.NewRuleSet(rule)
	if err != nil {
		t.Fatalf("generated rule is not valid: %v", err)
	}

	// Amounts skewed towards the small and awkward. Rp 1 and Rp 5 find the
	// rounding edges that Rp 1.000.000 never will.
	lineCount := 1 + rng.IntN(12)
	if rng.IntN(20) == 0 {
		lineCount = 1 + rng.IntN(120) // occasionally a wholesale-sized invoice
	}

	cart := tax.Cart{
		BusinessDate: "2026-08-21",
		FakturIssued: rng.IntN(2) == 0,
		Lines:        make([]tax.Line, 0, lineCount),
	}
	for i := range lineCount {
		var amount int64
		switch rng.IntN(6) {
		case 0:
			amount = int64(rng.IntN(10)) // includes zero: a line given away
		case 1:
			amount = int64(rng.IntN(1_000))
		case 2:
			amount = int64(50 + 100*rng.IntN(50)) // lands on an exact half rupiah at 11%
		case 3:
			amount = int64(rng.IntN(100_000))
		case 4:
			amount = int64(rng.IntN(10_000_000))
		default:
			amount = int64(rng.IntN(500)) * 500
		}
		cart.Lines = append(cart.Lines, tax.Line{
			Ref:    string(rune('A' + i%26)),
			Amount: money.IDR(amount),
			Exempt: rng.IntN(5) == 0,
		})
	}

	isPKP := rng.IntN(8) != 0 // mostly PKP; the non-PKP path is asserted below too
	g := generatedCart{
		seller: tax.Seller{EntityID: entityID, IsPKP: isPKP},
		cart:   cart, rules: rules, rule: rule, isPKP: isPKP,
	}
	if !isPKP {
		// A non-PKP entity holds no rule in force and issues no faktur; both
		// are refused, and refusing them is what the other tests assert.
		g.rules = tax.RuleSet{}
		g.cart.FakturIssued = false
	}
	return g
}

func TestPropertyPartsAlwaysSumToTheWhole(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(propertySeed1, propertySeed2))

	for n := range propertyCases {
		g := generateCart(t, rng)

		got, err := tax.Calculate(g.seller, g.cart, g.rules)
		if err != nil {
			t.Fatalf("case %d: Calculate: %v", n, err)
		}

		var (
			sumAmount, sumDPP, sumTax, sumTotal money.IDR
		)
		for _, l := range got.Lines {
			sumAmount = sumAmount.Add(l.Amount)
			sumDPP = sumDPP.Add(l.DPP)
			sumTax = sumTax.Add(l.Tax)
			sumTotal = sumTotal.Add(l.Total)

			// The line-level statement of the same property. A cart can sum
			// correctly while an individual line does not, and the line is what
			// a customer queries.
			if l.DPP.Add(l.Tax) != l.Total {
				t.Fatalf("case %d line %s: DPP %s + PPN %s != total %s", n, l.Ref, l.DPP, l.Tax, l.Total)
			}
			if l.DPP.IsNegative() || l.Tax.IsNegative() {
				t.Fatalf("case %d line %s: negative DPP %s or PPN %s", n, l.Ref, l.DPP, l.Tax)
			}
			if !l.Taxed && !l.Tax.IsZero() {
				t.Fatalf("case %d line %s: untaxed line carries PPN %s", n, l.Ref, l.Tax)
			}
		}

		// TASKS 5.7, stated four ways: the lines against each other, the lines
		// against the totals, and the totals against the whole.
		if sumDPP.Add(sumTax) != sumTotal {
			t.Fatalf("case %d: Σ DPP %s + Σ PPN %s != Σ total %s", n, sumDPP, sumTax, sumTotal)
		}
		if sumDPP != got.DPP || sumTax != got.Tax || sumTotal != got.GrandTotal {
			t.Fatalf("case %d: line sums (%s, %s, %s) disagree with the totals (%s, %s, %s)",
				n, sumDPP, sumTax, sumTotal, got.DPP, got.Tax, got.GrandTotal)
		}
		if got.DPP.Add(got.Tax) != got.GrandTotal {
			t.Fatalf("case %d: DPP %s + PPN %s != grand total %s", n, got.DPP, got.Tax, got.GrandTotal)
		}

		// Under inclusive pricing the customer pays the sum of the labels, to
		// the rupiah. This is the property the "subtract, never recompute" rule
		// of SPEC §2.2 exists to guarantee, and it is where the one-rupiah gap
		// would show up.
		if got.Taxed && g.rule.Inclusive && got.GrandTotal != sumAmount {
			t.Fatalf("case %d: inclusive cart totalled %s against labels of %s", n, got.GrandTotal, sumAmount)
		}
		// Under exclusive pricing the tax is added on top, and nothing else is.
		if got.Taxed && !g.rule.Inclusive && got.GrandTotal != sumAmount.Add(got.Tax) {
			t.Fatalf("case %d: exclusive cart totalled %s against a base of %s plus PPN %s",
				n, got.GrandTotal, sumAmount, got.Tax)
		}

		// TASKS 5.5, as a property rather than a convention: no basket, no
		// rate, and no rounding produces PPN at a non-PKP entity.
		if !g.isPKP {
			if !got.Tax.IsZero() || got.Taxed {
				t.Fatalf("case %d: non-PKP entity charged PPN of %s", n, got.Tax)
			}
			if got.DPP != sumAmount || got.GrandTotal != sumAmount {
				t.Fatalf("case %d: non-PKP totals moved: DPP %s, total %s, amounts %s", n, got.DPP, got.GrandTotal, sumAmount)
			}
		}
	}
}

// TestPropertyFakturNeverChangesTheTax is TASKS 5.4 stated as a property.
//
// A PKP owes output PPN on the delivery of taxable goods whether or not the
// buyer took a faktur (SPEC §2.3). The fixture cases assert it on two baskets;
// this asserts it on every basket the generator can produce, which is the
// version that survives someone later threading the faktur flag into the
// pricing code for what will look at the time like a good reason.
func TestPropertyFakturNeverChangesTheTax(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(propertySeed1, propertySeed2+1))

	for n := range propertyCases / 4 {
		g := generateCart(t, rng)
		if !g.isPKP {
			continue // a non-PKP entity cannot issue one at all; asserted elsewhere
		}

		with, cart := g.cart, g.cart
		with.FakturIssued, cart.FakturIssued = true, false

		issued, err := tax.Calculate(g.seller, with, g.rules)
		if err != nil {
			t.Fatalf("case %d with faktur: %v", n, err)
		}
		notIssued, err := tax.Calculate(g.seller, cart, g.rules)
		if err != nil {
			t.Fatalf("case %d without faktur: %v", n, err)
		}

		if issued.Tax != notIssued.Tax || issued.DPP != notIssued.DPP || issued.GrandTotal != notIssued.GrandTotal {
			t.Fatalf("case %d: issuing a faktur moved the tax: with %s, without %s", n, issued.Tax, notIssued.Tax)
		}
		for i := range issued.Lines {
			if issued.Lines[i] != notIssued.Lines[i] {
				t.Fatalf("case %d line %d: issuing a faktur moved the line", n, i+1)
			}
		}

		// The one thing that does change is the flag the PPN position report
		// reads, and only when there is a liability to be quiet about.
		wantFlag := notIssued.Tax.IsPositive()
		if notIssued.AccruedWithoutFaktur != wantFlag {
			t.Fatalf("case %d: accrued-without-faktur is %v on PPN of %s", n, notIssued.AccruedWithoutFaktur, notIssued.Tax)
		}
		if issued.AccruedWithoutFaktur {
			t.Fatalf("case %d: a sale with a faktur is flagged as accruing without one", n)
		}
	}
}
