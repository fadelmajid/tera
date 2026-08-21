package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// fixedNow keeps every test deterministic. 2026-10-15 14:30 UTC is 21:30 WIB,
// still the 15th in Jakarta -- deliberately not a case that straddles midnight,
// which INV-5 gets its own tests for.
var fixedNow = time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC)

// world is a company with people, products and a supplier in it.
type world struct {
	db       *store.DB
	q        *gen.Queries
	purch    *service.Purchasing
	opname   *service.Opname
	opening  *service.Opening
	actor    service.Actor
	entityID string
	supplier string
	gloves   string
	syringe  string
	budi     string
	sari     string
}

// newWorld builds a company. isPKP is the switch that changes what a purchase
// costs (SPEC §2.3), so it is the parameter.
func newWorld(t *testing.T, isPKP bool) (world, context.Context) {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := func() time.Time { return fixedNow }
	aud := service.NewAuditor(now)
	q := gen.New(db)

	entity, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "E1", Name: "PT Sehat Sentosa", IsPkp: boolInt(isPKP),
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("entity: %v", err)
	}

	owner := func(code, name string) string {
		t.Helper()
		o, err := q.CreateOwner(ctx, gen.CreateOwnerParams{ID: store.NewID(), Code: code, Name: name})
		if err != nil {
			t.Fatalf("owner %s: %v", code, err)
		}
		return o.ID
	}
	budi, sari := owner("BUDI", "Budi"), owner("SARI", "Sari")

	product := func(code, name string, ownerID *string) string {
		t.Helper()
		p, err := q.CreateProduct(ctx, gen.CreateProductParams{
			ID: store.NewID(), Code: code, Name: name, Unit: "box",
			OwnerID: ownerID, SalePriceIdr: 150_000,
		})
		if err != nil {
			t.Fatalf("product %s: %v", code, err)
		}
		return p.ID
	}

	supplier, err := q.CreateSupplier(ctx, gen.CreateSupplierParams{
		ID: store.NewID(), Code: "S1", Name: "PT Medika Jaya", IssuesFaktur: 1,
	})
	if err != nil {
		t.Fatalf("supplier: %v", err)
	}

	w := world{
		db: db, q: q,
		purch:   service.NewPurchasing(db, aud, now),
		opname:  service.NewOpname(db, aud, now),
		opening: service.NewOpening(db, aud, now),
		actor: service.Actor{
			LegalEntityID: entity.ID, ClientRequestID: store.NewID(),
		},
		entityID: entity.ID, supplier: supplier.ID,
		gloves: product("P-GLOVE", "Sarung Tangan", &budi),
		budi:   budi, sari: sari,
	}
	w.syringe = product("P-SYR", "Spuit 3ml", &sari)
	return w, ctx
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// buy records one purchase of `qty` gloves at `unit` with `ppn` PPN.
func (w world) buy(ctx context.Context, t *testing.T, qty int64, unit, ppn money.IDR, faktur bool) service.PurchaseResult {
	t.Helper()

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	got, err := w.purch.CreatePurchase(ctx, actor, service.PurchaseInput{
		SupplierID: w.supplier, FakturReceived: faktur, PurchaseDate: "2026-10-01",
		Lines: []service.PurchaseLineInput{
			{ProductID: w.gloves, Qty: qty, UnitPriceIDR: unit, PPNIDR: ppn},
		},
	})
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	return got
}

// TestPurchaseWithAndWithoutFakturProducesDifferentLayerCosts is TASKS 1.7 ⭐
// end to end, and acceptance criterion 5.
//
// The domain test of the same name proves the rule in isolation. This one
// proves the rule survives the trip through the service and into the database:
// two purchases from one supplier at one price, one with a faktur and one
// without, must leave two stock layers carrying different costs -- and only the
// one with the faktur may contribute to the input PPN position.
//
// If both layers ever cost the same, the business is back where it started:
// no-faktur purchases looking ~11% cheaper than they are, and the margin on
// those goods overstated by the same amount, in the one report the family
// splits money on.
func TestPurchaseWithAndWithoutFakturProducesDifferentLayerCosts(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true) // PKP: the only entity that can credit anything

	// 10 boxes at Rp 10.000, Rp 11.000 PPN, Rp 111.000 paid. Both times.
	withFaktur := w.buy(ctx, t, 10, 10_000, 11_000, true)
	without := w.buy(ctx, t, 10, 10_000, 11_000, false)

	// --- the layers ---------------------------------------------------------

	a, b := withFaktur.Layers[0], without.Layers[0]

	if a.CostTotalIdr != 100_000 {
		t.Errorf("with faktur: layer cost %d, want 100000 (the PPN is recoverable, not cost)", a.CostTotalIdr)
	}
	if b.CostTotalIdr != 111_000 {
		t.Errorf("without faktur: layer cost %d, want 111000 (nothing to credit, so the PPN is cost)", b.CostTotalIdr)
	}
	if a.CostTotalIdr == b.CostTotalIdr {
		t.Fatal("faktur and no-faktur purchases produced the same layer cost; " +
			"this is the bug the product exists to fix")
	}
	if gap := b.CostTotalIdr - a.CostTotalIdr; gap != 11_000 {
		t.Errorf("the layers differ by %d, want exactly the PPN paid (11000)", gap)
	}

	// The faktur status travels onto the layer, not just the purchase, so the
	// cost can still be justified years later (INV-9).
	if a.FakturReceived != 1 || b.FakturReceived != 0 {
		t.Errorf("faktur status did not reach the layers: %d and %d", a.FakturReceived, b.FakturReceived)
	}
	// The PPN paid is recorded either way; what differs is what became of it.
	if a.PpnPaidIdr != 11_000 || b.PpnPaidIdr != 11_000 {
		t.Errorf("ppn_paid = %d and %d, want 11000 on both -- the money left the account regardless",
			a.PpnPaidIdr, b.PpnPaidIdr)
	}

	// --- the lines ----------------------------------------------------------

	if got := withFaktur.Lines[0].CreditablePpnIdr; got != 11_000 {
		t.Errorf("with faktur: creditable PPN %d, want 11000", got)
	}
	if got := without.Lines[0].CreditablePpnIdr; got != 0 {
		t.Errorf("without faktur: creditable PPN %d, want 0", got)
	}

	// --- the PPN position (SPEC §2.4) ---------------------------------------
	// Only the purchase with the faktur is on the input side. The other one's
	// PPN went into the cost layer above; counting it here too would claim the
	// same rupiah twice.

	pos, err := w.purch.InputPPN(ctx, w.entityID, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("input ppn: %v", err)
	}
	if pos.Creditable != 11_000 {
		t.Errorf("creditable input PPN = %s, want %s -- only the faktur purchase counts",
			pos.Creditable, money.IDR(11_000))
	}
}

// The same purchase, in the non-PKP company, costs the full amount. A non-PKP
// entity can never credit input PPN whatever paperwork it holds (SPEC §2.3), so
// the faktur toggle changes nothing there -- and the identical purchase is
// genuinely more expensive in one company than the other.
func TestNonPKPEntityNeverCreditsInputPPN(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, false)

	withFaktur := w.buy(ctx, t, 10, 10_000, 11_000, true)
	without := w.buy(ctx, t, 10, 10_000, 11_000, false)

	for name, r := range map[string]service.PurchaseResult{"with faktur": withFaktur, "without": without} {
		if got := r.Layers[0].CostTotalIdr; got != 111_000 {
			t.Errorf("%s: layer cost %d, want 111000 -- a non-PKP entity can never credit", name, got)
		}
		if got := r.Lines[0].CreditablePpnIdr; got != 0 {
			t.Errorf("%s: creditable PPN %d, want 0", name, got)
		}
	}

	pos, err := w.purch.InputPPN(ctx, w.entityID, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("input ppn: %v", err)
	}
	if !pos.Creditable.IsZero() {
		t.Errorf("non-PKP entity reported %s of creditable input PPN, want zero", pos.Creditable)
	}
}

// The layer's owner is snapshotted from the product at purchase time, not
// joined at report time. Re-tagging a product next year must not move whose
// margin last year's stock belonged to -- that money has already been settled
// between family members (INV-8, R2.4).
func TestLayerOwnerIsSnapshottedNotJoined(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	bought := w.buy(ctx, t, 5, 10_000, 0, false)
	layerID := bought.Layers[0].ID
	if bought.Layers[0].OwnerID == nil || *bought.Layers[0].OwnerID != w.budi {
		t.Fatalf("layer owner = %v, want Budi", bought.Layers[0].OwnerID)
	}

	// The product changes hands.
	if _, err := w.q.UpdateProduct(ctx, gen.UpdateProductParams{
		ID: w.gloves, Code: "P-GLOVE", Name: "Sarung Tangan", Unit: "box",
		OwnerID: &w.sari, SalePriceIdr: 150_000, IsActive: 1,
	}); err != nil {
		t.Fatalf("retag product: %v", err)
	}

	after, err := w.q.GetStockLayer(ctx, layerID)
	if err != nil {
		t.Fatalf("get layer: %v", err)
	}
	if after.OwnerID == nil || *after.OwnerID != w.budi {
		t.Errorf("re-tagging the product moved existing stock to %v; it must stay Budi's", after.OwnerID)
	}
}

func TestPurchaseIsAtomicAndAudited(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	// A second line naming a product that does not exist must take the whole
	// purchase down with it. Stock the business paid for and cannot sell, or
	// layers with no purchase behind them, are both worse than a refusal.
	_, err := w.purch.CreatePurchase(ctx, w.actor, service.PurchaseInput{
		SupplierID: w.supplier, PurchaseDate: "2026-10-01",
		Lines: []service.PurchaseLineInput{
			{ProductID: w.gloves, Qty: 5, UnitPriceIDR: 10_000},
			{ProductID: store.NewID(), Qty: 5, UnitPriceIDR: 10_000},
		},
	})
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}

	purchases, err := w.purch.ListPurchases(ctx, w.entityID, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(purchases) != 0 {
		t.Errorf("a failed purchase left %d rows behind", len(purchases))
	}
	layers, err := w.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: w.entityID, ProductID: w.gloves,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) != 0 {
		t.Errorf("a failed purchase left %d stock layers behind", len(layers))
	}

	// A good one writes its audit row in the same transaction (INV-10).
	got := w.buy(ctx, t, 5, 10_000, 0, false)
	trail, err := w.q.ListAuditForRecord(ctx, gen.ListAuditForRecordParams{
		RecordType: "purchase", RecordID: got.Purchase.ID,
	})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(trail) != 1 || trail[0].Action != "CREATE" {
		t.Errorf("got %d audit rows for the purchase, want 1 CREATE", len(trail))
	}
}

func TestCreditPurchaseCreatesHutang(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	got, err := w.purch.CreatePurchase(ctx, w.actor, service.PurchaseInput{
		SupplierID: w.supplier, PurchaseDate: "2026-10-01",
		IsCredit: true, DueDate: "2026-11-01",
		Lines: []service.PurchaseLineInput{
			{ProductID: w.gloves, Qty: 10, UnitPriceIDR: 10_000, PPNIDR: 11_000},
		},
	})
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	if got.Payable == nil {
		t.Fatal("a credit purchase created no hutang")
	}
	// Hutang is what is owed -- the full invoice including PPN, not the cost
	// basis. What the goods cost and what the supplier is owed are different
	// numbers whenever a faktur is involved.
	if got.Payable.AmountIdr != 111_000 {
		t.Errorf("hutang = %d, want 111000 (the invoice total, not the cost basis)", got.Payable.AmountIdr)
	}

	outstanding, err := w.opening.ListPayables(ctx, w.entityID, true)
	if err != nil {
		t.Fatalf("list payables: %v", err)
	}
	if len(outstanding) != 1 || outstanding[0].OutstandingIdr != 111_000 {
		t.Fatalf("outstanding hutang = %+v", outstanding)
	}

	// Partial payment (R5.8). The balance is derived from the payments.
	if _, err := w.opening.PayPayable(ctx, w.actor, got.Payable.ID, service.PaymentInput{
		AmountIDR: 50_000, PaidOn: "2026-10-20", Method: "TRANSFER",
	}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	after, err := w.q.GetPayable(ctx, got.Payable.ID)
	if err != nil {
		t.Fatalf("get payable: %v", err)
	}
	if after.PaidIdr != 50_000 || after.OutstandingIdr != 61_000 {
		t.Errorf("paid %d outstanding %d, want 50000 and 61000", after.PaidIdr, after.OutstandingIdr)
	}

	// Overpaying is a typo, not a credit note.
	if _, err := w.opening.PayPayable(ctx, w.actor, got.Payable.ID, service.PaymentInput{
		AmountIDR: 100_000, PaidOn: "2026-10-21",
	}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("accepted an overpayment: %v", err)
	}
}

func TestPurchaseValidation(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	good := service.PurchaseInput{
		SupplierID: w.supplier, PurchaseDate: "2026-10-01",
		Lines: []service.PurchaseLineInput{{ProductID: w.gloves, Qty: 5, UnitPriceIDR: 10_000}},
	}

	tests := []struct {
		name   string
		mutate func(*service.PurchaseInput)
	}{
		{"no supplier", func(in *service.PurchaseInput) { in.SupplierID = "" }},
		{"no lines", func(in *service.PurchaseInput) { in.Lines = nil }},
		{"zero quantity", func(in *service.PurchaseInput) { in.Lines[0].Qty = 0 }},
		{"negative price", func(in *service.PurchaseInput) { in.Lines[0].UnitPriceIDR = -1 }},
		{"negative PPN", func(in *service.PurchaseInput) { in.Lines[0].PPNIDR = -1 }},
		{
			"a faktur number without the faktur",
			func(in *service.PurchaseInput) { in.FakturNo = "010.000-26.00000001"; in.FakturReceived = false },
		},
		{"a due date on a cash purchase", func(in *service.PurchaseInput) { in.DueDate = "2026-11-01" }},
		{"a date that is not a date", func(in *service.PurchaseInput) { in.PurchaseDate = "01/10/2026" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := good
			in.Lines = append([]service.PurchaseLineInput(nil), good.Lines...)
			tc.mutate(&in)

			if _, err := w.purch.CreatePurchase(ctx, w.actor, in); err == nil {
				t.Errorf("accepted %s", tc.name)
			}
		})
	}
}

// R12.2: a purchase return removes stock, reverses the cost layer, and gives
// back any input PPN claimed on the returned goods.
func TestPurchaseReturnReversesStockAndInputPPN(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	bought := w.buy(ctx, t, 10, 10_000, 11_000, true) // creditable: Rp 11.000

	line := bought.Lines[0]
	ret, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID, ReturnDate: "2026-10-05",
		Reason: "Barang rusak saat diterima",
		Lines:  []service.PurchaseReturnLineInput{{PurchaseLineID: line.ID, Qty: 4}},
	})
	if err != nil {
		t.Fatalf("return: %v", err)
	}

	// Stock left: 4 of 10 gone back.
	balance, err := w.q.GetLayerBalance(ctx, bought.Layers[0].ID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.QtyRemaining != 6 {
		t.Errorf("layer holds %d, want 6 after returning 4", balance.QtyRemaining)
	}

	// Cost went with it: 4/10 of Rp 100.000.
	if ret.Return.CostIdr != 40_000 {
		t.Errorf("returned cost = %d, want 40000", ret.Return.CostIdr)
	}
	// And 4/10 of the claimed input PPN is handed back. Keeping credit for
	// goods that went back is the error R12.2 names.
	if ret.Return.PpnReversedIdr != 4_400 {
		t.Errorf("reversed input PPN = %d, want 4400", ret.Return.PpnReversedIdr)
	}

	pos, err := w.purch.InputPPN(ctx, w.entityID, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("input ppn: %v", err)
	}
	if pos.Net != 6_600 {
		t.Errorf("net input PPN = %s, want %s", pos.Net, money.IDR(6_600))
	}

	// Returning the rest gives back exactly the remainder, to the rupiah.
	second, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID, ReturnDate: "2026-10-06",
		Reason: "Sisa kiriman ikut dikembalikan",
		Lines:  []service.PurchaseReturnLineInput{{PurchaseLineID: line.ID, Qty: 6}},
	})
	if err != nil {
		t.Fatalf("second return: %v", err)
	}
	if total := ret.Return.CostIdr + second.Return.CostIdr; total != 100_000 {
		t.Errorf("the two returns gave back %d in total, want exactly the layer cost 100000", total)
	}
	if total := ret.Return.PpnReversedIdr + second.Return.PpnReversedIdr; total != 11_000 {
		t.Errorf("reversed PPN totals %d, want exactly the 11000 claimed", total)
	}

	// A third return has nothing left to send back.
	if _, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID, Reason: "sekali lagi",
		Lines: []service.PurchaseReturnLineInput{{PurchaseLineID: line.ID, Qty: 1}},
	}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("returned more than was ever bought: %v", err)
	}
}

// Goods already sold cannot go back to the supplier. Pretending otherwise
// drives the layer negative and corrupts every margin drawn from it.
func TestPurchaseReturnCannotSendBackSoldGoods(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	bought := w.buy(ctx, t, 10, 10_000, 0, false)

	// Eight boxes leave through the front door.
	if _, err := w.q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: bought.Layers[0].ID, MovementID: store.NewID(),
		MovementType: "SALE", QtyOut: 8, CostIdr: 80_000,
		OccurredAt: fixedNow.Unix(), BusinessDate: "2026-10-03", CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sale: %v", err)
	}

	_, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID, Reason: "Rusak",
		Lines: []service.PurchaseReturnLineInput{{PurchaseLineID: bought.Lines[0].ID, Qty: 5}},
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("got %v, want a refusal: only 2 of the 10 are still on the shelf", err)
	}

	// Two is fine.
	if _, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID, Reason: "Rusak",
		Lines: []service.PurchaseReturnLineInput{{PurchaseLineID: bought.Lines[0].ID, Qty: 2}},
	}); err != nil {
		t.Errorf("refused a return of what is actually on the shelf: %v", err)
	}
}

func TestPurchaseReturnRequiresAReason(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	bought := w.buy(ctx, t, 10, 10_000, 0, false)

	_, err := w.purch.CreatePurchaseReturn(ctx, w.actor, service.PurchaseReturnInput{
		PurchaseID: bought.Purchase.ID,
		Lines:      []service.PurchaseReturnLineInput{{PurchaseLineID: bought.Lines[0].ID, Qty: 1}},
	})
	if !errors.Is(err, service.ErrReasonRequired) {
		t.Errorf("got %v, want ErrReasonRequired (R12.5)", err)
	}
}

// A purchase belonging to the other company must not be readable by naming its
// id (R13.4).
func TestPurchaseIsScopedToItsCompany(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	bought := w.buy(ctx, t, 5, 10_000, 0, false)

	if _, _, err := w.purch.GetPurchase(ctx, store.NewID(), bought.Purchase.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("a purchase was readable from another company: %v", err)
	}
}
