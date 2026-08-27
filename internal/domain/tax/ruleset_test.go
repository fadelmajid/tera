package tax_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/tax"
)

// rule builds a PPN rule for one entity over a window, for the tests below.
func rule(id, from, to string) tax.Rule {
	r := validRule()
	r.ID, r.ValidFrom, r.ValidTo = id, from, to
	return r
}

func TestNewRuleSetRejectsOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []tax.Rule
		want  error
	}{
		{
			// The discipline SPEC §2.1 asks for, done properly: the old row is
			// closed on the day before the new one opens.
			name: "adjacent windows",
			rules: []tax.Rule{
				rule("old", "2022-04-01", "2024-12-31"),
				rule("new", "2025-01-01", ""),
			},
		},
		{
			name:  "one open-ended rule",
			rules: []tax.Rule{rule("only", "2025-01-01", "")},
		},
		{
			name:  "no rules at all is what a non-PKP entity has",
			rules: nil,
		},
		{
			// The half-done rate change: a new row inserted, the old one never
			// closed. Both cover today and which one a sale gets depends on row
			// order, so some sales are taxed at the new rate and some at the
			// old.
			name: "the old rule was never closed",
			rules: []tax.Rule{
				rule("old", "2022-04-01", ""),
				rule("new", "2025-01-01", ""),
			},
			want: tax.ErrRuleOverlap,
		},
		{
			name: "the old rule was closed a day too late",
			rules: []tax.Rule{
				rule("old", "2022-04-01", "2025-01-01"),
				rule("new", "2025-01-01", ""),
			},
			want: tax.ErrRuleOverlap,
		},
		{
			name: "one rule swallows another",
			rules: []tax.Rule{
				rule("wide", "2022-04-01", "2030-12-31"),
				rule("inner", "2025-01-01", "2025-12-31"),
			},
			want: tax.ErrRuleOverlap,
		},
		{
			name: "insertion order does not hide an overlap",
			rules: []tax.Rule{
				rule("new", "2025-01-01", ""),
				rule("old", "2022-04-01", ""),
			},
			want: tax.ErrRuleOverlap,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tax.NewRuleSet(tt.rules...)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("NewRuleSet: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

// TestRulesOfDifferentTypesMayCoincide: PPN and PPnBM run alongside each other
// by design. Only two rules of the same type covering one day is an overlap.
func TestRulesOfDifferentTypesMayCoincide(t *testing.T) {
	t.Parallel()

	ppn := rule("ppn", "2025-01-01", "")
	luxury := rule("ppnbm", "2025-01-01", "")
	luxury.Type, luxury.DPPNum, luxury.DPPDen = tax.PPnBM, 1, 1

	set, err := tax.NewRuleSet(ppn, luxury)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	if _, ok := set.Effective(tax.PPN, "2026-08-21"); !ok {
		t.Error("no PPN rule in force")
	}
	if _, ok := set.Effective(tax.PPnBM, "2026-08-21"); !ok {
		t.Error("no PPnBM rule in force")
	}
}

// TestOneRuleSetIsOneCompany. One entity here is PKP and the other legally
// cannot charge, so a set holding both is pointed at whichever sale reads it.
func TestOneRuleSetIsOneCompany(t *testing.T) {
	t.Parallel()

	mine := rule("mine", "2025-01-01", "")
	theirs := rule("theirs", "2025-01-01", "")
	theirs.EntityID = "entity-nonpkp"

	if _, err := tax.NewRuleSet(mine, theirs); !errors.Is(err, tax.ErrEntityMismatch) {
		t.Fatalf("want ErrEntityMismatch, got %v", err)
	}
}

func TestEffectiveSelectsByBusinessDate(t *testing.T) {
	t.Parallel()

	set, err := tax.NewRuleSet(
		rule("eleven", "2022-04-01", "2024-12-31"),
		rule("twelve-on-eleven-twelfths", "2025-01-01", ""),
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	tests := []struct {
		date string
		want string
	}{
		{"2021-12-31", ""}, // before either rule existed
		{"2022-04-01", "eleven"},
		{"2024-12-31", "eleven"}, // the last day of the old rate
		{"2025-01-01", "twelve-on-eleven-twelfths"},
		{"2026-08-21", "twelve-on-eleven-twelfths"},
	}

	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			t.Parallel()

			got, ok := set.Effective(tax.PPN, tt.date)
			if tt.want == "" {
				if ok {
					t.Fatalf("got rule %s, want none in force", got.ID)
				}
				return
			}
			if !ok {
				t.Fatalf("no rule in force, want %s", tt.want)
			}
			if got.ID != tt.want {
				t.Fatalf("got rule %s, want %s", got.ID, tt.want)
			}
		})
	}
}

// TestRulesReturnsACopy: the set is read on every sale, so a caller that sorts
// or edits what it hands back must not reach the config behind it.
func TestRulesReturnsACopy(t *testing.T) {
	t.Parallel()

	set, err := tax.NewRuleSet(rule("only", "2025-01-01", ""))
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	got := set.Rules()
	got[0].RateBP = 9999

	again, ok := set.Effective(tax.PPN, "2026-08-21")
	if !ok {
		t.Fatal("no rule in force")
	}
	if again.RateBP != 1200 {
		t.Fatalf("the rule set was edited from outside: rate is now %d bp", again.RateBP)
	}
}

// TestInvalidRuleIsRejectedAtLoad. A rate that cannot be true is caught when
// config is read, not when it reaches somebody's money.
func TestInvalidRuleIsRejectedAtLoad(t *testing.T) {
	t.Parallel()

	bad := rule("bad", "2025-01-01", "")
	bad.LegalRef = ""

	if _, err := tax.NewRuleSet(bad); !errors.Is(err, tax.ErrInvalidRule) {
		t.Fatalf("want ErrInvalidRule, got %v", err)
	}
}
