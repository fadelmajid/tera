package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// TestVoidGivesBackExactlyWhatTheSaleTook is TASKS 2.10 and R12.3.
//
// A void says the sale did not happen. The stock goes back to the exact layers
// it came from at the exact cost taken, so nothing about the day's margin
// remembers it.
func TestVoidGivesBackExactlyWhatTheSaleTook(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	// Seven units at Rp 100.000: the layer that does not divide evenly.
	w.buy(ctx, t, 7, 0, 0, false)
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: layerIDFor(t), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.budi,
		AcquiredAt: fixedNow.Unix() + 100, BusinessDate: "2026-10-02", Source: "PURCHASE",
		QtyIn: 7, CostTotalIdr: 100_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("layer: %v", err)
	}
	w.till(ctx, t)

	before := onHandFor(ctx, t, w, w.budi)

	price := money.IDR(30_000)
	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 3, UnitPriceIDR: &price})

	if _, err := w.sales.Void(ctx, w.actor, sale.Sale.ID, "Salah input, barang belum keluar"); err != nil {
		t.Fatalf("void: %v", err)
	}

	// Stock is exactly back.
	if after := onHandFor(ctx, t, w, w.budi); after != before {
		t.Errorf("stock is %d after the void, was %d before the sale", after, before)
	}

	// And the cost reversed to the rupiah: the consumptions for that sale now
	// net to zero rather than approximately zero.
	drill, err := w.sales.SaleDrillDown(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("drill: %v", err)
	}
	var netQty, netCost int64
	for _, c := range drill {
		netQty += c.QtyOut
		netCost += c.CostIdr
	}
	if netQty != 0 || netCost != 0 {
		t.Errorf("a voided sale still nets %d units and %d rupiah of cost", netQty, netCost)
	}

	voided, _, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if voided.Status != "VOID" || voided.VoidReason == nil {
		t.Errorf("sale = %+v, want VOID with a recorded reason", voided)
	}
	// The row is still there. A sale that happened cannot stop having happened
	// (INV-2); it is marked, not deleted.
	if voided.CogsIdr == 0 && len(drill) == 0 {
		t.Error("the void deleted the trail instead of compensating it")
	}
}

// R12.3 and the rule recorded in migration 009: the void window closes when the
// till is counted. After that the goods are with the customer and getting them
// back is a return, not a pretence that nothing happened.
func TestVoidWindowClosesWithTheTill(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)
	session := w.till(ctx, t)

	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 2})

	totals, err := w.sales.Totals(ctx, w.entityID, session.ID)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if _, err := w.sales.CloseSession(ctx, w.actor, session.ID, totals.ExpectedCash, "tutup harian"); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := w.sales.Void(ctx, w.actor, sale.Sale.ID, "terlambat"); !errors.Is(err, service.ErrVoidWindowClosed) {
		t.Errorf("voided after the till was counted: %v", err)
	}
	// A return still works, because that is the honest correction.
	_, lines, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := w.sales.CreateReturn(ctx, w.actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID, ReturnDate: "2026-10-16", Reason: "Barang dikembalikan pelanggan",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	}); err != nil {
		t.Errorf("a return after the till closed was refused: %v", err)
	}
}

// TestReturnRestoresToTheOriginalLayerAndReversesMarginExactly is TASKS 2.9,
// R12.1, D-010.
//
// The goods go back to the layer they came from, not to a new layer at today's
// cost. A new layer would reverse revenue in full while reversing cost at a
// different figure, quietly inventing margin in the report the family settles
// money on.
func TestReturnRestoresToTheOriginalLayerAndReversesMarginExactly(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	// Seven at Rp 100.000 -- Rp 14.285,714… each, so nothing divides evenly.
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: layerIDFor(t), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.budi,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 7, CostTotalIdr: 100_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("layer: %v", err)
	}
	w.till(ctx, t)

	price := money.IDR(30_000)
	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 7, UnitPriceIDR: &price})
	if sale.COGS != 100_000 {
		t.Fatalf("COGS = %s, want the whole layer", sale.COGS)
	}
	soldMargin := sale.Margin

	_, lines, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Return it in pieces: three, then four.
	var totalRefund, totalPPN, totalCOGS money.IDR
	for _, qty := range []int64{3, 4} {
		got, err := w.sales.CreateReturn(ctx, w.actor, service.SaleReturnInput{
			SaleID: sale.Sale.ID, ReturnDate: "2026-11-02", Reason: "Barang tidak sesuai",
			Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: qty}},
		})
		if err != nil {
			t.Fatalf("return %d: %v", qty, err)
		}
		totalRefund = totalRefund.Add(got.Refund)
		totalPPN = totalPPN.Add(got.PPNReversed)
		totalCOGS = totalCOGS.Add(got.COGSReversed)

		// SPEC §4.4 is undecided, so both dates are on the record either way.
		if got.Return.BusinessDate != "2026-11-02" {
			t.Errorf("return business date = %q, want the day the goods came back", got.Return.BusinessDate)
		}
		if got.Return.SaleBusinessDate != "2026-10-15" {
			t.Errorf("sale business date = %q, want the original sale's day carried onto the return",
				got.Return.SaleBusinessDate)
		}
	}

	// Revenue and cost both reverse exactly. Not approximately -- the margin
	// nets to zero, which is the whole point of returning to the same layer.
	//
	// The customer gets back every rupiah they handed over, PPN included: the
	// Rp 210.000 on the till, split three-then-four, with nothing lost to
	// rounding on either piece.
	if totalRefund != 210_000 {
		t.Errorf("refunded %s in total, want the full %s taken", totalRefund, money.IDR(210_000))
	}
	if totalCOGS != 100_000 {
		t.Errorf("reversed %s of cost, want exactly the layer's %s", totalCOGS, money.IDR(100_000))
	}
	// Rp 210.000 inclusive is Rp 189.189 of revenue and Rp 20.811 of PPN, and
	// the PPN goes back to the state's column rather than the owner's
	// (SPEC §2.4).
	if totalPPN != 20_811 {
		t.Errorf("reversed %s of output PPN, want %s", totalPPN, money.IDR(20_811))
	}
	// Revenue out, cost back: a fully returned sale leaves no margin behind.
	// Revenue is the refund less the PPN inside it, which is what the margin
	// report reverses -- the tax was never margin to begin with.
	revenueBack := totalRefund.Sub(totalPPN)
	if net := soldMargin.Sub(revenueBack).Add(totalCOGS); !net.IsZero() {
		t.Errorf("margin nets to %s after a full return, want zero", net)
	}

	// The stock is back on the original layer, so the next sale draws it at the
	// same cost -- not at whatever a new layer would have guessed.
	if got := onHandFor(ctx, t, w, w.budi); got != 7 {
		t.Errorf("on hand = %d after returning everything, want 7", got)
	}

	// And a further return has nothing left to give back.
	if _, err := w.sales.CreateReturn(ctx, w.actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID, Reason: "sekali lagi",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("returned more than was sold: %v", err)
	}
}

func TestReturnRequiresAReason(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)
	w.till(ctx, t)
	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 2})
	_, lines, _, _ := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)

	if _, err := w.sales.CreateReturn(ctx, w.actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID,
		Lines:  []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	}); !errors.Is(err, service.ErrReasonRequired) {
		t.Errorf("got %v, want ErrReasonRequired", err)
	}
	if _, err := w.sales.Void(ctx, w.actor, sale.Sale.ID, ""); !errors.Is(err, service.ErrReasonRequired) {
		t.Errorf("void without a reason: %v", err)
	}
}

// R9.8 and R9.10: the Z-report. Non-cash takings are recorded but never
// expected in the drawer -- counting a QRIS payment as cash is how a till
// "loses" money that was never in it.
func TestZReportExpectsOnlyCashInTheDrawer(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 100, 1_000, 0, false)
	session := w.till(ctx, t)

	price := money.IDR(10_000)
	pay := func(method string, amount money.IDR, qty int64) {
		t.Helper()
		if _, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
			SaleDate: "2026-10-15",
			Lines:    []service.SaleLineInput{{ProductID: w.gloves, Qty: qty, UnitPriceIDR: &price}},
			Payments: []service.PaymentInputLine{{Method: method, AmountIDR: amount, Reference: "ref-1"}},
		}); err != nil {
			t.Fatalf("ring %s: %v", method, err)
		}
	}

	pay("TUNAI", 30_000, 3)
	pay("QRIS", 50_000, 5)
	pay("TRANSFER", 20_000, 2)

	totals, err := w.sales.Totals(ctx, w.entityID, session.ID)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if totals.TotalTakings != 100_000 {
		t.Errorf("takings = %s, want %s", totals.TotalTakings, money.IDR(100_000))
	}
	// Opening float 500.000 plus 30.000 cash. The 70.000 of QRIS and transfer
	// is real money, and none of it is in the drawer.
	if totals.ExpectedCash != 530_000 {
		t.Errorf("expected cash = %s, want %s -- non-cash must not be counted into the drawer",
			totals.ExpectedCash, money.IDR(530_000))
	}

	// Close it short by Rp 5.000 and the variance is recorded, not hidden.
	closed, err := w.sales.CloseSession(ctx, w.actor, session.ID, 525_000, "kurang 5rb")
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Session.VarianceIdr == nil || *closed.Session.VarianceIdr != -5_000 {
		t.Errorf("variance = %v, want -5000", closed.Session.VarianceIdr)
	}
	if _, err := w.sales.CloseSession(ctx, w.actor, session.ID, 525_000, ""); !errors.Is(err, service.ErrSessionClosed) {
		t.Errorf("closed the till twice: %v", err)
	}
}

// R9.11: a credit sale raises piutang from the sales screen.
func TestCreditSaleRaisesPiutang(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)
	w.till(ctx, t)

	customer, err := w.q.CreateCustomer(ctx, gen.CreateCustomerParams{
		ID: layerIDFor(t), Code: "C1", Name: "Klinik Harapan",
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}

	price := money.IDR(25_000)
	got, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate: "2026-10-15", CustomerID: customer.ID, IsCredit: true, DueDate: "2026-11-15",
		Lines: []service.SaleLineInput{{ProductID: w.gloves, Qty: 4, UnitPriceIDR: &price}},
	})
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	if got.Receivable == nil {
		t.Fatal("a credit sale raised no piutang")
	}
	if got.Receivable.AmountIdr != 100_000 || got.Receivable.Source != "SALE" {
		t.Errorf("piutang = %+v, want 100000 sourced from the sale", got.Receivable)
	}
	// The forward foreign key migration 009 owed migration 008.
	if got.Receivable.SaleID == nil || *got.Receivable.SaleID != got.Sale.ID {
		t.Errorf("piutang does not name the sale behind it: %v", got.Receivable.SaleID)
	}

	// A credit sale with nobody to chase is refused (R5.6).
	if _, err := w.sales.Ring(ctx, w.actor, service.SaleInput{
		SaleDate: "2026-10-15", IsCredit: true,
		Lines: []service.SaleLineInput{{ProductID: w.gloves, Qty: 1}},
	}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("accepted a credit sale with no customer: %v", err)
	}
}

func layerIDFor(t *testing.T) string {
	t.Helper()
	return store.NewID()
}

// onHandFor is what one owner currently holds of the test product.
func onHandFor(ctx context.Context, t *testing.T, w world, owner string) int64 {
	t.Helper()

	rows, err := w.opname.CountSheet(ctx, w.entityID)
	if err != nil {
		t.Fatalf("count sheet: %v", err)
	}
	for _, r := range rows {
		if r.ProductID == w.gloves && r.OwnerID != nil && *r.OwnerID == owner {
			return r.QtyOnHand
		}
	}
	return 0
}

// R12.3's line, enforced in the direction it was missing.
//
// A void and a return are separated on one fact: whether the goods ever left
// the shop. A return is proof they did, so the two cannot both apply to one
// sale. Voiding after a return was accepted before this guard, and the damage
// landed in the till rather than the stock: the void withdrew the sale's
// takings from the session while the cash already refunded against it still
// stood, so the Z-report reported the drawer as over by the whole sale — money
// that is really there, recorded as a surplus for somebody to guess at.
func TestASaleThatHasBeenReturnedCannotBeVoided(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)
	session := w.till(ctx, t)

	price := money.IDR(20_000)
	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 4, UnitPriceIDR: &price})
	_, lines, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// One of the four comes back, refunded in cash, while the till is still
	// open — so the void window has not closed and nothing else would stop it.
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.CreateReturn(ctx, actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID, ReturnDate: "2026-10-15", Reason: "Kemasan rusak",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	}); err != nil {
		t.Fatalf("return: %v", err)
	}

	before, err := w.sales.Totals(ctx, w.entityID, session.ID)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	// Float 500.000, plus 80.000 taken, less 20.000 refunded.
	if before.ExpectedCash != 560_000 {
		t.Fatalf("expected cash before = %s, want %s", before.ExpectedCash, money.IDR(560_000))
	}

	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.Void(ctx, actor, sale.Sale.ID, "salah input"); !errors.Is(err, service.ErrVoidAfterReturn) {
		t.Fatalf("got %v, want ErrVoidAfterReturn", err)
	}

	// Refused means nothing moved: the sale still stands and the drawer still
	// reconciles to what is actually in it.
	after, err := w.sales.Totals(ctx, w.entityID, session.ID)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if after.ExpectedCash != before.ExpectedCash {
		t.Errorf("expected cash moved from %s to %s on a refused void",
			before.ExpectedCash, after.ExpectedCash)
	}
	current, _, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.Status != "FINAL" {
		t.Errorf("sale status = %s after a refused void", current.Status)
	}

	// The honest correction is still available: return the rest.
	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.CreateReturn(ctx, actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID, ReturnDate: "2026-10-15", Reason: "Sisanya ikut dikembalikan",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 3}},
	}); err != nil {
		t.Errorf("returning the remainder was refused: %v", err)
	}
}

// The pair, stated once: a voided sale cannot be returned either. Together
// these mean a sale is never both voided and returned, which is what lets the
// Z-report and the margin report each read one of the two without checking for
// the other.
func TestAVoidedSaleCannotBeReturned(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)
	w.till(ctx, t)

	sale := w.ring(ctx, t, service.SaleLineInput{ProductID: w.gloves, Qty: 4})
	_, lines, _, err := w.sales.GetSale(ctx, w.entityID, sale.Sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.Void(ctx, actor, sale.Sale.ID, "salah input"); err != nil {
		t.Fatalf("void: %v", err)
	}

	actor.ClientRequestID = store.NewID()
	_, err = w.sales.CreateReturn(ctx, actor, service.SaleReturnInput{
		SaleID: sale.Sale.ID, ReturnDate: "2026-10-16", Reason: "Barang dikembalikan",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	})
	if !errors.Is(err, service.ErrAlreadyVoid) {
		t.Fatalf("got %v, want ErrAlreadyVoid", err)
	}
}
