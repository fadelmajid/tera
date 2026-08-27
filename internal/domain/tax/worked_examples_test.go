package tax_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/tax"
)

// TASKS 5.6: worked examples to the rupiah, as table-driven fixtures.
//
// The fixtures live in testdata/worked_examples at the repository root rather
// than beside this package, because they are not only a test input. They are
// what gets taken to the owner's konsultan pajak: every case carries the
// reasoning for its figure in prose, so somebody who does not read Go can check
// the arithmetic and the rule behind it. Keeping them in one place across the
// tax, FIFO, margin and omzet engines is the point of that directory.
//
// Where a figure is not known, the case carries a todo instead of an expected
// value and this suite prints what the engine currently produces without
// asserting it. Inventing a figure and asserting it would manufacture exactly
// the false confidence the exercise exists to avoid.
const workedExamplesDir = "../../../testdata/worked_examples"

type fixtureFile struct {
	About        string                 `json:"about"`
	Verification string                 `json:"verification"`
	Rules        map[string]fixtureRule `json:"rules"`
	Cases        []fixtureCase          `json:"cases"`
}

type fixtureRule struct {
	ID           string `json:"id"`
	EntityID     string `json:"entity_id"`
	Type         string `json:"type"`
	RateBP       int64  `json:"rate_bp"`
	DPPNum       int64  `json:"dpp_num"`
	DPPDen       int64  `json:"dpp_den"`
	Inclusive    bool   `json:"inclusive"`
	Level        string `json:"level"`
	Rounding     string `json:"rounding"`
	RoundingUnit int64  `json:"rounding_unit"`
	ValidFrom    string `json:"valid_from"`
	ValidTo      string `json:"valid_to"`
	LegalRef     string `json:"legal_ref"`
}

func (f fixtureRule) rule() tax.Rule {
	return tax.Rule{
		ID: f.ID, EntityID: f.EntityID, Type: tax.Type(f.Type),
		RateBP: f.RateBP, DPPNum: f.DPPNum, DPPDen: f.DPPDen,
		Inclusive: f.Inclusive, Level: tax.Level(f.Level),
		Rounding: tax.RoundingMode(f.Rounding), RoundingUnit: f.RoundingUnit,
		ValidFrom: f.ValidFrom, ValidTo: f.ValidTo, LegalRef: f.LegalRef,
	}
}

type fixtureSeller struct {
	EntityID string `json:"entity_id"`
	IsPKP    bool   `json:"is_pkp"`
}

type fixtureLine struct {
	Ref    string    `json:"ref"`
	Amount money.IDR `json:"amount"`
	Exempt bool      `json:"exempt"`
}

type fixtureLineExpect struct {
	Ref   string    `json:"ref"`
	DPP   money.IDR `json:"dpp"`
	Tax   money.IDR `json:"tax"`
	Total money.IDR `json:"total"`
}

type fixtureExpect struct {
	DPP                  money.IDR           `json:"dpp"`
	Tax                  money.IDR           `json:"tax"`
	GrandTotal           money.IDR           `json:"grand_total"`
	AccruedWithoutFaktur bool                `json:"accrued_without_faktur"`
	Lines                []fixtureLineExpect `json:"lines"`
}

type fixtureCase struct {
	Name string `json:"name"`
	// Why is the reasoning for the figure, for a reader who is checking it
	// rather than running it.
	Why string `json:"why"`
	// TODO replaces Expect when the figure is not known (TASKS 5.6).
	TODO string `json:"todo"`

	Rule         string        `json:"rule"`
	Seller       fixtureSeller `json:"seller"`
	BusinessDate string        `json:"business_date"`
	FakturIssued bool          `json:"faktur_issued"`
	Lines        []fixtureLine `json:"lines"`

	Expect      *fixtureExpect `json:"expect"`
	ExpectError string         `json:"expect_error"`
}

// sentinels maps a fixture's expect_error to the error it names, so a fixture
// can assert a refusal without importing Go.
var sentinels = map[string]error{
	"ErrEntityMismatch":  tax.ErrEntityMismatch,
	"ErrInvalidCart":     tax.ErrInvalidCart,
	"ErrInvalidRule":     tax.ErrInvalidRule,
	"ErrNoEffectiveRule": tax.ErrNoEffectiveRule,
	"ErrNonPKPCharge":    tax.ErrNonPKPCharge,
	"ErrNonPKPFaktur":    tax.ErrNonPKPFaktur,
	"ErrRuleOverlap":     tax.ErrRuleOverlap,
}

func loadFixtures(t *testing.T) map[string]fixtureFile {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(workedExamplesDir, "ppn_*.json"))
	if err != nil {
		t.Fatalf("glob worked examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no ppn_*.json fixtures in %s: TASKS 5.6 is the whole reason this suite exists", workedExamplesDir)
	}
	sort.Strings(paths)

	out := make(map[string]fixtureFile, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // a fixture path this test built itself
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		// A mistyped key in a fixture would otherwise be silently dropped, and
		// a case whose "expect" became "expects" would pass by asserting
		// nothing at all.
		dec.DisallowUnknownFields()

		var f fixtureFile
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		out[filepath.Base(p)] = f
	}
	return out
}

// TestWorkedExamples runs every fixture case.
func TestWorkedExamples(t *testing.T) {
	t.Parallel()

	files := loadFixtures(t)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		file := files[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, c := range file.Cases {
				t.Run(c.Name, func(t *testing.T) {
					t.Parallel()
					runFixtureCase(t, file, c)
				})
			}
		})
	}
}

func runFixtureCase(t *testing.T, file fixtureFile, c fixtureCase) {
	t.Helper()

	var rules tax.RuleSet
	if c.Rule != "" {
		fr, ok := file.Rules[c.Rule]
		if !ok {
			t.Fatalf("case names rule %q, which the fixture file does not define", c.Rule)
		}
		var err error
		rules, err = tax.NewRuleSet(fr.rule())
		if err != nil {
			t.Fatalf("rule %q is not valid: %v", c.Rule, err)
		}
	}

	cart := tax.Cart{BusinessDate: c.BusinessDate, FakturIssued: c.FakturIssued}
	for _, l := range c.Lines {
		cart.Lines = append(cart.Lines, tax.Line{Ref: l.Ref, Amount: l.Amount, Exempt: l.Exempt})
	}
	seller := tax.Seller{EntityID: c.Seller.EntityID, IsPKP: c.Seller.IsPKP}

	got, err := tax.Calculate(seller, cart, rules)

	switch {
	case c.ExpectError != "":
		want, ok := sentinels[c.ExpectError]
		if !ok {
			t.Fatalf("fixture names error %q, which this suite does not know; add it to sentinels", c.ExpectError)
		}
		if !errors.Is(err, want) {
			t.Fatalf("want %s, got %v", c.ExpectError, err)
		}
		return

	case c.TODO != "":
		// TASKS 5.6: no figure was invented, so nothing is asserted. What the
		// engine produces is printed so the person verifying has something
		// concrete to check against, clearly labelled as unverified.
		if c.Expect != nil {
			t.Fatalf("case carries both a todo and expected figures; it is one or the other")
		}
		if err != nil {
			t.Fatalf("unverified case did not price at all: %v", err)
		}
		t.Logf("UNVERIFIED — not asserted.\n  todo: %s\n  engine says: DPP %s + PPN %s = %s",
			c.TODO, got.DPP, got.Tax, got.GrandTotal)
		for _, l := range got.Lines {
			t.Logf("    line %s: amount %s -> DPP %s + PPN %s", l.Ref, l.Amount, l.DPP, l.Tax)
		}
		return

	case c.Expect == nil:
		// The hole this closes: a case with no expectation and no todo asserts
		// nothing while still reporting as a passing test.
		t.Fatalf("case has neither expected figures, an expected error, nor a todo (TASKS 5.6)")
	}

	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if c.Why == "" {
		t.Errorf("case states a figure with no reasoning; the fixture is read by somebody checking the arithmetic, not only by this test")
	}

	want := c.Expect
	if got.DPP != want.DPP {
		t.Errorf("DPP = %s, want %s", got.DPP, want.DPP)
	}
	if got.Tax != want.Tax {
		t.Errorf("PPN = %s, want %s", got.Tax, want.Tax)
	}
	if got.GrandTotal != want.GrandTotal {
		t.Errorf("grand total = %s, want %s", got.GrandTotal, want.GrandTotal)
	}
	if got.AccruedWithoutFaktur != want.AccruedWithoutFaktur {
		t.Errorf("accrued without faktur = %v, want %v", got.AccruedWithoutFaktur, want.AccruedWithoutFaktur)
	}

	if len(got.Lines) != len(want.Lines) {
		t.Fatalf("got %d lines, want %d", len(got.Lines), len(want.Lines))
	}
	for i, wl := range want.Lines {
		gl := got.Lines[i]
		if gl.Ref != wl.Ref {
			t.Errorf("line %d: ref = %q, want %q", i+1, gl.Ref, wl.Ref)
		}
		if gl.DPP != wl.DPP || gl.Tax != wl.Tax || gl.Total != wl.Total {
			t.Errorf("line %s: DPP %s + PPN %s = %s, want DPP %s + PPN %s = %s",
				wl.Ref, gl.DPP, gl.Tax, gl.Total, wl.DPP, wl.Tax, wl.Total)
		}
	}

	// SPEC §2.2 restated on every worked example, not only on generated carts.
	if got.DPP.Add(got.Tax) != got.GrandTotal {
		t.Errorf("the parts do not sum to the whole: %s + %s != %s", got.DPP, got.Tax, got.GrandTotal)
	}
}

// TestWorkedExamplesReportWhatIsUnverified prints the outstanding questions as
// one list.
//
// Scattered t.Logf lines are easy to lose in a passing run. This is the list to
// take to the konsultan pajak, and it is generated from the fixtures so it
// cannot drift from them.
func TestWorkedExamplesReportWhatIsUnverified(t *testing.T) {
	t.Parallel()

	files := loadFixtures(t)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	var pending []string
	for _, n := range names {
		for _, c := range files[n].Cases {
			if c.TODO != "" {
				pending = append(pending, fmt.Sprintf("%s: %s", n, c.Name))
			}
		}
	}

	if len(pending) == 0 {
		t.Log("every worked example carries a verified figure")
		return
	}
	t.Logf("%d worked example(s) await verification against DJP sources and a konsultan pajak:", len(pending))
	for _, p := range pending {
		t.Logf("  - %s", p)
	}
}
