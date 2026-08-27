package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// till opens a cash session so sales can be rung.
func (w world) till(ctx context.Context, t *testing.T) gen.CashSession {
	t.Helper()

	s, err := w.sales.OpenSession(ctx, w.actor, service.OpenSessionInput{
		OpeningFloatIDR: 500_000, BusinessDate: "2026-10-15",
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	return s
}

func (w world) ring(ctx context.Context, t *testing.T, lines ...service.SaleLineInput) service.SaleResult {
	t.Helper()

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	got, err := w.sales.Ring(ctx, actor, service.SaleInput{SaleDate: "2026-10-15", Lines: lines})
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	return got
}

// TestSaleWritesEverythingOrNothing is TASKS 2.2's hard requirement.
//
// A sale writes its header, its lines, and one stock consumption per layer
// drawn. A partial commit corrupts stock and margin simultaneously: goods leave
// the shelf with no record of what they cost, or cost is recorded against goods
// that never moved. Either way the report the family settles money on is
// quietly wrong, and nothing in the system would say so.
func TestSaleWritesEverythingOrNothing(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false) // Budi: 10 gloves at Rp 10.000
	w.till(ctx, t)

	// A cart whose second line asks for stock that is not there. The first line
	// is perfectly good, which is exactly the case a partial commit would ruin.
	_, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate: "2026-10-15",
		Lines: []service.SaleLineInput{
			{ProductID: w.gloves, Qty: 2},
			{ProductID: w.syringe, Qty: 5}, // Sari's product, no stock at all
		},
	})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}

	sales, err := w.sales.ListSales(ctx, w.entityID, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sales) != 0 {
		t.Errorf("a failed sale left %d rows behind", len(sales))
	}

	// And the stock the good line would have taken is untouched.
	onHand, err := w.opname.CountSheet(ctx, w.entityID)
	if err != nil {
		t.Fatalf("count sheet: %v", err)
	}
	for _, row := range onHand {
		if row.ProductID == w.gloves && row.QtyOnHand != 10 {
			t.Errorf("gloves on hand = %d, want 10 -- a failed sale consumed stock", row.QtyOnHand)
		}
	}

	// The successful case writes all three kinds of row together.
	got := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 4})
	if len(got.Lines) != 1 || got.Consumptions != 1 {
		t.Errorf("got %d lines and %d consumptions, want 1 and 1", len(got.Lines), got.Consumptions)
	}
	if got.COGS != 40_000 {
		t.Errorf("COGS = %s, want %s", got.COGS, money.IDR(40_000))
	}
	if got.Sale.CogsIdr != int64(got.COGS) {
		t.Errorf("the sale header says COGS %d but the draws total %s", got.Sale.CogsIdr, got.COGS)
	}

	// The consumption rows are the authority, and they agree with the header.
	drill, err := w.sales.SaleDrillDown(ctx, w.entityID, got.Sale.ID)
	if err != nil {
		t.Fatalf("drill down: %v", err)
	}
	var drawn int64
	for _, c := range drill {
		drawn += c.CostIdr
	}
	if drawn != got.Sale.CogsIdr {
		t.Errorf("drill-down totals %d against a header of %d", drawn, got.Sale.CogsIdr)
	}
}

// INV-8 at the till, and acceptance criterion 6. A sale of Budi's product never
// draws Sari's stock, however much of it is on the same shelf.
func TestSaleNeverDrawsAnotherOwnersStock(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 3, 10_000, 0, false) // Budi: 3

	// Sari has a hundred of the same product.
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.sari,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 100, CostTotalIdr: 1_000_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sari layer: %v", err)
	}
	w.till(ctx, t)

	_, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate: "2026-10-15",
		Lines:    []service.SaleLineInput{{ProductID: w.gloves, Qty: 10}},
	})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("got %v, want a refusal", err)
	}
	// The message has to explain itself, or a cashier looking at a full shelf
	// reads it as a bug and starts keeping a notebook.
	if msg := err.Error(); !strings.Contains(msg, "pemilik lain") {
		t.Errorf("error does not mention that the remaining stock belongs to someone else: %q", msg)
	}

	// Sari's stock is exactly where it was.
	after, err := w.opname.CountSheet(ctx, w.entityID)
	if err != nil {
		t.Fatalf("count sheet: %v", err)
	}
	for _, row := range after {
		if row.OwnerID != nil && *row.OwnerID == w.sari && row.QtyOnHand != 100 {
			t.Errorf("Sari holds %d, want 100", row.QtyOnHand)
		}
	}
}

// The layer's cost basis reaches the till. Two purchases at the same supplier
// price, one with a faktur and one without, sell at the same price and report
// different margins -- the whole thesis, now visible where money is taken.
func TestFakturCostBasisReachesTheMarginAtTheTill(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.till(ctx, t)

	// Oldest first: the faktur batch sells before the no-faktur batch.
	withFaktur := w.buy(ctx, t, 5, 10_000, 11_000, true) // cost basis 50.000
	_ = withFaktur
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.budi,
		AcquiredAt: fixedNow.Unix() + 1000, BusinessDate: "2026-10-02", Source: "PURCHASE",
		QtyIn: 5, CostTotalIdr: 55_500, FakturReceived: 0, PpnPaidIdr: 5_500,
		CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("second layer: %v", err)
	}

	price := money.IDR(20_000)
	first := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 5, UnitPriceIDR: &price})
	second := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 5, UnitPriceIDR: &price})

	if first.COGS != 50_000 {
		t.Errorf("first sale COGS = %s, want %s (the faktur batch, net of creditable PPN)", first.COGS, money.IDR(50_000))
	}
	if second.COGS != 55_500 {
		t.Errorf("second sale COGS = %s, want %s (the no-faktur batch, PPN is cost)", second.COGS, money.IDR(55_500))
	}
	if first.Margin == second.Margin {
		t.Fatal("identical sales reported identical margins; the faktur made no difference at the till")
	}
	if diff := first.Margin.Sub(second.Margin); diff != 5_500 {
		t.Errorf("margins differ by %s, want exactly the PPN that was not creditable (%s)", diff, money.IDR(5_500))
	}
}

// SPEC §2.2's property, at the discount layer: the lines must sum to the total
// exactly, with no rupiah lost to integer division.
func TestInvoiceDiscountAllocatesWithoutLosingRupiah(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 100, 1_000, 0, false)
	w.till(ctx, t)

	// Three lines and a discount that does not divide evenly by any of them.
	price := money.IDR(1_000)
	got, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate:           "2026-10-15",
		InvoiceDiscountIDR: 1_000,
		Lines: []service.SaleLineInput{
			{ProductID: w.gloves, Qty: 3, UnitPriceIDR: &price},
			{ProductID: w.gloves, Qty: 3, UnitPriceIDR: &price},
			{ProductID: w.gloves, Qty: 3, UnitPriceIDR: &price},
		},
	})
	if err != nil {
		t.Fatalf("ring: %v", err)
	}

	var netSum, allocSum money.IDR
	for _, l := range got.Lines {
		netSum = netSum.Add(money.IDR(l.NetIdr))
		allocSum = allocSum.Add(money.IDR(l.AllocDiscountIdr))
	}
	if allocSum != 1_000 {
		t.Errorf("the allocated discount totals %s, want exactly the %s given", allocSum, money.IDR(1_000))
	}
	if netSum != money.IDR(got.Sale.TotalIdr) {
		t.Errorf("lines sum to %s but the sale total is %s -- a rupiah was lost in the split",
			netSum, money.IDR(got.Sale.TotalIdr))
	}
	if got.Sale.TotalIdr != 8_000 {
		t.Errorf("total = %d, want 8000 (9 x 1.000 less a 1.000 discount)", got.Sale.TotalIdr)
	}
}

// R9.8: a sale belongs to a till, so the day can be reconciled.
func TestSaleRequiresAnOpenTill(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)

	if _, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		Lines: []service.SaleLineInput{{ProductID: w.gloves, Qty: 1}},
	}); !errors.Is(err, service.ErrNoOpenSession) {
		t.Errorf("rang a sale with no till open: %v", err)
	}
}

// TestRepeatedProductLinesDrawTheShelfOnce guards a bug that would corrupt
// exactly the figures Phase 3 reports.
//
// A cashier scanning the same item twice, or ringing it at two different
// prices, produces two lines of one product. The draws for every line are
// computed before any of them is written, so each line reads the same stored
// balance -- and without a running total, both would take the same units. The
// till would sell six of a stock of five and cost them against a layer that
// never held them, breaking the append-only derivation remaining = qty_in - Σ
// qty_out (INV-7) and inflating COGS for whoever owns the product.
func TestRepeatedProductLinesDrawTheShelfOnce(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, false)
	w.buy(ctx, t, 5, 10_000, 0, false) // Budi: 5 boxes at Rp 10.000
	w.till(ctx, t)

	// Six units asked for across two lines, five on the shelf. Short is short,
	// however the cart is arranged.
	_, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate: "2026-10-15",
		Lines: []service.SaleLineInput{
			{ProductID: w.gloves, Qty: 3},
			{ProductID: w.gloves, Qty: 3},
		},
	})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("got %v, want ErrInsufficientStock -- the second line drew stock the first had taken", err)
	}

	// And a cart that does fit costs each line its own slice of the layer.
	got := w.ring(ctx, t,
		service.SaleLineInput{ProductID: w.gloves, Qty: 3},
		service.SaleLineInput{ProductID: w.gloves, Qty: 2},
	)
	if got.COGS != 50_000 {
		t.Errorf("COGS = %s, want %s -- the whole layer, drawn once", got.COGS, money.IDR(50_000))
	}

	onHand, err := w.opname.CountSheet(ctx, w.entityID)
	if err != nil {
		t.Fatalf("count sheet: %v", err)
	}
	for _, row := range onHand {
		if row.ProductID == w.gloves && row.QtyOnHand != 0 {
			t.Errorf("gloves on hand = %d, want 0", row.QtyOnHand)
		}
	}
}
