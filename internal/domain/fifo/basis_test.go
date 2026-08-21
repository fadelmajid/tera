package fifo_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// TestSameSupplierPriceFakturVsNoFakturProduceDifferentLayerCosts is the thesis
// of the product (TASKS 1.7 ⭐, SPEC §3.2, acceptance criterion 5, INV-9).
//
// The business buys the same goods, from the same supplier, at the same price,
// twice. One time the supplier hands over a faktur pajak. The other time they
// do not. Nothing else differs — not the product, not the quantity, not the
// rupiah that left the bank account.
//
// Those two purchases must not cost the same, and a sale of each at an
// identical price must not report the same margin. The gap is the PPN: ~11% of
// the net price, on every no-faktur purchase, invisible in the system they use
// today. That invisibility is why they are switching. If this test ever goes
// green while the numbers are equal, the product has no reason to exist.
func TestSameSupplierPriceFakturVsNoFakturProduceDifferentLayerCosts(t *testing.T) {
	t.Parallel()

	// One purchase: 10 boxes at Rp 10.000 net, Rp 11.000 PPN, Rp 111.000 paid.
	// PPN is 12% on a DPP nilai lain of 11/12 — 11% effective (SPEC §2.1).
	const (
		qty       = int64(10)
		grossPaid = money.IDR(111_000)
		ppnPaid   = money.IDR(11_000)
		netPrice  = money.IDR(100_000)
	)

	pkpWithFaktur := mustBasis(t, fifo.Acquisition{
		BuyerIsPKP: true, FakturReceived: true,
		GrossPaid: grossPaid, PPNPaid: ppnPaid,
	})
	pkpNoFaktur := mustBasis(t, fifo.Acquisition{
		BuyerIsPKP: true, FakturReceived: false,
		GrossPaid: grossPaid, PPNPaid: ppnPaid,
	})
	nonPKP := mustBasis(t, fifo.Acquisition{
		BuyerIsPKP: false, FakturReceived: true, // holding a faktur changes nothing
		GrossPaid: grossPaid, PPNPaid: ppnPaid,
	})

	// --- 1. The layer costs differ ------------------------------------------

	if pkpWithFaktur.CostTotal != netPrice {
		t.Errorf("PKP with faktur: layer cost = %s, want %s (the PPN is recoverable, not cost)",
			pkpWithFaktur.CostTotal, netPrice)
	}
	if pkpNoFaktur.CostTotal != grossPaid {
		t.Errorf("PKP without faktur: layer cost = %s, want %s (nothing to credit, so the PPN is cost)",
			pkpNoFaktur.CostTotal, grossPaid)
	}
	if nonPKP.CostTotal != grossPaid {
		t.Errorf("non-PKP: layer cost = %s, want %s (can never credit, so the PPN is always cost)",
			nonPKP.CostTotal, grossPaid)
	}

	// The whole point, stated as one comparison.
	if pkpWithFaktur.CostTotal == pkpNoFaktur.CostTotal {
		t.Fatal("faktur and no-faktur purchases produced the same layer cost; " +
			"this is the bug the product exists to fix")
	}
	if gap := pkpNoFaktur.CostTotal.Sub(pkpWithFaktur.CostTotal); gap != ppnPaid {
		t.Errorf("the gap between the two bases is %s, want exactly the PPN paid (%s)", gap, ppnPaid)
	}

	// --- 2. Only one of them feeds the input side of the PPN position -------
	// SPEC §2.4: input PPN counts purchases WHERE faktur_received. A purchase
	// without a faktur contributes nothing there — its PPN went into the cost
	// layer above instead. The two halves must not double-count.

	if pkpWithFaktur.CreditablePPN != ppnPaid {
		t.Errorf("PKP with faktur: creditable input PPN = %s, want %s", pkpWithFaktur.CreditablePPN, ppnPaid)
	}
	if !pkpNoFaktur.CreditablePPN.IsZero() {
		t.Errorf("PKP without faktur: creditable input PPN = %s, want zero", pkpNoFaktur.CreditablePPN)
	}
	if !nonPKP.CreditablePPN.IsZero() {
		t.Errorf("non-PKP: creditable input PPN = %s, want zero — a non-PKP entity can never credit (SPEC §2.3)",
			nonPKP.CreditablePPN)
	}

	// Every rupiah paid is either cost or credit, never both and never lost.
	for name, b := range map[string]fifo.Basis{
		"PKP with faktur": pkpWithFaktur, "PKP without faktur": pkpNoFaktur, "non-PKP": nonPKP,
	} {
		if got := b.CostTotal.Add(b.CreditablePPN); got != grossPaid {
			t.Errorf("%s: cost %s + credit %s = %s, want the %s actually paid",
				name, b.CostTotal, b.CreditablePPN, got, grossPaid)
		}
	}

	// --- 3. The consequence: identical sales, different margins -------------
	// This is the part the family sees. Sell all 10 boxes for Rp 150.000 and
	// the two purchases report margins Rp 11.000 apart.

	const revenue = money.IDR(150_000)

	sell := func(t *testing.T, b fifo.Basis) money.IDR {
		t.Helper()
		got, err := fifo.Consume(request(ownerBudi, qty), []fifo.Layer{
			{
				ID: "layer-under-test", EntityID: entityPKP, ProductID: gloves,
				OwnerID: ownerBudi, AcquiredAt: day(1),
				QtyIn: qty, CostTotal: b.CostTotal, FakturReceived: b.CreditablePPN.IsPositive(),
			},
		})
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
		return revenue.Sub(got.COGS)
	}

	marginWithFaktur := sell(t, pkpWithFaktur)
	marginNoFaktur := sell(t, pkpNoFaktur)

	if marginWithFaktur != 50_000 {
		t.Errorf("margin with faktur = %s, want %s", marginWithFaktur, money.IDR(50_000))
	}
	if marginNoFaktur != 39_000 {
		t.Errorf("margin without faktur = %s, want %s", marginNoFaktur, money.IDR(39_000))
	}
	if diff := marginWithFaktur.Sub(marginNoFaktur); diff != ppnPaid {
		t.Errorf("the same sale reported margins %s apart, want exactly the PPN (%s)", diff, ppnPaid)
	}
}

// TestCostBasisMatchesTheSpecTable walks SPEC §3.2 row by row, including the
// cases the narrative table leaves implicit.
func TestCostBasisMatchesTheSpecTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		acq           fifo.Acquisition
		wantCost      money.IDR
		wantCreditPPN money.IDR
	}{
		{
			name:          "PKP with faktur: the PPN is recoverable",
			acq:           fifo.Acquisition{BuyerIsPKP: true, FakturReceived: true, GrossPaid: 111_000, PPNPaid: 11_000},
			wantCost:      100_000,
			wantCreditPPN: 11_000,
		},
		{
			name:          "PKP without faktur: the PPN is cost",
			acq:           fifo.Acquisition{BuyerIsPKP: true, FakturReceived: false, GrossPaid: 111_000, PPNPaid: 11_000},
			wantCost:      111_000,
			wantCreditPPN: 0,
		},
		{
			name:          "non-PKP with faktur: recorded, credits nothing",
			acq:           fifo.Acquisition{BuyerIsPKP: false, FakturReceived: true, GrossPaid: 111_000, PPNPaid: 11_000},
			wantCost:      111_000,
			wantCreditPPN: 0,
		},
		{
			name:          "non-PKP without faktur",
			acq:           fifo.Acquisition{BuyerIsPKP: false, FakturReceived: false, GrossPaid: 111_000, PPNPaid: 11_000},
			wantCost:      111_000,
			wantCreditPPN: 0,
		},
		{
			name:          "PKP with faktur but no PPN charged: a non-PKP supplier, nothing to credit",
			acq:           fifo.Acquisition{BuyerIsPKP: true, FakturReceived: true, GrossPaid: 100_000, PPNPaid: 0},
			wantCost:      100_000,
			wantCreditPPN: 0,
		},
		{
			name:          "free goods: a bonus batch costs nothing and is still a layer",
			acq:           fifo.Acquisition{BuyerIsPKP: true, FakturReceived: false, GrossPaid: 0, PPNPaid: 0},
			wantCost:      0,
			wantCreditPPN: 0,
		},
		{
			name:          "the whole payment is PPN: cost falls to zero, not below",
			acq:           fifo.Acquisition{BuyerIsPKP: true, FakturReceived: true, GrossPaid: 11_000, PPNPaid: 11_000},
			wantCost:      0,
			wantCreditPPN: 11_000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := fifo.CostBasis(tc.acq)
			if err != nil {
				t.Fatalf("CostBasis: unexpected error: %v", err)
			}
			if got.CostTotal != tc.wantCost {
				t.Errorf("CostTotal = %s, want %s", got.CostTotal, tc.wantCost)
			}
			if got.CreditablePPN != tc.wantCreditPPN {
				t.Errorf("CreditablePPN = %s, want %s", got.CreditablePPN, tc.wantCreditPPN)
			}
			// PPN paid is recorded whatever became of it, so the decision can
			// be re-examined later (INV-9, INV-10).
			if got.PPNPaid != tc.acq.PPNPaid {
				t.Errorf("PPNPaid = %s, want it carried through unchanged (%s)", got.PPNPaid, tc.acq.PPNPaid)
			}
		})
	}
}

func TestCostBasisRejectsImpossibleAmounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		acq  fifo.Acquisition
	}{
		{"negative payment", fifo.Acquisition{GrossPaid: -1, PPNPaid: 0}},
		{"negative PPN", fifo.Acquisition{GrossPaid: 111_000, PPNPaid: -1}},
		{"PPN exceeds the payment", fifo.Acquisition{GrossPaid: 100_000, PPNPaid: 100_001}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := fifo.CostBasis(tc.acq); !errors.Is(err, fifo.ErrInvalidAcquisition) {
				t.Errorf("got error %v, want ErrInvalidAcquisition", err)
			}
		})
	}
}

func mustBasis(t *testing.T, a fifo.Acquisition) fifo.Basis {
	t.Helper()

	b, err := fifo.CostBasis(a)
	if err != nil {
		t.Fatalf("CostBasis(%+v): %v", a, err)
	}
	return b
}
