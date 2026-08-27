package margin_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/margin"
)

// one returns a minimal well-formed October, ready to be broken one way at a
// time by the table below.
func one() margin.Sale {
	return margin.Sale{
		ID: "sale-1", InvoiceNo: "20261010-0001", BusinessDate: "2026-10-10",
		OccurredAt: acquired(10),
		Lines: []margin.SaleLine{
			saleLine("sl-1", "p-glove", "P-GLOVE", "Sarung Tangan", budi, 4, 60_000, 40_000),
		},
		Draws: []margin.Draw{draw("d-1", "layer-1", "p-glove", budi, 1, 4, 40_000)},
	}
}

// TestComputeRefusesRatherThanGuesses is the disposition this package is
// written with. Every case below is a figure that could be printed and would
// look entirely reasonable. On a report family members settle money on, a
// plausible wrong number is worse than a refusal, because nothing downstream
// would ever question it.
func TestComputeRefusesRatherThanGuesses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		corrupt func(*margin.Input)
		want    error
	}{
		{
			name: "revenue and cost disagree about whose product it was",
			// INV-8. Budi sold it; the layer drawn belongs to Sari. Printed
			// naively this moves Rp 40.000 of cost onto the wrong family member
			// and both totals still look fine.
			corrupt: func(in *margin.Input) {
				in.Sales[0].Draws[0].OwnerID = sari
			},
			want: margin.ErrOwnerMismatch,
		},
		{
			name: "the line's recorded cost no longer matches the layers behind it",
			corrupt: func(in *margin.Input) {
				in.Sales[0].Lines[0].RecordedCOGS = 39_000
			},
			want: margin.ErrCOGSMismatch,
		},
		{
			name: "a cost is recorded against no layers at all",
			corrupt: func(in *margin.Input) {
				in.Sales[0].Draws = nil
			},
			want: margin.ErrCOGSMismatch,
		},
		{
			name: "a layer was drawn for something the sale never sold",
			corrupt: func(in *margin.Input) {
				in.Sales[0].Draws = append(in.Sales[0].Draws,
					draw("d-2", "layer-9", "p-syr", sari, 1, 1, 5_000))
			},
			want: margin.ErrOrphanDraw,
		},
		{
			name: "an owner nobody can name",
			corrupt: func(in *margin.Input) {
				in.OwnerNames = map[margin.OwnerID]string{margin.Company: "Perusahaan"}
			},
			want: margin.ErrUnknownOwner,
		},
		{
			name: "no returns-period rule was chosen",
			corrupt: func(in *margin.Input) {
				in.ReturnPeriod = margin.ReturnPeriodUnset
			},
			want: margin.ErrPolicyUnset,
		},
		{
			name: "a rule string nobody recognises",
			corrupt: func(in *margin.Input) {
				in.ReturnPeriod = margin.ReturnPeriodRule("BULAN_DEPAN")
			},
			want: margin.ErrUnknownReturnRule,
		},
		{
			name: "the window runs backwards",
			corrupt: func(in *margin.Input) {
				in.Period = margin.Period{From: "2026-10-31", To: "2026-10-01"}
			},
			want: margin.ErrInvalidPeriod,
		},
		{
			name: "a draw that puts stock back without saying what it reverses",
			corrupt: func(in *margin.Input) {
				in.Sales[0].Draws[0].QtyOut = -4
			},
			want: margin.ErrInvalidRecord,
		},
		{
			name: "a sale line for no units",
			corrupt: func(in *margin.Input) {
				in.Sales[0].Lines[0].Qty = 0
			},
			want: margin.ErrInvalidRecord,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := margin.Input{
				Period: oct, ReturnPeriod: atReturnDate, OwnerNames: names,
				Sales: []margin.Sale{one()},
			}
			tt.corrupt(&in)

			if _, err := margin.Compute(in); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// The mismatch error has to name the paperwork, not just the fault. Whoever
// reads it is trying to find one sale among a month of them.
func TestOwnerMismatchNamesTheSaleAndTheProduct(t *testing.T) {
	t.Parallel()

	sale := one()
	sale.Draws[0].OwnerID = sari

	_, err := margin.Compute(margin.Input{
		Period: oct, ReturnPeriod: atReturnDate, OwnerNames: names, Sales: []margin.Sale{sale},
	})

	var mismatch *margin.OwnerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v, want an *OwnerMismatchError", err)
	}
	if mismatch.InvoiceNo != "20261010-0001" || mismatch.ProductID != "p-glove" {
		t.Errorf("error names invoice %q product %q", mismatch.InvoiceNo, mismatch.ProductID)
	}
	if mismatch.LineOwner != budi || mismatch.LayerOwner != sari {
		t.Errorf("error reports %s against %s", mismatch.LineOwner, mismatch.LayerOwner)
	}
}

// A voided sale must never reach this package. Its reversals share the sale's
// movement id, so it would net to nothing and still add a phantom row to the
// drill-down -- a sale on screen that did not happen. The guard is the caller's
// filter; this test states the contract the caller is holding up.
func TestAVoidedSaleWouldNotComputeAsNothing(t *testing.T) {
	t.Parallel()

	voided := one()
	voided.Draws = append(voided.Draws,
		reversal("d-void", "d-1", "layer-1", "p-glove", budi, 1, 4, 40_000))

	// Revenue is still recorded on the line, so the arithmetic no longer
	// balances and Compute says so rather than reporting a zero-cost sale.
	if _, err := margin.Compute(margin.Input{
		Period: oct, ReturnPeriod: atReturnDate, OwnerNames: names, Sales: []margin.Sale{voided},
	}); !errors.Is(err, margin.ErrCOGSMismatch) {
		t.Fatalf("got %v, want ErrCOGSMismatch -- voided sales must be filtered out by the caller", err)
	}
}
