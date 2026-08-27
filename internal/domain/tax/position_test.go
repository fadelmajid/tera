package tax_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/tax"
)

func TestParseMasa(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    tax.Masa
		wantErr bool
	}{
		{in: "2026-08", want: tax.Masa{Year: 2026, Month: time.August}},
		{in: "2026-01", want: tax.Masa{Year: 2026, Month: time.January}},
		{in: "2026-12", want: tax.Masa{Year: 2026, Month: time.December}},
		{in: "2026-13", wantErr: true},
		{in: "2026-8", wantErr: true},
		{in: "Agustus 2026", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := tax.ParseMasa(tt.in)
			if tt.wantErr {
				if !errors.Is(err, tax.ErrInvalidMasa) {
					t.Fatalf("want ErrInvalidMasa, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMasa: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMasaRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		masa     tax.Masa
		wantFrom string
		wantTo   string
	}{
		{tax.Masa{Year: 2026, Month: time.August}, "2026-08-01", "2026-08-31"},
		{tax.Masa{Year: 2026, Month: time.February}, "2026-02-01", "2026-02-28"},
		// A leap year, which is the month a hand-written range gets wrong.
		{tax.Masa{Year: 2028, Month: time.February}, "2028-02-01", "2028-02-29"},
		{tax.Masa{Year: 2026, Month: time.December}, "2026-12-01", "2026-12-31"},
	}

	for _, tt := range tests {
		t.Run(tt.masa.String(), func(t *testing.T) {
			t.Parallel()

			from, to := tt.masa.Range()
			if from != tt.wantFrom || to != tt.wantTo {
				t.Fatalf("range is %s..%s, want %s..%s", from, to, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestMasaNextRollsTheYear(t *testing.T) {
	t.Parallel()

	got := tax.Masa{Year: 2026, Month: time.December}.Next()
	if want := (tax.Masa{Year: 2027, Month: time.January}); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestPositionArithmetic is SPEC §2.4.
func TestPositionArithmetic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pos         tax.Position
		wantOutput  money.IDR
		wantInput   money.IDR
		wantPayable money.IDR
		wantOverpay bool
	}{
		{
			name: "an ordinary month",
			pos: tax.Position{
				OutputWithFaktur: 4_400_000, InputCreditable: 2_200_000,
			},
			wantOutput: 4_400_000, wantInput: 2_200_000, wantPayable: 2_200_000,
		},
		{
			// TASKS 5.4. The walk-in sales are owed identically and are the
			// larger half of this month's liability. Netting them into one
			// total would hide the figure the owner is about to be surprised by.
			name: "most of the month was walk-in trade with no faktur",
			pos: tax.Position{
				OutputWithFaktur: 1_100_000, OutputWithoutFaktur: 3_300_000,
				InputCreditable: 2_200_000,
			},
			wantOutput: 4_400_000, wantInput: 2_200_000, wantPayable: 2_200_000,
		},
		{
			// SPEC §2.4 and INV-9: PPN paid on a purchase with no faktur is not
			// creditable. It went into the cost of the goods instead
			// (SPEC §3.2), so crediting it here would claim the same rupiah
			// twice — once against output PPN and once as a lower COGS in the
			// margin the family settles on.
			name: "half the purchases arrived without a faktur",
			pos: tax.Position{
				OutputWithFaktur:   4_400_000,
				InputCreditable:    1_100_000,
				InputNonCreditable: 1_100_000,
			},
			wantOutput: 4_400_000, wantInput: 1_100_000, wantPayable: 3_300_000,
		},
		{
			name: "returns on both sides",
			pos: tax.Position{
				OutputWithFaktur: 4_400_000, OutputReversed: 400_000,
				InputCreditable: 2_200_000, InputReversed: 200_000,
			},
			wantOutput: 4_000_000, wantInput: 2_000_000, wantPayable: 2_000_000,
		},
		{
			// A month that stocked up. The excess is carried to the next masa
			// or claimed at the end of the book year (UU PPN Pasal 9 ayat (4)),
			// so clamping it to zero would forget money the business is owed.
			name: "lebih bayar",
			pos: tax.Position{
				OutputWithFaktur: 1_100_000, InputCreditable: 5_500_000,
			},
			wantOutput: 1_100_000, wantInput: 5_500_000, wantPayable: -4_400_000,
			wantOverpay: true,
		},
		{
			name:       "a month with no trade at all",
			pos:        tax.Position{},
			wantOutput: 0, wantInput: 0, wantPayable: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.pos.Output(); got != tt.wantOutput {
				t.Errorf("output = %s, want %s", got, tt.wantOutput)
			}
			if got := tt.pos.Input(); got != tt.wantInput {
				t.Errorf("input = %s, want %s", got, tt.wantInput)
			}
			if got := tt.pos.Payable(); got != tt.wantPayable {
				t.Errorf("payable = %s, want %s", got, tt.wantPayable)
			}
			if got := tt.pos.IsOverpaid(); got != tt.wantOverpay {
				t.Errorf("overpaid = %v, want %v", got, tt.wantOverpay)
			}
		})
	}
}

// TestNonCreditableInputNeverReachesThePayable states the filter of SPEC §2.4
// as a difference rather than a value: the same month, twice, differing only in
// whether the supplier handed over the faktur.
func TestNonCreditableInputNeverReachesThePayable(t *testing.T) {
	t.Parallel()

	const ppn = money.IDR(1_100_000)

	withFaktur := tax.Position{OutputWithFaktur: 4_400_000, InputCreditable: ppn}
	without := tax.Position{OutputWithFaktur: 4_400_000, InputNonCreditable: ppn}

	if withFaktur.Payable() != 3_300_000 {
		t.Fatalf("with a faktur, payable is %s", withFaktur.Payable())
	}
	if without.Payable() != 4_400_000 {
		t.Fatalf("without a faktur, payable is %s", without.Payable())
	}
	if diff := without.Payable().Sub(withFaktur.Payable()); diff != ppn {
		t.Fatalf("the faktur is worth %s of the position, want %s", diff, ppn)
	}
}
