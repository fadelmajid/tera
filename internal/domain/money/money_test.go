package money_test

import (
	"encoding/json"
	"errors"
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/fadelmajid/tera/internal/domain/money"
)

func TestArithmetic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  money.IDR
		want money.IDR
	}{
		{"add", money.IDR(100_000).Add(50_000), 150_000},
		{"sub", money.IDR(150_000).Sub(50_000), 100_000},
		{"sub past zero", money.IDR(50_000).Sub(150_000), -100_000},
		{"mul qty", money.IDR(14_285).MulQty(7), 99_995},
		{"mul qty zero", money.IDR(14_285).MulQty(0), 0},
		{"neg", money.IDR(100_000).Neg(), -100_000},
		{"neg of neg", money.IDR(-100_000).Neg(), 100_000},
		{"abs", money.IDR(-100_000).Abs(), 100_000},
		{"sum", money.Sum(1_000, 2_000, 3_000), 6_000},
		{"sum empty", money.Sum(), 0},
		{"sum with reversal", money.Sum(10_000, -3_000), 7_000},
		{"min", money.Min(5, 9), 5},
		{"max", money.Max(5, 9), 9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("got %d, want %d", tc.got, tc.want)
			}
		})
	}
}

// Rounding is half away from zero, which is what tax_rule.rounding_mode
// specifies (SPEC §2.1).
func TestFromDecimalRoundsHalfAwayFromZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want money.IDR
	}{
		{"1000", 1000},
		{"1000.4", 1000},
		{"1000.5", 1001},
		{"1000.6", 1001},
		{"-1000.4", -1000},
		{"-1000.5", -1001},
		{"0.5", 1},
		{"0.4", 0},
		{"91666.6666666", 91667},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := money.FromDecimal(decimal.RequireFromString(tc.in))
			if got != tc.want {
				t.Errorf("FromDecimal(%s) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// The DPP nilai lain is an exact fraction. No 0.916666… appears anywhere in
// this codebase (SPEC §2.1).
func TestMulRatioIsExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     money.IDR
		num, den int64
		want     money.IDR
	}{
		{"dpp 11/12 of 100.000", 100_000, 11, 12, 91_667},
		{"dpp 11/12 of 150.000", 150_000, 11, 12, 137_500},
		{"dpp 11/12 of 1", 1, 11, 12, 1},
		{"whole fraction", 100_000, 1, 1, 100_000},
		{"halving rounds up", 12_345, 1, 2, 6_173},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.base.MulRatio(tc.num, tc.den); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// PPN is statutory 12% on a DPP of 11/12, which is effectively 11%. Both routes
// to the figure must agree — the real engine lives in domain/tax, this is the
// arithmetic anchor underneath it (SPEC §2.1).
func TestEffectivePPNIsElevenPercent(t *testing.T) {
	t.Parallel()

	const base money.IDR = 100_000

	dpp := base.MulRatio(11, 12) // 91.667
	viaDPP := dpp.Percent(1200)  // 12% of the DPP
	direct := base.Percent(1100) // 11% of the base

	if viaDPP != direct {
		t.Errorf("12%% of DPP 11/12 = %d, but 11%% of base = %d — these must agree", viaDPP, direct)
	}
	if direct != 11_000 {
		t.Errorf("effective PPN on Rp 100.000 = %d, want 11.000", direct)
	}
}

func TestPercentUsesBasisPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base money.IDR
		bp   int64
		want money.IDR
	}{
		{"12 percent", 100_000, 1200, 12_000},
		{"11 percent", 100_000, 1100, 11_000},
		{"zero percent", 100_000, 0, 0},
		{"rounds half up", 1_005, 50, 5}, // 5,025 → 5
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.base.Percent(tc.bp); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAllocate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		total   money.IDR
		weights []int64
		want    []money.IDR
	}{
		{"exact split", 90_000, []int64{1, 1, 1}, []money.IDR{30_000, 30_000, 30_000}},
		{"remainder to the front", 100, []int64{1, 1, 1}, []money.IDR{34, 33, 33}},
		{"single rupiah", 1, []int64{1, 1}, []money.IDR{1, 0}},
		{"weighted", 100_000, []int64{30, 70}, []money.IDR{30_000, 70_000}},
		{"zero total", 0, []int64{1, 2, 3}, []money.IDR{0, 0, 0}},
		{"one bucket", 12_345, []int64{5}, []money.IDR{12_345}},
		{"zero weight gets nothing", 100, []int64{0, 1}, []money.IDR{0, 100}},
		// A refund allocates the same way a sale does, just signed (INV-2).
		{"negative total", -100, []int64{1, 1, 1}, []money.IDR{-34, -33, -33}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := money.Allocate(tc.total, tc.weights)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d parts, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("part %d = %d, want %d (full: %v)", i, got[i], tc.want[i], got)
				}
			}
			if sum := money.Sum(got...); sum != tc.total {
				t.Errorf("parts sum to %d, want exactly %d — rupiah was lost", sum, tc.total)
			}
		})
	}
}

func TestAllocateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		weights []int64
		want    error
	}{
		{"no weights", nil, money.ErrNoWeight},
		{"empty weights", []int64{}, money.ErrNoWeight},
		{"all zero", []int64{0, 0}, money.ErrNoWeight},
		{"negative weight", []int64{1, -1}, money.ErrNegativeWeight},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := money.Allocate(1000, tc.weights); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// The example from SPEC §1: a FIFO layer of 7 units at Rp 100.000 is
// Rp 14.285,71 each. Store the total and derive; the last draws absorb the
// remainder so the layer consumes to exactly Rp 100.000 with nothing lost.
func TestSevenUnitLayerConsumesToExactlyOneHundredThousand(t *testing.T) {
	t.Parallel()

	const layerTotal money.IDR = 100_000
	const units = 7

	weights := make([]int64, units)
	for i := range weights {
		weights[i] = 1
	}

	parts, err := money.Allocate(layerTotal, weights)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sum := money.Sum(parts...); sum != layerTotal {
		t.Fatalf("7 units of a Rp 100.000 layer consumed to %s, want exactly %s", sum, layerTotal)
	}

	// Naively rounding a unit cost and multiplying loses rupiah on every draw.
	naive := money.IDR(14_286).MulQty(units)
	if naive == layerTotal {
		t.Fatal("expected the rounded-unit-cost approach to be wrong; the test has lost its point")
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    money.IDR
		want  string
		plain string
	}{
		{0, "Rp 0", "0"},
		{1, "Rp 1", "1"},
		{999, "Rp 999", "999"},
		{1_000, "Rp 1.000", "1.000"},
		{1_234_567, "Rp 1.234.567", "1.234.567"},
		{4_800_000_000, "Rp 4.800.000.000", "4.800.000.000"},
		{-1_234_567, "-Rp 1.234.567", "-1.234.567"},
		{-500, "-Rp 500", "-500"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			if got := tc.in.Plain(); got != tc.plain {
				t.Errorf("Plain() = %q, want %q", got, tc.plain)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    money.IDR
		wantErr error
	}{
		{in: "Rp 1.234.567", want: 1_234_567},
		{in: "1.234.567", want: 1_234_567},
		{in: "1234567", want: 1_234_567},
		{in: "  Rp   1.000  ", want: 1_000},
		{in: "rp 1.000", want: 1_000},
		{in: "-Rp 1.000", want: -1_000},
		{in: "Rp -1.000", want: -1_000},
		{in: "0", want: 0},
		// A comma is Indonesia's decimal separator: fractional, so refused.
		{in: "1.000,50", wantErr: money.ErrFractional},
		{in: "", wantErr: money.ErrMalformed},
		{in: "Rp", wantErr: money.ErrMalformed},
		{in: "abc", wantErr: money.ErrMalformed},
		{in: "12a3", wantErr: money.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := money.Parse(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse(%q) error = %v, want %v", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	t.Parallel()

	vals := []money.IDR{0, 1, 999, 1_000, 1_234_567, 4_800_000_000, -1, -1_234_567}
	for _, v := range vals {
		got, err := money.Parse(v.String())
		if err != nil {
			t.Fatalf("Parse(%q): %v", v.String(), err)
		}
		if got != v {
			t.Errorf("round trip of %d via %q gave %d", v, v.String(), got)
		}
	}
}

// Money crosses the wire as a JSON integer, never a float and never a
// pre-formatted string (INV-1, SPEC §1).
func TestJSONIsAnInteger(t *testing.T) {
	t.Parallel()

	type payload struct {
		Price money.IDR `json:"price"`
	}

	b, err := json.Marshal(payload{Price: 1_234_567})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"price":1234567}` {
		t.Errorf("got %s, want {\"price\":1234567}", b)
	}
}

func TestJSONRefusesFractionalValues(t *testing.T) {
	t.Parallel()

	type payload struct {
		Price money.IDR `json:"price"`
	}

	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{"integer", `{"price":1000}`, nil},
		{"quoted integer", `{"price":"1000"}`, nil},
		{"fractional", `{"price":1000.5}`, money.ErrFractional},
		{"fractional but whole", `{"price":1000.0}`, money.ErrFractional},
		{"scientific", `{"price":1e3}`, money.ErrFractional},
		{"not a number", `{"price":"abc"}`, money.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var p payload
			err := json.Unmarshal([]byte(tc.body), &p)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if p.Price != 1000 {
					t.Errorf("got %d, want 1000", p.Price)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v, want %v — a silent truncation here is a settlement that does not balance", err, tc.wantErr)
			}
		})
	}
}

// Whatever the split, the parts sum to the whole. This is the invariant the
// tax engine's zero-drift property test (TASKS 5.7) will lean on.
func TestAllocateNeverLosesRupiah(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(20260820)) //nolint:gosec // deterministic fixture, not security

	for i := 0; i < 5_000; i++ {
		total := money.IDR(rng.Int63n(2_000_000_001) - 1_000_000_000)

		n := 1 + rng.Intn(12)
		weights := make([]int64, n)
		var sumW int64
		for j := range weights {
			weights[j] = rng.Int63n(1_000)
			sumW += weights[j]
		}
		if sumW == 0 {
			weights[0] = 1
		}

		parts, err := money.Allocate(total, weights)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if sum := money.Sum(parts...); sum != total {
			t.Fatalf("iteration %d: parts %v sum to %d, want %d (weights %v)", i, parts, sum, total, weights)
		}
	}
}

// TestAllocateSurvivesThresholdScaleFigures is a regression, found by a sale
// sized at the PKP threshold.
//
// Allocate multiplied the total by each weight in int64. At Rp 4,8 miliar
// against a weight of the same size that product is about 2,4e19, against an
// int64 ceiling of 9,2e18 — it wrapped negative, every remainder came out below
// the sentinel the distribution loop starts from, and the call panicked.
//
// That is not an exotic figure in this system: it is the threshold the whole
// omzet clock is built around, and an invoice discount or an invoice-level PPN
// on a sale of that size goes straight through here.
func TestAllocateSurvivesThresholdScaleFigures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		total   money.IDR
		weights []int64
	}{
		{
			// One line at the threshold itself.
			name:  "a single line at the PKP threshold",
			total: 4_324_324_324, weights: []int64{4_800_000_000},
		},
		{
			name:  "two lines either side of it",
			total: 8_648_648_648, weights: []int64{4_800_000_000, 4_800_000_000},
		},
		{
			// Deliberately awkward: the shares do not divide evenly, so the
			// remainder loop has real work to do at a scale that used to
			// overflow.
			name:  "three uneven lines at scale",
			total: 4_324_324_324, weights: []int64{1_600_000_001, 1_600_000_001, 1_599_999_998},
		},
		{
			name:  "a refund of the same size",
			total: -4_324_324_324, weights: []int64{4_800_000_000, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := money.Allocate(tt.total, tt.weights)
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			if len(got) != len(tt.weights) {
				t.Fatalf("got %d parts, want %d", len(got), len(tt.weights))
			}

			// The property the whole function exists for: the parts sum to the
			// whole, exactly, with nothing lost and nothing invented.
			var sum money.IDR
			for _, p := range got {
				sum = sum.Add(p)
			}
			if sum != tt.total {
				t.Errorf("parts sum to %s, want %s", sum, tt.total)
			}

			// And each part keeps the sign of the total, so a refund does not
			// come back with a positive slice in it.
			for i, p := range got {
				if tt.total.IsPositive() && p.IsNegative() {
					t.Errorf("part %d is %s against a positive total", i, p)
				}
				if tt.total.IsNegative() && p.IsPositive() {
					t.Errorf("part %d is %s against a negative total", i, p)
				}
			}
		})
	}
}

// TestAllocateIsProportionalAtScale: the fix must not have changed the answer,
// only its range.
func TestAllocateIsProportionalAtScale(t *testing.T) {
	t.Parallel()

	// Rp 4.800.000.000 split three ways in the ratio 1:1:2.
	got, err := money.Allocate(4_800_000_000, []int64{1_000_000_000, 1_000_000_000, 2_000_000_000})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	want := []money.IDR{1_200_000_000, 1_200_000_000, 2_400_000_000}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %s, want %s", i, got[i], want[i])
		}
	}
}
