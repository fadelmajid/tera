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

// --- a two-company world ----------------------------------------------------

// pair is the user's actual setup: two companies under one family, one PKP and
// one not, sharing a product catalogue and the same owners (D-006, R1.3).
//
// The shared catalogue is what makes a transfer a movement rather than a
// reconciliation: there is one product id on both sides, so there is nothing to
// match on and nothing to drift.
type pair struct {
	db    *store.DB
	q     *gen.Queries
	purch *service.Purchasing
	trf   *service.Transfers
	sales *service.Sales
	mrg   *service.Margin

	pkp, nonPKP     string
	budi, sari      string
	gloves, syringe string
	supplier        string
}

func newPair(t *testing.T) (pair, context.Context) {
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

	entity := func(code, name string, isPKP bool) string {
		t.Helper()
		e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
			ID: store.NewID(), Code: code, Name: name, IsPkp: boolInt(isPKP),
			Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
		})
		if err != nil {
			t.Fatalf("entity %s: %v", code, err)
		}
		return e.ID
	}
	owner := func(code, name string) string {
		t.Helper()
		o, err := q.CreateOwner(ctx, gen.CreateOwnerParams{ID: store.NewID(), Code: code, Name: name})
		if err != nil {
			t.Fatalf("owner %s: %v", code, err)
		}
		return o.ID
	}

	p := pair{
		db: db, q: q,
		purch: service.NewPurchasing(db, aud, now),
		trf:   service.NewTransfers(db, aud, now),
		sales: service.NewSales(db, aud, now),
		mrg:   service.NewMargin(db, aud, now),
		pkp:   entity("PKP", "PT Sehat Sentosa", true),
	}
	p.nonPKP = entity("NONPKP", "PT Medika Jaya", false)
	p.budi, p.sari = owner("BUDI", "Budi"), owner("SARI", "Sari")

	product := func(code, name, ownerID string) string {
		t.Helper()
		row, err := q.CreateProduct(ctx, gen.CreateProductParams{
			ID: store.NewID(), Code: code, Name: name, Unit: "box",
			OwnerID: &ownerID, SalePriceIdr: 150_000,
		})
		if err != nil {
			t.Fatalf("product %s: %v", code, err)
		}
		return row.ID
	}
	p.gloves = product("P-GLOVE", "Sarung Tangan", p.budi)
	p.syringe = product("P-SYR", "Spuit 3ml", p.sari)

	supplier, err := q.CreateSupplier(ctx, gen.CreateSupplierParams{
		ID: store.NewID(), Code: "S1", Name: "PT Medika Farma", IssuesFaktur: 1,
	})
	if err != nil {
		t.Fatalf("supplier: %v", err)
	}
	p.supplier = supplier.ID
	return p, ctx
}

func (p pair) actor(entityID string) service.Actor {
	return service.Actor{LegalEntityID: entityID, ClientRequestID: store.NewID()}
}

// stockInto buys stock into one company, so a transfer has something to move.
func (p pair) stockInto(ctx context.Context, t *testing.T, entityID, productID string, qty int64, unit, ppn money.IDR, faktur bool) {
	t.Helper()

	in := service.PurchaseInput{
		SupplierID: p.supplier, PurchaseDate: "2026-10-01", FakturReceived: faktur,
		Lines: []service.PurchaseLineInput{
			{ProductID: productID, Qty: qty, UnitPriceIDR: unit, PPNIDR: ppn},
		},
	}
	if faktur {
		in.FakturNo = "010.000-26.00000001"
	}
	if _, err := p.purch.CreatePurchase(ctx, p.actor(entityID), in); err != nil {
		t.Fatalf("purchase into %s: %v", entityID, err)
	}
}

func (p pair) onHand(ctx context.Context, t *testing.T, entityID, productID string) (qty int64, cost money.IDR) {
	t.Helper()

	layers, err := p.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: entityID, ProductID: productID,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	for _, l := range layers {
		qty += l.QtyRemaining
		cost = cost.Add(money.IDR(l.CostTotalIdr).MulRatio(l.QtyRemaining, l.QtyIn))
	}
	return qty, cost
}

func (p pair) destLayers(ctx context.Context, t *testing.T, transferID string) []gen.StockLayer {
	t.Helper()

	rows, err := p.q.ListLayersBySourceDoc(ctx, gen.ListLayersBySourceDocParams{
		Source: "TRANSFER_IN", SourceDocID: &transferID,
	})
	if err != nil {
		t.Fatalf("dest layers: %v", err)
	}
	return rows
}

// --- 4.6: atomic, and owner attribution preserved ---------------------------

// TestTransferMovesBothSidesOrNeither is TASKS 4.6 and the whole reason Phase 4
// exists.
//
// Olsera records the inter-company transaction and does not move the stock,
// which is why the user's quantities are drifting from reality today
// (REQUIREMENTS §2). A partial commit here is the same bug with extra steps:
// goods that left one company and never arrived in the other, or arrived
// without having left, with nothing in the system saying so.
func TestTransferMovesBothSidesOrNeither(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true) // layer cost 100.000
	p.stockInto(ctx, t, p.pkp, p.syringe, 2, 5_000, 0, false)      // only 2 syringes

	// A transfer whose second line asks for stock that is not there. The first
	// line is perfectly good, which is exactly the case a partial commit ruins.
	_, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{
			{ProductID: p.gloves, Qty: 4},
			{ProductID: p.syringe, Qty: 5},
		},
	})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}

	// Neither side moved.
	if qty, _ := p.onHand(ctx, t, p.pkp, p.gloves); qty != 10 {
		t.Errorf("source gloves = %d, want 10 — a failed transfer consumed stock", qty)
	}
	if qty, _ := p.onHand(ctx, t, p.nonPKP, p.gloves); qty != 0 {
		t.Errorf("destination gloves = %d, want 0 — a failed transfer created a layer", qty)
	}
	transfers, err := p.trf.List(ctx, p.pkp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(transfers) != 0 {
		t.Errorf("a failed transfer left %d documents behind", len(transfers))
	}

	// The successful case moves both sides together.
	got, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 4, PPNIDR: 4_400}},
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	srcQty, _ := p.onHand(ctx, t, p.pkp, p.gloves)
	dstQty, _ := p.onHand(ctx, t, p.nonPKP, p.gloves)
	if srcQty != 6 || dstQty != 4 {
		t.Errorf("after the transfer: source %d, destination %d; want 6 and 4", srcQty, dstQty)
	}
	if got.Consumptions != 1 || len(got.Lines) != 1 {
		t.Errorf("wrote %d consumptions and %d lines, want 1 and 1", got.Consumptions, len(got.Lines))
	}
	// At cost, no markup (R4.3): four of a ten-unit Rp 100.000 layer.
	if got.Cost != 40_000 {
		t.Errorf("transfer cost = %s, want %s", got.Cost, money.IDR(40_000))
	}
}

// SPEC §3.4: the family member owning the goods does not change because the
// goods crossed a company line. If this is wrong, stock silently changes hands
// between family members — the same class of error as drawing another owner's
// layers, and just as invisible.
func TestTransferCarriesOwnerAttributionAcross(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true) // Budi's
	p.stockInto(ctx, t, p.pkp, p.syringe, 10, 5_000, 5_500, true)  // Sari's

	got, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{
			{ProductID: p.gloves, Qty: 4, PPNIDR: 4_400},
			{ProductID: p.syringe, Qty: 5, PPNIDR: 2_750},
		},
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	layers := p.destLayers(ctx, t, got.Transfer.ID)
	if len(layers) != 2 {
		t.Fatalf("created %d destination layers, want 2", len(layers))
	}

	byProduct := make(map[string]gen.StockLayer, 2)
	for _, l := range layers {
		byProduct[l.ProductID] = l
	}
	for _, tt := range []struct {
		productID string
		wantOwner string
		who       string
	}{
		{p.gloves, p.budi, "Budi"},
		{p.syringe, p.sari, "Sari"},
	} {
		layer, ok := byProduct[tt.productID]
		if !ok {
			t.Fatalf("no destination layer for product %s", tt.productID)
		}
		if layer.OwnerID == nil {
			t.Errorf("%s's stock arrived in the company bucket", tt.who)
			continue
		}
		if *layer.OwnerID != tt.wantOwner {
			t.Errorf("%s's stock arrived attributed to %s", tt.who, *layer.OwnerID)
		}
	}

	// And the transfer lines record it too, snapshotted like a sale line.
	for _, l := range got.Lines {
		if l.OwnerID == nil {
			t.Errorf("transfer line for %s carries no owner (INV-8)", l.ProductID)
		}
	}
}

// The company bucket crosses as itself (R2.2). Unowned stock is a real
// attribution, not a missing one, and it must not acquire an owner on the way.
func TestCompanyOwnedStockStaysUnowned(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	mask, err := p.q.CreateProduct(ctx, gen.CreateProductParams{
		ID: store.NewID(), Code: "P-MASK", Name: "Masker", Unit: "box",
		OwnerID: nil, SalePriceIdr: 4_000,
	})
	if err != nil {
		t.Fatalf("mask: %v", err)
	}
	p.stockInto(ctx, t, p.pkp, mask.ID, 30, 2_000, 6_600, true)

	got, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: mask.ID, Qty: 10, PPNIDR: 2_200}},
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if layers := p.destLayers(ctx, t, got.Transfer.ID); layers[0].OwnerID != nil {
		t.Errorf("company stock acquired owner %s crossing the boundary", *layers[0].OwnerID)
	}
}

// INV-8 at the boundary, as at the till: a transfer of Budi's goods never
// quietly draws Sari's, however much of the same product is on the shelf.
func TestTransferNeverDrawsAnotherOwnersStock(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 3, 10_000, 0, false) // Budi: 3

	// Sari holds a hundred of the same product in the same company.
	if _, err := p.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: p.pkp, ProductID: p.gloves, OwnerID: &p.sari,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 100, CostTotalIdr: 1_000_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sari's layer: %v", err)
	}

	_, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 5}},
	})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("got %v, want ErrInsufficientStock", err)
	}
	if qty, _ := p.onHand(ctx, t, p.pkp, p.gloves); qty != 103 {
		t.Errorf("stock on hand = %d, want 103 untouched", qty)
	}
}

// --- 4.3: the direction decides what the destination layer costs ------------

// SPEC §3.4's two rows, end to end. The same goods at the same cost arrive on
// the receiving company's books at different figures depending only on which
// way they crossed — and one of the two directions destroys something that
// cannot be got back.
func TestDirectionDecidesTheDestinationCost(t *testing.T) {
	t.Parallel()

	t.Run("PKP to non-PKP: PPN on the delivery becomes the receiver's cost", func(t *testing.T) {
		t.Parallel()

		p, ctx := newPair(t)
		// The PKP company bought with a faktur, so its layer is net of
		// creditable PPN: Rp 100.000 for ten.
		p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true)

		got, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
			ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
			FakturIssued: true, FakturNo: "010.000-26.00000009",
			Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 10, PPNIDR: 11_000}},
		})
		if err != nil {
			t.Fatalf("transfer: %v", err)
		}

		layer := p.destLayers(ctx, t, got.Transfer.ID)[0]
		// A taxable delivery the receiver can never credit: Rp 100.000 of
		// goods lands at Rp 111.000.
		if layer.CostTotalIdr != 111_000 {
			t.Errorf("destination layer cost = %d, want 111000", layer.CostTotalIdr)
		}
		// The receiver is non-PKP, so the faktur buys them nothing and the
		// layer must not claim otherwise (SPEC §3.2).
		if layer.FakturReceived != 0 {
			t.Error("a non-PKP company's layer is marked as carrying creditable PPN")
		}
		if layer.PpnPaidIdr != 11_000 {
			t.Errorf("ppn_paid = %d, want 11000 recorded even though it is not creditable", layer.PpnPaidIdr)
		}
		if got.ForfeitedPPN != 0 {
			t.Errorf("forfeited PPN = %s, want 0 — this direction destroys no credit", got.ForfeitedPPN)
		}
	})

	t.Run("non-PKP to PKP: cost crosses, credit does not", func(t *testing.T) {
		t.Parallel()

		p, ctx := newPair(t)
		// The non-PKP company paid PPN it could never credit, so it is in the
		// cost: Rp 111.000 for ten.
		p.stockInto(ctx, t, p.nonPKP, p.gloves, 10, 10_000, 11_000, true)

		got, err := p.trf.Create(ctx, p.actor(p.nonPKP), service.TransferInput{
			ToEntityID: p.pkp, TransferDate: "2026-10-05",
			AcknowledgeCreditLoss: true,
			Lines:                 []service.TransferLineInput{{ProductID: p.gloves, Qty: 10}},
		})
		if err != nil {
			t.Fatalf("transfer: %v", err)
		}

		layer := p.destLayers(ctx, t, got.Transfer.ID)[0]
		if layer.CostTotalIdr != 111_000 {
			t.Errorf("destination layer cost = %d, want 111000 at cost", layer.CostTotalIdr)
		}
		// The whole of R4.5 in one field. The PKP company now holds stock it
		// will owe full output PPN on, with nothing to set against it, and no
		// document can be produced later to change that.
		if layer.FakturReceived != 0 {
			t.Error("a non-PKP sender somehow produced a creditable layer")
		}
		// Rp 11.000 of input PPN was paid on this stock and can never now be
		// credited by anyone. That is the figure the warning quotes.
		if got.ForfeitedPPN != 11_000 {
			t.Errorf("forfeited PPN = %s, want %s", got.ForfeitedPPN, money.IDR(11_000))
		}
		if got.Transfer.CreditLossAck != 1 {
			t.Error("the transfer does not record that the loss was acknowledged")
		}
	})
}

// SPEC §2.3, enforced rather than conventional, at the boundary the user will
// actually meet it.
func TestANonPKPSenderCannotChargePPNOrIssueAFakturOnATransfer(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.nonPKP, p.gloves, 10, 10_000, 0, false)

	base := service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-05", AcknowledgeCreditLoss: true,
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 2}},
	}

	withPPN := base
	withPPN.Lines = []service.TransferLineInput{{ProductID: p.gloves, Qty: 2, PPNIDR: 2_200}}
	if _, err := p.trf.Create(ctx, p.actor(p.nonPKP), withPPN); !errors.Is(err, service.ErrValidation) {
		t.Errorf("a non-PKP company charged PPN: %v", err)
	}

	withFaktur := base
	withFaktur.FakturIssued = true
	if _, err := p.trf.Create(ctx, p.actor(p.nonPKP), withFaktur); !errors.Is(err, service.ErrValidation) {
		t.Errorf("a non-PKP company issued a faktur: %v", err)
	}
}

// --- 4.4: the warning is a precondition, not a message ----------------------

// TestNonPKPToPKPIsRefusedUntilAcknowledged is TASKS 4.4 and R4.5.
//
// The confirmation has to be blocking, and the only way to make a server
// enforce that is to make the acknowledgement a precondition of the write. A
// warning attached to a successful response is not a warning — by the time it
// arrives the input credit is already gone and there is nothing to act on.
func TestNonPKPToPKPIsRefusedUntilAcknowledged(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.nonPKP, p.gloves, 10, 10_000, 11_000, true)

	in := service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 4}},
	}

	_, err := p.trf.Create(ctx, p.actor(p.nonPKP), in)
	if !errors.Is(err, service.ErrCreditLossNotAcknowledged) {
		t.Fatalf("got %v, want ErrCreditLossNotAcknowledged", err)
	}
	// Refused means nothing moved. The point of a blocking confirmation is that
	// the state before and after a refusal are the same state.
	if qty, _ := p.onHand(ctx, t, p.nonPKP, p.gloves); qty != 10 {
		t.Errorf("source stock = %d after a refused transfer, want 10", qty)
	}
	if qty, _ := p.onHand(ctx, t, p.pkp, p.gloves); qty != 0 {
		t.Errorf("destination stock = %d after a refused transfer, want 0", qty)
	}

	in.AcknowledgeCreditLoss = true
	if _, err := p.trf.Create(ctx, p.actor(p.nonPKP), in); err != nil {
		t.Fatalf("acknowledged transfer: %v", err)
	}
}

// The flag cannot become something a client sets on every request. If it were
// accepted where it does not apply, the one direction it exists to guard would
// eventually go through silently.
func TestAcknowledgementIsRefusedWhereItDoesNotApply(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true)

	_, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		AcknowledgeCreditLoss: true,
		Lines:                 []service.TransferLineInput{{ProductID: p.gloves, Qty: 4, PPNIDR: 4_400}},
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("got %v, want the acknowledgement refused on a direction that keeps its credit", err)
	}
}

// A blocking dialog that cannot say what is at stake trains people to click
// through it. Preview is what lets it quote a figure — and the figure is the
// input PPN actually paid on the stock, not a percentage of a guess.
func TestPreviewQuotesTheCreditAboutToBeDestroyed(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	// Ten boxes bought with PPN of Rp 11.000, none of it creditable by a
	// non-PKP company, so it sits in the layer's cost.
	p.stockInto(ctx, t, p.nonPKP, p.gloves, 10, 10_000, 11_000, true)

	got, err := p.trf.Preview(ctx, p.actor(p.nonPKP), service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 4}},
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if !got.DestroysInputCredit {
		t.Error("preview does not flag the direction that destroys input credit")
	}
	if got.TaxableDelivery {
		t.Error("preview says a non-PKP delivery is taxable")
	}
	// Four of ten units: Rp 44.400 of cost and Rp 4.400 of input PPN gone.
	if got.Cost != 44_400 {
		t.Errorf("preview cost = %s, want %s", got.Cost, money.IDR(44_400))
	}
	if got.ForfeitedPPN != 4_400 {
		t.Errorf("forfeited PPN = %s, want %s", got.ForfeitedPPN, money.IDR(4_400))
	}
	// And it wrote nothing.
	if qty, _ := p.onHand(ctx, t, p.nonPKP, p.gloves); qty != 10 {
		t.Errorf("preview moved stock: source = %d, want 10", qty)
	}
	if list, _ := p.trf.List(ctx, p.nonPKP); len(list) != 0 {
		t.Errorf("preview wrote %d transfer documents", len(list))
	}
}

// Stock that never carried input PPN forfeits nothing, and the warning must say
// so rather than inventing 11% of something. A figure that is wrong in the
// harmless direction still teaches people to ignore the dialog.
func TestNothingIsForfeitedWhenNoInputPPNWasEverPaid(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	// Bought from a supplier who issued no faktur and charged no PPN.
	p.stockInto(ctx, t, p.nonPKP, p.gloves, 10, 10_000, 0, false)

	got, err := p.trf.Preview(ctx, p.actor(p.nonPKP), service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 4}},
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !got.DestroysInputCredit {
		t.Error("the direction still destroys credit in principle and must still be confirmed")
	}
	if got.ForfeitedPPN != 0 {
		t.Errorf("forfeited PPN = %s, want 0 — no input PPN was ever paid on this stock", got.ForfeitedPPN)
	}
}

// --- 4.5 / D-014: the net position ------------------------------------------

// What the two companies owe each other, at cost, netted off — and nowhere near
// the hutang and piutang reports, which are about suppliers and customers.
func TestInterCompanyPositionNetsBothDirections(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 20, 10_000, 22_000, true) // PKP holds gloves
	p.stockInto(ctx, t, p.nonPKP, p.syringe, 20, 5_000, 0, false)  // non-PKP holds syringes

	// PKP sends Rp 100.000 of goods plus Rp 11.000 PPN.
	if _, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 10, PPNIDR: 11_000}},
	}); err != nil {
		t.Fatalf("outbound: %v", err)
	}
	// non-PKP sends Rp 50.000 back, no PPN possible.
	if _, err := p.trf.Create(ctx, p.actor(p.nonPKP), service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-06", AcknowledgeCreditLoss: true,
		Lines: []service.TransferLineInput{{ProductID: p.syringe, Qty: 10}},
	}); err != nil {
		t.Fatalf("inbound: %v", err)
	}

	position, err := p.trf.Position(ctx, p.pkp)
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if len(position) != 1 {
		t.Fatalf("got %d counterparties, want 1", len(position))
	}
	got := position[0]
	if got.CounterpartyName != "PT Medika Jaya" {
		t.Errorf("counterparty = %q", got.CounterpartyName)
	}
	if got.Out != 111_000 || got.In != 50_000 {
		t.Errorf("out/in = %s/%s, want 111.000/50.000", got.Out, got.In)
	}
	// Positive means the counterparty owes this company.
	if got.Net != 61_000 {
		t.Errorf("net = %s, want %s", got.Net, money.IDR(61_000))
	}

	// The other company sees the mirror image, not a second opinion.
	mirror, err := p.trf.Position(ctx, p.nonPKP)
	if err != nil {
		t.Fatalf("mirror position: %v", err)
	}
	if mirror[0].Net != -61_000 {
		t.Errorf("mirror net = %s, want %s", mirror[0].Net, money.IDR(-61_000))
	}
}

// The transfer is one document belonging to two companies, and a company that
// is not party to it cannot read it by naming its id (R13.4).
func TestATransferIsReadableFromBothSidesAndNoOther(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true)

	got, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 4, PPNIDR: 4_400}},
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	for _, entityID := range []string{p.pkp, p.nonPKP} {
		if _, lines, err := p.trf.Get(ctx, entityID, got.Transfer.ID); err != nil || len(lines) != 1 {
			t.Errorf("entity %s cannot read its own transfer: %v", entityID, err)
		}
	}

	outsider, err := p.q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "OTHER", Name: "PT Lain", IsPkp: 0,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("outsider: %v", err)
	}
	if _, _, err := p.trf.Get(ctx, outsider.ID, got.Transfer.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("a company not party to the transfer could read it: %v", err)
	}
}

// Stock cannot cross to where it already is.
func TestATransferToItselfIsRefused(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 0, false)

	if _, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.pkp, TransferDate: "2026-10-05",
		Lines: []service.TransferLineInput{{ProductID: p.gloves, Qty: 1}},
	}); !errors.Is(err, service.ErrSameEntity) {
		t.Fatalf("got %v, want ErrSameEntity", err)
	}
}

// R12.6, across the company boundary: a stock-changing event has to land in the
// right owner's margin, or R2.4 — the figure the family settles money on —
// breaks.
//
// The goods are Budi's in one company and Budi's in the other, so when the
// receiving company sells them the margin is Budi's, computed against the cost
// that actually crossed. Nobody decided that at sale time; it fell out of the
// owner attribution being carried across (INV-8).
func TestMarginFollowsTheGoodsAcrossTheBoundary(t *testing.T) {
	t.Parallel()

	p, ctx := newPair(t)
	// The PKP company buys Budi's gloves with a faktur: Rp 100.000 for ten.
	p.stockInto(ctx, t, p.pkp, p.gloves, 10, 10_000, 11_000, true)

	// All ten cross to the non-PKP company, with PPN on the delivery it cannot
	// credit. Rp 111.000 of cost arrives.
	if _, err := p.trf.Create(ctx, p.actor(p.pkp), service.TransferInput{
		ToEntityID: p.nonPKP, TransferDate: "2026-10-05", FakturIssued: true,
		FakturNo: "010.000-26.00000009",
		Lines:    []service.TransferLineInput{{ProductID: p.gloves, Qty: 10, PPNIDR: 11_000}},
	}); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	// The receiving company sells them.
	sellerActor := p.actor(p.nonPKP)
	if _, err := p.sales.OpenSession(ctx, sellerActor, service.OpenSessionInput{
		OpeningFloatIDR: 0, BusinessDate: "2026-10-10",
	}); err != nil {
		t.Fatalf("open till: %v", err)
	}
	price := money.IDR(20_000)
	sellerActor.ClientRequestID = store.NewID()
	if _, err := p.sales.Ring(ctx, sellerActor, service.SaleInput{
		SaleDate: "2026-10-10",
		Lines:    []service.SaleLineInput{{ProductID: p.gloves, Qty: 10, UnitPriceIDR: &price}},
	}); err != nil {
		t.Fatalf("sale: %v", err)
	}

	report, err := p.mrg.Report(ctx, p.nonPKP, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("margin: %v", err)
	}
	if len(report.Owners) != 1 {
		t.Fatalf("got %d owner lines, want Budi alone", len(report.Owners))
	}
	budi := report.Owners[0]
	if budi.OwnerName != "Budi" {
		t.Fatalf("the margin landed with %s, not the owner whose goods crossed", budi.OwnerName)
	}
	// Rp 200.000 sold against the Rp 111.000 that actually crossed — the
	// transfer's PPN included, because the receiver can never credit it.
	if budi.Revenue != 200_000 || budi.COGS != 111_000 || budi.Margin() != 89_000 {
		t.Errorf("Budi = %s revenue / %s COGS / %s margin, want 200.000 / 111.000 / 89.000",
			budi.Revenue, budi.COGS, budi.Margin())
	}

	// And the drill-down names the transfer as where the stock came from, so
	// "why did this cost Rp 111.000" is answerable on screen (SPEC §4.2).
	layers := budi.Sales[0].Products[0].Layers
	if len(layers) != 1 || layers[0].Source != "TRANSFER_IN" {
		t.Errorf("the drill-down does not show the stock arriving by transfer: %+v", layers)
	}
	if layers[0].FakturReceived {
		t.Error("a non-PKP company's layer claims creditable PPN")
	}
}
