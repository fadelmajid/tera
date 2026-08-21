package service_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// TestOpeningStockSortsBeforeEverythingBoughtAfterwards is R11.5.
//
// The business is switching systems mid-life, so day one is not day zero. The
// stock already on the shelves has to be carried in, and it has to consume
// first: goods that were physically there before the new system started did not
// arrive after the first delivery it recorded.
func TestOpeningStockSortsBeforeEverythingBoughtAfterwards(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	carried, err := w.opening.CarryInStock(ctx, w.actor, service.OpeningStockInput{
		AsOfDate: "2026-09-30",
		Lines: []service.OpeningStockLine{
			{ProductID: w.gloves, OwnerID: w.budi, Qty: 7, CostTotalIDR: 100_000},
		},
	})
	if err != nil {
		t.Fatalf("carry in: %v", err)
	}
	if carried.TotalQty != 7 || carried.TotalCost != 100_000 {
		t.Fatalf("carried %d units at %s", carried.TotalQty, carried.TotalCost)
	}
	if carried.Layers[0].Source != "OPENING" {
		t.Errorf("source = %q, want OPENING -- carried-in stock is not a purchase that happened here", carried.Layers[0].Source)
	}

	// A delivery on 1 October, after go-live.
	w.buy(ctx, t, 10, 20_000, 0, false)

	layers, err := w.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: w.entityID, ProductID: w.gloves,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("got %d layers, want 2", len(layers))
	}
	if layers[0].Source != "OPENING" {
		t.Errorf("FIFO order starts with %q, want the carried-in stock first", layers[0].Source)
	}

	// Carried-in stock costs what it cost: Rp 100.000 over 7 units, derived
	// per unit and never stored rounded (SPEC §1).
	if layers[0].CostTotalIdr != 100_000 || layers[0].QtyIn != 7 {
		t.Errorf("opening layer = %d over %d units, want 100000 over 7", layers[0].CostTotalIdr, layers[0].QtyIn)
	}
}

// Carried-in stock defaults to holding no faktur. Input PPN on goods bought
// under the old books was either already claimed there or lost; claiming it
// again here would be claiming the same rupiah twice (INV-9, SPEC §2.4).
func TestOpeningStockClaimsNoInputPPNByDefault(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	carried, err := w.opening.CarryInStock(ctx, w.actor, service.OpeningStockInput{
		AsOfDate: "2026-09-30",
		Lines: []service.OpeningStockLine{
			{ProductID: w.gloves, OwnerID: w.budi, Qty: 10, CostTotalIDR: 111_000},
		},
	})
	if err != nil {
		t.Fatalf("carry in: %v", err)
	}
	if carried.Layers[0].FakturReceived != 0 || carried.Layers[0].PpnPaidIdr != 0 {
		t.Errorf("carried-in stock claimed a faktur: %+v", carried.Layers[0])
	}

	// And it does not appear on the input side of the PPN position, because it
	// is not a purchase this company made (SPEC §2.4).
	pos, err := w.purch.InputPPN(ctx, w.entityID, "2026-09-01", "2026-10-31")
	if err != nil {
		t.Fatalf("input ppn: %v", err)
	}
	if !pos.Creditable.IsZero() {
		t.Errorf("carried-in stock contributed %s of input PPN, want zero", pos.Creditable)
	}
}

// INV-8 at go-live. Attribution is required thinking: getting it wrong on day
// one means every margin figure afterwards starts from the wrong place.
func TestOpeningStockCarriesOwnerAttribution(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	carried, err := w.opening.CarryInStock(ctx, w.actor, service.OpeningStockInput{
		AsOfDate: "2026-09-30",
		Lines: []service.OpeningStockLine{
			{ProductID: w.gloves, OwnerID: w.budi, Qty: 5, CostTotalIDR: 50_000},
			{ProductID: w.syringe, OwnerID: w.sari, Qty: 8, CostTotalIDR: 40_000},
			{ProductID: w.gloves, Qty: 3, CostTotalIDR: 30_000}, // company bucket (R2.2)
		},
	})
	if err != nil {
		t.Fatalf("carry in: %v", err)
	}

	byOwner := map[string]int64{}
	for _, l := range carried.Layers {
		key := "company"
		if l.OwnerID != nil {
			key = *l.OwnerID
		}
		byOwner[key] += l.QtyIn
	}
	if byOwner[w.budi] != 5 || byOwner[w.sari] != 8 || byOwner["company"] != 3 {
		t.Errorf("attribution = %+v, want Budi 5, Sari 8, company 3", byOwner)
	}
}

func TestOpeningStockIsAtomic(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	_, err := w.opening.CarryInStock(ctx, w.actor, service.OpeningStockInput{
		AsOfDate: "2026-09-30",
		Lines: []service.OpeningStockLine{
			{ProductID: w.gloves, OwnerID: w.budi, Qty: 5, CostTotalIDR: 50_000},
			{ProductID: store.NewID(), Qty: 5, CostTotalIDR: 50_000}, // no such product
		},
	})
	if err == nil {
		t.Fatal("accepted a line naming a product that does not exist")
	}

	layers, err := w.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: w.entityID, ProductID: w.gloves,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) != 0 {
		t.Errorf("a failed carry-in left %d layers behind", len(layers))
	}
}

// R11.5, R5.5-5.6: what the business owes and is owed on day one.
func TestOpeningHutangAndPiutang(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	customer, err := w.q.CreateCustomer(ctx, gen.CreateCustomerParams{
		ID: store.NewID(), Code: "C1", Name: "Klinik Harapan",
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}

	hutang, err := w.opening.CarryInPayable(ctx, w.actor, service.OpeningDebtInput{
		PartyID: w.supplier, InvoiceNo: "INV-LAMA-1", AmountIDR: 4_500_000,
		IncurredOn: "2026-09-10", DueDate: "2026-10-10",
	})
	if err != nil {
		t.Fatalf("carry in payable: %v", err)
	}
	piutang, err := w.opening.CarryInReceivable(ctx, w.actor, service.OpeningDebtInput{
		PartyID: customer.ID, AmountIDR: 2_750_000,
		IncurredOn: "2026-09-20", DueDate: "2026-10-20",
	})
	if err != nil {
		t.Fatalf("carry in receivable: %v", err)
	}

	// Tagged as what they are. A synthetic purchase would pollute the
	// purchases report and the input PPN position; a synthetic sale would
	// inflate the omzet clock with turnover from the previous books.
	if hutang.Source != "OPENING" || hutang.PurchaseID != nil {
		t.Errorf("hutang = %+v, want source OPENING with no purchase behind it", hutang)
	}
	if piutang.Source != "OPENING" || piutang.SaleID != nil {
		t.Errorf("piutang = %+v, want source OPENING with no sale behind it", piutang)
	}

	// Neither shows up as a purchase.
	purchases, err := w.purch.ListPurchases(ctx, w.entityID, "", "")
	if err != nil {
		t.Fatalf("list purchases: %v", err)
	}
	if len(purchases) != 0 {
		t.Errorf("an opening balance appeared as %d purchases", len(purchases))
	}

	// Partial payment both ways (R5.8), balance derived from the payment rows.
	if _, err := w.opening.PayPayable(ctx, w.actor, hutang.ID, service.PaymentInput{
		AmountIDR: 1_500_000, PaidOn: "2026-10-05", Method: "TRANSFER",
	}); err != nil {
		t.Fatalf("pay hutang: %v", err)
	}
	if _, err := w.opening.PayReceivable(ctx, w.actor, piutang.ID, service.PaymentInput{
		AmountIDR: 750_000, PaidOn: "2026-10-06",
	}); err != nil {
		t.Fatalf("collect piutang: %v", err)
	}

	payables, err := w.opening.ListPayables(ctx, w.entityID, true)
	if err != nil {
		t.Fatalf("list payables: %v", err)
	}
	if len(payables) != 1 || payables[0].OutstandingIdr != 3_000_000 {
		t.Errorf("outstanding hutang = %+v, want 3000000", payables)
	}

	receivables, err := w.opening.ListReceivables(ctx, w.entityID, true)
	if err != nil {
		t.Fatalf("list receivables: %v", err)
	}
	if len(receivables) != 1 || receivables[0].OutstandingIdr != 2_000_000 {
		t.Errorf("outstanding piutang = %+v, want 2000000", receivables)
	}

	// Settling in full drops it off the outstanding list without deleting it.
	if _, err := w.opening.PayReceivable(ctx, w.actor, piutang.ID, service.PaymentInput{
		AmountIDR: 2_000_000, PaidOn: "2026-10-30",
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	outstanding, err := w.opening.ListReceivables(ctx, w.entityID, true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(outstanding) != 0 {
		t.Errorf("a settled piutang is still outstanding: %+v", outstanding)
	}
	all, err := w.opening.ListReceivables(ctx, w.entityID, false)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("a settled piutang vanished from the ledger entirely: %d rows", len(all))
	}
}

func TestOpeningBalanceValidation(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	tests := []struct {
		name string
		in   service.OpeningDebtInput
	}{
		{"no party", service.OpeningDebtInput{AmountIDR: 1_000, IncurredOn: "2026-09-01"}},
		{"zero amount", service.OpeningDebtInput{PartyID: w.supplier, IncurredOn: "2026-09-01"}},
		{"negative amount", service.OpeningDebtInput{PartyID: w.supplier, AmountIDR: -1, IncurredOn: "2026-09-01"}},
		{
			"a date that is not a date",
			service.OpeningDebtInput{PartyID: w.supplier, AmountIDR: 1_000, IncurredOn: "10-09-2026"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := w.opening.CarryInPayable(ctx, w.actor, tc.in); !errors.Is(err, service.ErrValidation) {
				t.Errorf("accepted %s: %v", tc.name, err)
			}
		})
	}

	// And an opening stock line with no product, for the other direction.
	if _, err := w.opening.CarryInStock(ctx, w.actor, service.OpeningStockInput{
		Lines: []service.OpeningStockLine{{Qty: 5, CostTotalIDR: money.IDR(50_000)}},
	}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("accepted an opening stock line with no product: %v", err)
	}
}
