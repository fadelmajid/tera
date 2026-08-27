package tax_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/tax"
)

func pkpSeller() tax.Seller { return tax.Seller{EntityID: "entity-pkp", IsPKP: true} }

func inclusiveSet(t *testing.T) tax.RuleSet {
	t.Helper()

	set, err := tax.NewRuleSet(validRule())
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	return set
}

func cartOf(amounts ...money.IDR) tax.Cart {
	c := tax.Cart{BusinessDate: "2026-08-21", FakturIssued: true}
	for i, a := range amounts {
		c.Lines = append(c.Lines, tax.Line{Ref: string(rune('A' + i)), Amount: a})
	}
	return c
}

func TestCalculateRejectsACartThatCouldNotBeTrue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		seller tax.Seller
		cart   tax.Cart
		want   error
	}{
		{
			name:   "no selling entity",
			seller: tax.Seller{IsPKP: true},
			cart:   cartOf(1000),
			want:   tax.ErrInvalidCart,
		},
		{
			name:   "no lines",
			seller: pkpSeller(),
			cart:   tax.Cart{BusinessDate: "2026-08-21"},
			want:   tax.ErrInvalidCart,
		},
		{
			name:   "business date is not a date",
			seller: pkpSeller(),
			cart:   tax.Cart{BusinessDate: "21 Agustus 2026", Lines: cartOf(1000).Lines},
			want:   tax.ErrInvalidCart,
		},
		{
			// A negative line would be a refund, and a refund reverses the tax
			// snapshotted on the original sale rather than pricing a fresh one
			// against today's rate (INV-3).
			name:   "a negative line",
			seller: pkpSeller(),
			cart:   cartOf(1000, -500),
			want:   tax.ErrInvalidCart,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tax.Calculate(tt.seller, tt.cart, inclusiveSet(t)); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

// TestARateChangeDoesNotReachAnEarlierSale is INV-3 at the domain level.
//
// The store-level version of this (TASKS 5.11) proves a settled sale keeps its
// snapshot. This proves the layer underneath: a cart dated before a new rule's
// valid_from is priced by the old rule, whatever else is in config now.
func TestARateChangeDoesNotReachAnEarlierSale(t *testing.T) {
	t.Parallel()

	old := rule("eleven-percent-statutory", "2022-04-01", "2024-12-31")
	old.RateBP, old.DPPNum, old.DPPDen = 1100, 1, 1 // a plain 11%, no nilai lain
	current := rule("twelve-on-eleven-twelfths", "2025-01-01", "")

	set, err := tax.NewRuleSet(old, current)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	// The two rules give the same effective rate by design — that is the whole
	// point of the nilai lain — so the figures match and only the citation
	// differs. A report that showed PMK 131/2024 against a 2023 sale would be
	// wrong in a way no total would reveal.
	before := cartOf(1_110_000)
	before.BusinessDate = "2023-06-15"

	got, err := tax.Calculate(pkpSeller(), before, set)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if got.Rule.ID != old.ID {
		t.Fatalf("a 2023 sale was priced under rule %s", got.Rule.ID)
	}
	if got.DPP != 1_000_000 || got.Tax != 110_000 {
		t.Fatalf("DPP %s + PPN %s, want Rp 1.000.000 + Rp 110.000", got.DPP, got.Tax)
	}

	after := cartOf(1_110_000)
	got, err = tax.Calculate(pkpSeller(), after, set)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if got.Rule.ID != current.ID {
		t.Fatalf("a 2026 sale was priced under rule %s", got.Rule.ID)
	}
}

// TestTheRuleIsCarriedOntoTheResult. INV-3: the service snapshots these fields
// onto the sale rather than storing a reference to the config row, so this is
// the handover.
func TestTheRuleIsCarriedOntoTheResult(t *testing.T) {
	t.Parallel()

	got, err := tax.Calculate(pkpSeller(), cartOf(1_110_000), inclusiveSet(t))
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	want := validRule()
	if got.Rule != want {
		t.Fatalf("the result carries rule %+v, want %+v", got.Rule, want)
	}
	if !got.Taxed {
		t.Error("a taxed sale is not marked taxed")
	}
}

func TestCalculateEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level tax.Level
		lines []tax.Line
		// wantDPP, wantTax and wantTotal are the invoice figures.
		wantDPP, wantTax, wantTotal money.IDR
		wantTaxed                   bool
	}{
		{
			// A cart discounted to nothing, or a giveaway. The invoice-level
			// path allocates by line weight and every weight is zero, which is
			// the shape that would otherwise fail rather than return nil tax.
			name:  "a cart worth nothing, rounded on the invoice",
			level: tax.LevelInvoice,
			lines: []tax.Line{{Ref: "A"}, {Ref: "B"}},
		},
		{
			name:  "a cart worth nothing, rounded on each line",
			level: tax.LevelLine,
			lines: []tax.Line{{Ref: "A"}, {Ref: "B"}},
		},
		{
			// Every line exempt, so there is nothing to allocate the invoice
			// figure across.
			name:  "every line exempt, rounded on the invoice",
			level: tax.LevelInvoice,
			lines: []tax.Line{
				{Ref: "A", Amount: 250_000, Exempt: true},
				{Ref: "B", Amount: 100_000, Exempt: true},
			},
			wantDPP: 350_000, wantTotal: 350_000,
		},
		{
			// A single rupiah. The DPP of Rp 1/1,11 is Rp 0,9009, which rounds
			// to Rp 1, leaving no tax at all.
			name:      "one rupiah",
			level:     tax.LevelLine,
			lines:     []tax.Line{{Ref: "A", Amount: 1}},
			wantDPP:   1,
			wantTotal: 1,
		},
		{
			// Rp 10 divides to Rp 9,009 -> Rp 9 of DPP and Rp 1 of PPN.
			name:      "ten rupiah",
			level:     tax.LevelLine,
			lines:     []tax.Line{{Ref: "A", Amount: 10}},
			wantDPP:   9,
			wantTax:   1,
			wantTotal: 10,
			wantTaxed: true,
		},
		{
			// A zero line sitting between real ones must not swallow a share of
			// the invoice DPP or take the leftover rupiah.
			name:  "a zero line among real ones, rounded on the invoice",
			level: tax.LevelInvoice,
			lines: []tax.Line{
				{Ref: "A", Amount: 5_000},
				{Ref: "B"},
				{Ref: "C", Amount: 5_000},
			},
			wantDPP: 9_009, wantTax: 991, wantTotal: 10_000, wantTaxed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRule()
			r.Level = tt.level
			set, err := tax.NewRuleSet(r)
			if err != nil {
				t.Fatalf("NewRuleSet: %v", err)
			}

			got, err := tax.Calculate(pkpSeller(), tax.Cart{
				BusinessDate: "2026-08-21", FakturIssued: true, Lines: tt.lines,
			}, set)
			if err != nil {
				t.Fatalf("Calculate: %v", err)
			}

			if got.DPP != tt.wantDPP || got.Tax != tt.wantTax || got.GrandTotal != tt.wantTotal {
				t.Errorf("DPP %s + PPN %s = %s, want %s + %s = %s",
					got.DPP, got.Tax, got.GrandTotal, tt.wantDPP, tt.wantTax, tt.wantTotal)
			}
			if got.Taxed != tt.wantTaxed {
				t.Errorf("taxed = %v, want %v", got.Taxed, tt.wantTaxed)
			}
			if got.DPP.Add(got.Tax) != got.GrandTotal {
				t.Errorf("the parts do not sum to the whole")
			}
		})
	}
}

// TestARateOfZeroIsNotTheSameAsNoRule separates a documented exemption from a
// hole in config.
//
// PPN dibebaskan is expressed as a rule with a rate of zero: a rule is in force,
// it was applied, and it levied nothing. That is a different fact from a PKP
// entity having no rule at all, which is refused (TASKS 5.4), and the two must
// not collapse — one is a documented exemption and the other is a hole in
// config.
func TestARateOfZeroIsNotTheSameAsNoRule(t *testing.T) {
	t.Parallel()

	free := validRule()
	free.RateBP = 0
	set, err := tax.NewRuleSet(free)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	got, err := tax.Calculate(pkpSeller(), cartOf(1_110_000), set)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if !got.Tax.IsZero() || got.DPP != 1_110_000 {
		t.Fatalf("DPP %s + PPN %s, want the whole amount untaxed", got.DPP, got.Tax)
	}
	if got.Rule.ID != free.ID {
		t.Fatal("the rule that levied nothing was not carried onto the result")
	}

	empty, err := tax.Calculate(pkpSeller(), cartOf(1_110_000), tax.RuleSet{})
	if !errors.Is(err, tax.ErrNoEffectiveRule) {
		t.Fatalf("want ErrNoEffectiveRule, got %v (%+v)", err, empty)
	}
}

// TestInsufficientRuleErrorsCarryTheirDetail. The UI is in Bahasa Indonesia and
// builds its own wording from these fields; it never shows Error() to a cashier.
func TestInsufficientRuleErrorsCarryTheirDetail(t *testing.T) {
	t.Parallel()

	_, err := tax.Calculate(pkpSeller(), cartOf(1_000), tax.RuleSet{})

	var missing *tax.NoEffectiveRuleError
	if !errors.As(err, &missing) {
		t.Fatalf("want a *NoEffectiveRuleError, got %v", err)
	}
	if missing.EntityID != "entity-pkp" || missing.BusinessDate != "2026-08-21" || missing.Type != tax.PPN {
		t.Fatalf("the error does not say which entity, tax and day: %+v", missing)
	}

	staged := validRule()
	staged.EntityID = "entity-nonpkp"
	set, err := tax.NewRuleSet(staged)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	walkIn := cartOf(1_000)
	walkIn.FakturIssued = false // the faktur refusal is a separate case
	_, err = tax.Calculate(tax.Seller{EntityID: "entity-nonpkp"}, walkIn, set)

	var contradiction *tax.NonPKPChargeError
	if !errors.As(err, &contradiction) {
		t.Fatalf("want a *NonPKPChargeError, got %v", err)
	}
	if contradiction.RuleID != staged.ID || contradiction.LegalRef != staged.LegalRef {
		t.Fatalf("the error does not name the rule to go and look at: %+v", contradiction)
	}
	if contradiction.ValidFrom != staged.ValidFrom {
		t.Fatalf("the error does not say since when it has been under-charging: %+v", contradiction)
	}
}
