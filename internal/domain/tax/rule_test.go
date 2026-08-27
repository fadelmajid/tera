package tax_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/tax"
)

// validRule is the August 2026 PPN rule: 12% statutory on a DPP nilai lain of
// 11/12, giving an effective 11% (SPEC §2.1).
func validRule() tax.Rule {
	return tax.Rule{
		ID: "rule-1", EntityID: "entity-pkp", Type: tax.PPN,
		RateBP: 1200, DPPNum: 11, DPPDen: 12,
		Inclusive: true, Level: tax.LevelInvoice,
		Rounding: tax.HalfUp, RoundingUnit: 1,
		ValidFrom: "2025-01-01", LegalRef: "PMK 131/2024",
	}
}

func TestRuleValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*tax.Rule)
		want   error
	}{
		{"the seeded rule", func(*tax.Rule) {}, nil},
		{"closed rule", func(r *tax.Rule) { r.ValidTo = "2026-12-31" }, nil},
		{"exclusive rule may round to a coarser unit", func(r *tax.Rule) {
			r.Inclusive, r.RoundingUnit = false, 100
		}, nil},

		{"no entity", func(r *tax.Rule) { r.EntityID = "" }, tax.ErrInvalidRule},
		{"unknown tax type", func(r *tax.Rule) { r.Type = "PB1" }, tax.ErrInvalidRule},
		{"negative rate", func(r *tax.Rule) { r.RateBP = -1 }, tax.ErrInvalidRule},
		{"rate above 100 per cent", func(r *tax.Rule) { r.RateBP = 10_001 }, tax.ErrInvalidRule},
		{"zero DPP denominator", func(r *tax.Rule) { r.DPPDen = 0 }, tax.ErrInvalidRule},
		{"zero DPP numerator", func(r *tax.Rule) { r.DPPNum = 0 }, tax.ErrInvalidRule},
		// A DPP nilai lain reduces the base; a factor above one would tax more
		// than the transaction.
		{"DPP factor above one", func(r *tax.Rule) { r.DPPNum, r.DPPDen = 13, 12 }, tax.ErrInvalidRule},
		{"unknown calculation level", func(r *tax.Rule) { r.Level = "CART" }, tax.ErrInvalidRule},
		// The column exists for INV-4, not because a second mode is wanted. A
		// mode nothing implements must refuse rather than approximate.
		{"unimplemented rounding mode", func(r *tax.Rule) { r.Rounding = "HALF_EVEN" }, tax.ErrInvalidRule},
		{"zero rounding unit", func(r *tax.Rule) { r.RoundingUnit = 0 }, tax.ErrInvalidRule},
		// Found by the property test: the tax is the remainder under inclusive
		// pricing, so rounding the base to a coarser unit can push it past the
		// price and hand PPN back.
		{"inclusive rule rounding to a coarser unit", func(r *tax.Rule) { r.RoundingUnit = 100 }, tax.ErrInvalidRule},
		// A rule nobody can check against a regulation is a number somebody
		// typed. The konsultan pajak reads this column.
		{"no legal reference", func(r *tax.Rule) { r.LegalRef = "" }, tax.ErrInvalidRule},
		{"malformed valid_from", func(r *tax.Rule) { r.ValidFrom = "1 Jan 2025" }, tax.ErrInvalidRule},
		{"malformed valid_to", func(r *tax.Rule) { r.ValidTo = "31/12/2026" }, tax.ErrInvalidRule},
		{"valid_to before valid_from", func(r *tax.Rule) { r.ValidTo = "2024-12-31" }, tax.ErrInvalidRule},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRule()
			tt.mutate(&r)

			err := r.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

// TestEffectiveRateIsAnExactFraction is SPEC §2.1's central claim.
//
// The DPP nilai lain has no finite decimal form. Carried as a fraction and
// reduced, 12% of 11/12 is exactly 11/100 — the same number the regulation the
// arrangement exists to implement is describing.
func TestEffectiveRateIsAnExactFraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		rateBP           int64
		num, den         int64
		wantNum, wantDen int64
	}{
		{"PPN non-luxury: 12% on 11/12 is exactly 11/100", 1200, 11, 12, 11, 100},
		{"PPnBM luxury: 12% on the full price is 3/25", 1200, 1, 1, 3, 25},
		{"the pre-2025 statutory 11%", 1100, 1, 1, 11, 100},
		{"10% on 11/12", 1000, 11, 12, 11, 120},
		{"a rate that levies nothing", 0, 11, 12, 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRule()
			r.RateBP, r.DPPNum, r.DPPDen = tt.rateBP, tt.num, tt.den

			got := r.EffectiveRate()
			if got.Num != tt.wantNum || got.Den != tt.wantDen {
				t.Fatalf("effective rate = %s, want %d/%d", got, tt.wantNum, tt.wantDen)
			}
		})
	}
}

// TestAppliesOnIsInclusiveAtBothEnds pins the boundary day.
//
// "Close the old row's valid_to" (SPEC §2.1) means the old rate applied through
// that day. Reading valid_to as exclusive would tax one day's sales at the wrong
// rate — and it would be the changeover day, which is the day somebody checks.
func TestAppliesOnIsInclusiveAtBothEnds(t *testing.T) {
	t.Parallel()

	r := validRule()
	r.ValidFrom, r.ValidTo = "2025-01-01", "2026-12-31"

	tests := []struct {
		date string
		want bool
	}{
		{"2024-12-31", false},
		{"2025-01-01", true}, // the first day it applies
		{"2026-08-21", true},
		{"2026-12-31", true}, // the last day it applies
		{"2027-01-01", false},
	}

	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			t.Parallel()

			if got := r.AppliesOn(tt.date); got != tt.want {
				t.Fatalf("AppliesOn(%s) = %v, want %v", tt.date, got, tt.want)
			}
		})
	}

	open := validRule()
	open.ValidFrom, open.ValidTo = "2025-01-01", ""
	if !open.AppliesOn("2099-01-01") {
		t.Error("an open-ended rule stopped applying")
	}
}

// TestNoFloatLiteralInTheDomain is INV-1 and SPEC §2.1 checked mechanically.
//
// The linter forbids the float types in internal/domain, which catches a
// conversion but not a literal: writing 0.9166 as an untyped constant slips
// through, and that is precisely the number SPEC §2.1 says must not exist —
// the DPP nilai lain flattened into a decimal that appears in no regulation and
// loses rupiah wherever it is multiplied.
//
// Parsed rather than grepped, so a doc comment can name the bad value in order
// to warn about it — as several here do — without tripping the check.
func TestNoFloatLiteralInTheDomain(t *testing.T) {
	t.Parallel()

	const domainDir = "../../../internal/domain"

	fset := token.NewFileSet()
	err := filepath.WalkDir(domainDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.FLOAT {
				t.Errorf("%s: float literal %s — money is never a float (INV-1), and a rate is an exact fraction (SPEC §2.1)",
					fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", domainDir, err)
	}
}
