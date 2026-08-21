package store_test

import (
	"context"
	"testing"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// stockFixtures gives every test in this file a company, a product, and two
// family members to attribute stock to.
type stockFixtures struct {
	entity  string
	product string
	budi    string
	sari    string
}

func newStockFixtures(ctx context.Context, t *testing.T, q *gen.Queries) stockFixtures {
	t.Helper()

	e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "PKP", Name: "PT Sehat Sentosa", IsPkp: 1,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}

	owner := func(code, name string) string {
		t.Helper()
		o, err := q.CreateOwner(ctx, gen.CreateOwnerParams{ID: store.NewID(), Code: code, Name: name})
		if err != nil {
			t.Fatalf("create owner %s: %v", code, err)
		}
		return o.ID
	}
	budi, sari := owner("BUDI", "Budi"), owner("SARI", "Sari")

	p, err := q.CreateProduct(ctx, gen.CreateProductParams{
		ID: store.NewID(), Code: "P-GLOVE", Name: "Sarung Tangan Steril", Unit: "box",
		OwnerID: &budi, SalePriceIdr: 150_000,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	return stockFixtures{entity: e.ID, product: p.ID, budi: budi, sari: sari}
}

// layer inserts a stock layer and returns its id.
func (f stockFixtures) layer(ctx context.Context, t *testing.T, q *gen.Queries,
	owner *string, acquiredAt int64, qtyIn, cost int64, fakturReceived int64,
) string {
	t.Helper()

	l, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: f.entity, ProductID: f.product, OwnerID: owner,
		AcquiredAt: acquiredAt, BusinessDate: "2026-08-01", Source: "PURCHASE",
		QtyIn: qtyIn, CostTotalIdr: cost, FakturReceived: fakturReceived,
	})
	if err != nil {
		t.Fatalf("create layer: %v", err)
	}
	return l.ID
}

func (f stockFixtures) draw(ctx context.Context, t *testing.T, q *gen.Queries,
	layerID, movementID string, qtyOut, cost int64,
) string {
	t.Helper()

	c, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: layerID, MovementID: movementID, MovementType: "SALE",
		QtyOut: qtyOut, CostIdr: cost, OccurredAt: 1_800_000_000, BusinessDate: "2026-08-15",
	})
	if err != nil {
		t.Fatalf("record consumption: %v", err)
	}
	return c.ID
}

// TestStockTablesAreAppendOnly is INV-7 at the storage layer.
//
// The trail of which layers a sale drew from, and what each cost, is what the
// margin report decomposes into months later when a family member asks why
// theirs is lower. An UPDATE that silently moves a layer's cost, or a DELETE
// that removes a draw, destroys the only record of what actually happened.
// Corrections are compensating rows (INV-2, D-010), so nothing legitimate needs
// either statement.
func TestStockTablesAreAppendOnly(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)
	drawID := f.draw(ctx, t, q, layerID, "sale-1", 4, 40_000)

	// A layer nothing has drawn from yet. Deleting the one above is already
	// blocked by ON DELETE RESTRICT from its consumption, which would mask
	// whether the trigger works at all - so the delete case uses this one.
	untouched := f.layer(ctx, t, q, &f.sari, 1_800_000_500, 5, 50_000, 0)

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"decrementing a layer in place",
			`UPDATE stock_layer SET qty_in = qty_in - 1 WHERE id = ?`, []any{layerID},
		},
		{
			"quietly restating a layer's cost",
			`UPDATE stock_layer SET cost_total_idr = 1 WHERE id = ?`, []any{layerID},
		},
		{
			"flipping faktur_received after the fact",
			`UPDATE stock_layer SET faktur_received = 0 WHERE id = ?`, []any{layerID},
		},
		{
			"deleting a layer nothing references",
			`DELETE FROM stock_layer WHERE id = ?`, []any{untouched},
		},
		{
			"editing what a draw took",
			`UPDATE stock_consumption SET qty_out = 1 WHERE id = ?`, []any{drawID},
		},
		{
			"editing what a draw cost",
			`UPDATE stock_consumption SET cost_idr = 0 WHERE id = ?`, []any{drawID},
		},
		{
			"deleting a draw instead of reversing it",
			`DELETE FROM stock_consumption WHERE id = ?`, []any{drawID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, tc.sql, tc.args...); err == nil {
				t.Fatalf("the database allowed %s; INV-7 is not enforced", tc.name)
			}
		})
	}

	// And the rows are untouched after all that.
	l, err := q.GetStockLayer(ctx, layerID)
	if err != nil {
		t.Fatalf("get layer: %v", err)
	}
	if l.QtyIn != 10 || l.CostTotalIdr != 100_000 || l.FakturReceived != 1 {
		t.Errorf("layer was modified despite the triggers: %+v", l)
	}
}

// TestRemainingQuantityIsDerivedNotStored is SPEC 3.1.
//
// There is no balance column on stock_layer, deliberately. A mutable balance is
// exactly what INV-7 exists to prevent, so remaining is qty_in - sum of qty_out
// computed on read.
func TestRemainingQuantityIsDerivedNotStored(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	// No column on stock_layer may hold a running balance.
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('stock_layer')`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch name {
		case "qty_remaining", "qty_out", "qty_consumed", "balance", "qty_on_hand":
			t.Errorf("stock_layer has a %q column; remaining quantity must stay derived (INV-7, SPEC 3.1)", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)

	assertBalance := func(t *testing.T, wantConsumed, wantRemaining int64) {
		t.Helper()
		b, err := q.GetLayerBalance(ctx, layerID)
		if err != nil {
			t.Fatalf("get balance: %v", err)
		}
		if b.QtyConsumed != wantConsumed || b.QtyRemaining != wantRemaining {
			t.Errorf("consumed %d remaining %d, want %d and %d",
				b.QtyConsumed, b.QtyRemaining, wantConsumed, wantRemaining)
		}
	}

	assertBalance(t, 0, 10)

	drawID := f.draw(ctx, t, q, layerID, "sale-1", 4, 40_000)
	assertBalance(t, 4, 6)

	f.draw(ctx, t, q, layerID, "sale-2", 3, 30_000)
	assertBalance(t, 7, 3)

	// D-010: a return appends a negative draw naming the one it reverses. The
	// derivation needs no special case for it - the sum simply falls.
	reverses := drawID
	if _, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: layerID, MovementID: "return-1", MovementType: "SALES_RETURN",
		QtyOut: -2, CostIdr: -20_000, OccurredAt: 1_800_100_000, BusinessDate: "2026-09-02",
		ReversesID: &reverses,
	}); err != nil {
		t.Fatalf("record reversal: %v", err)
	}
	assertBalance(t, 5, 5)
}

// A row that puts stock back without naming the draw it reverses would be a
// stock increase disguised as a correction (D-010).
func TestConsumptionSignsMustCohere(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)
	drawID := f.draw(ctx, t, q, layerID, "sale-1", 4, 40_000)

	insert := func(qtyOut, cost int64, reverses *string) error {
		_, err := db.ExecContext(ctx, `
            INSERT INTO stock_consumption (id, layer_id, movement_id, movement_type,
                qty_out, cost_idr, occurred_at, business_date, reverses_id, created_at)
            VALUES (?, ?, 'm-x', 'SALE', ?, ?, 0, '2026-08-15', ?, 0)`,
			store.NewID(), layerID, qtyOut, cost, reverses)
		return err
	}

	tests := []struct {
		name     string
		qtyOut   int64
		cost     int64
		reverses *string
		wantErr  bool
	}{
		{"an ordinary draw", 2, 20_000, nil, false},
		{"a free-goods draw costs nothing", 2, 0, nil, false},
		{"a reversal naming its draw", -2, -20_000, &drawID, false},
		{"stock appearing from nowhere", -2, -20_000, nil, true},
		{"a reversal that draws stock out", 2, 20_000, &drawID, true},
		{"a draw of nothing", 0, 0, nil, true},
		{"a draw with a negative cost", 2, -20_000, nil, true},
		{"a reversal with a positive cost", -2, 20_000, &drawID, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := insert(tc.qtyOut, tc.cost, tc.reverses)
			if tc.wantErr && err == nil {
				t.Errorf("accepted %s", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("rejected %s: %v", tc.name, err)
			}
		})
	}
}

// TestLayersForConsumptionReturnEveryOwnersStock is the contract
// internal/domain/fifo depends on.
//
// The query must NOT filter by owner. The domain does the scoping (INV-8), and
// it needs to see other owners' layers so an insufficient-stock error can say
// how much stock is on the shelf but not this owner's. A WHERE owner_id = ?
// here would silently turn that into a bare "out of stock" in front of a full
// shelf.
func TestLayersForConsumptionReturnEveryOwnersStock(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	f.layer(ctx, t, q, &f.sari, 1_800_000_300, 100, 1_000_000, 0)
	f.layer(ctx, t, q, &f.budi, 1_800_000_100, 3, 30_000, 1)
	f.layer(ctx, t, q, nil, 1_800_000_200, 50, 500_000, 1) // company bucket

	got, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: f.entity, ProductID: f.product,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d layers, want all 3 regardless of owner", len(got))
	}

	// Oldest first (SPEC 3.3).
	for i := 1; i < len(got); i++ {
		if got[i-1].AcquiredAt > got[i].AcquiredAt {
			t.Errorf("layers are not oldest-first: %d then %d", got[i-1].AcquiredAt, got[i].AcquiredAt)
		}
	}
	if got[0].QtyIn != 3 || got[2].QtyIn != 100 {
		t.Errorf("ordering is wrong: %d then %d then %d", got[0].QtyIn, got[1].QtyIn, got[2].QtyIn)
	}
	// NULL owner survives the round trip as the company bucket (R2.2).
	if got[1].OwnerID != nil {
		t.Errorf("the company-bucket layer came back owned by %v", *got[1].OwnerID)
	}

	// An exhausted layer drops out; it contributes nothing either way.
	f.draw(ctx, t, q, got[0].ID, "sale-1", 3, 30_000)
	after, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: f.entity, ProductID: f.product,
	})
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("got %d layers after exhausting one, want 2", len(after))
	}
}

// FIFO ties on acquired_at break by id, and ids are UUIDv7 (D-003) so that
// resolves to insertion order. One purchase with several lines lands in the
// same second routinely.
func TestFifoTieBreakFollowsInsertionOrder(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	const sameInstant = 1_800_000_000
	var inserted []string
	for range 5 {
		inserted = append(inserted, f.layer(ctx, t, q, &f.budi, sameInstant, 1, 1_000, 1))
	}

	got, err := q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: f.entity, ProductID: f.product,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i, l := range got {
		if l.ID != inserted[i] {
			t.Fatalf("position %d holds %s, want %s -- v7 ids should sort into insertion order", i, l.ID, inserted[i])
		}
	}
}

func TestStockLayerConstraints(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	tests := []struct {
		name   string
		mutate func(*gen.CreateStockLayerParams)
	}{
		{"a layer that brings in nothing", func(p *gen.CreateStockLayerParams) { p.QtyIn = 0 }},
		{"a layer that brings in a negative", func(p *gen.CreateStockLayerParams) { p.QtyIn = -1 }},
		{"a negative cost", func(p *gen.CreateStockLayerParams) { p.CostTotalIdr = -1 }},
		{"a negative PPN", func(p *gen.CreateStockLayerParams) { p.PpnPaidIdr = -1 }},
		{"faktur_received as a free integer", func(p *gen.CreateStockLayerParams) { p.FakturReceived = 2 }},
		{"an unknown source", func(p *gen.CreateStockLayerParams) { p.Source = "MAGIC" }},
		{"a business_date that is not a date", func(p *gen.CreateStockLayerParams) { p.BusinessDate = "01/08/2026" }},
		{"an owner who does not exist", func(p *gen.CreateStockLayerParams) { id := store.NewID(); p.OwnerID = &id }},
		{"a product that does not exist", func(p *gen.CreateStockLayerParams) { p.ProductID = store.NewID() }},
		{"an entity that does not exist", func(p *gen.CreateStockLayerParams) { p.EntityID = store.NewID() }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := gen.CreateStockLayerParams{
				ID: store.NewID(), EntityID: f.entity, ProductID: f.product, OwnerID: &f.budi,
				AcquiredAt: 1_800_000_000, BusinessDate: "2026-08-01", Source: "PURCHASE",
				QtyIn: 10, CostTotalIdr: 100_000, FakturReceived: 1, PpnPaidIdr: 11_000,
			}
			tc.mutate(&p)

			if _, err := q.CreateStockLayer(ctx, p); err == nil {
				t.Errorf("accepted %s", tc.name)
			}
		})
	}

	// INV-1 reaches these columns too: STRICT refuses a fractional rupiah.
	_, err := db.ExecContext(ctx, `
        INSERT INTO stock_layer (id, entity_id, product_id, owner_id, acquired_at, business_date,
            source, qty_in, cost_total_idr, faktur_received, ppn_paid_idr, created_at)
        VALUES (?, ?, ?, ?, 0, '2026-08-01', 'PURCHASE', 7, 14285.71, 1, 0, 0)`,
		store.NewID(), f.entity, f.product, f.budi)
	if err == nil {
		t.Error("stored a fractional rupiah in cost_total_idr; INV-1 is not enforced")
	}
}

// INV-9: whether the faktur arrived is what sets the layer's cost basis, and it
// has to survive on the layer to the audit months later.
func TestFakturStatusRoundTripsOnTheLayer(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	// Same supplier price, two purchases. Only the faktur differs, and so the
	// cost basis differs by the PPN (SPEC 3.2). See the domain test
	// TestSameSupplierPriceFakturVsNoFakturProduceDifferentLayerCosts.
	withFaktur := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)
	without := f.layer(ctx, t, q, &f.budi, 1_800_000_100, 10, 111_000, 0)

	a, err := q.GetStockLayer(ctx, withFaktur)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	b, err := q.GetStockLayer(ctx, without)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if a.FakturReceived != 1 || b.FakturReceived != 0 {
		t.Errorf("faktur status did not round trip: %d and %d", a.FakturReceived, b.FakturReceived)
	}
	if a.CostTotalIdr == b.CostTotalIdr {
		t.Error("the two layers carry the same cost; the faktur made no difference")
	}
	if diff := b.CostTotalIdr - a.CostTotalIdr; diff != 11_000 {
		t.Errorf("cost basis differs by %d, want 11000 (the PPN)", diff)
	}
}

// The drill-down in SPEC 4.2 walks a sale down to the layers it drew from and
// what each cost. Without the movement join there is nothing to walk.
func TestConsumptionsDrillDownFromAMovement(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	first := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 5, 50_000, 1)
	second := f.layer(ctx, t, q, &f.budi, 1_800_000_100, 5, 60_000, 0)

	f.draw(ctx, t, q, first, "sale-42", 5, 50_000)
	f.draw(ctx, t, q, second, "sale-42", 3, 36_000)
	f.draw(ctx, t, q, second, "sale-99", 1, 12_000) // a different sale

	got, err := q.ListConsumptionsForMovement(ctx, "sale-42")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("sale-42 drew from %d layers, want 2", len(got))
	}

	var cogs int64
	for _, c := range got {
		cogs += c.CostIdr
	}
	if cogs != 86_000 {
		t.Errorf("COGS = %d, want 86000", cogs)
	}
	// The faktur status that set each layer's basis comes back with the draw,
	// so the drill-down can show why one batch cost more than another.
	if got[0].LayerFakturReceived != 1 || got[1].LayerFakturReceived != 0 {
		t.Errorf("faktur status did not come through the drill-down: %+v", got)
	}
	if got[0].OwnerID == nil || *got[0].OwnerID != f.budi {
		t.Error("the drill-down lost the owner attribution")
	}
}

// D-010: returning four of three units sold is data entry to reject. SQL cannot
// see the running total from a row check, so the service reads this figure.
func TestReversalTotalIsQueryable(t *testing.T) {
	t.Parallel()

	_, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)
	drawID := f.draw(ctx, t, q, layerID, "sale-1", 3, 30_000)

	reversed, err := q.SumReversedAgainstConsumption(ctx, &drawID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if reversed != 0 {
		t.Errorf("nothing reversed yet, got %d", reversed)
	}

	for range 2 {
		if _, err := q.RecordConsumption(ctx, gen.RecordConsumptionParams{
			ID: store.NewID(), LayerID: layerID, MovementID: "return-1", MovementType: "SALES_RETURN",
			QtyOut: -1, CostIdr: -10_000, OccurredAt: 1_800_100_000, BusinessDate: "2026-09-02",
			ReversesID: &drawID,
		}); err != nil {
			t.Fatalf("reversal: %v", err)
		}
	}

	reversed, err = q.SumReversedAgainstConsumption(ctx, &drawID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if reversed != 2 {
		t.Errorf("reversed %d units, want 2", reversed)
	}
}

// D-010 was decided after SPEC 3.1 was written and supersedes it here: a sales
// return restores stock to the ORIGINAL layer, so a return never creates one.
// Allowing source = 'RETURN' would let someone reintroduce the exact bug D-010
// rules out - revenue reversed in full against a cost reversed at a different
// figure, inventing margin in the report the family settles on.
//
// The two return directions are spelled out for the same reason: a sales return
// puts stock back, a purchase return sends it to the supplier. One shared word
// is how one gets booked as the other.
func TestReturnIsNotALayerSourceAndTheTwoDirectionsAreDistinct(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	if _, err := q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: f.entity, ProductID: f.product, OwnerID: &f.budi,
		AcquiredAt: 1_800_000_000, BusinessDate: "2026-08-01", Source: "RETURN",
		QtyIn: 5, CostTotalIdr: 50_000,
	}); err == nil {
		t.Error("accepted a layer sourced from a RETURN; D-010 says returns restore to the original layer")
	}

	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, 10, 100_000, 1)
	drawID := f.draw(ctx, t, q, layerID, "sale-1", 4, 40_000)

	movement := func(kind string, qtyOut, cost int64, reverses *string) error {
		_, err := db.ExecContext(ctx, `
            INSERT INTO stock_consumption (id, layer_id, movement_id, movement_type,
                qty_out, cost_idr, occurred_at, business_date, reverses_id, created_at)
            VALUES (?, ?, 'm-x', ?, ?, ?, 0, '2026-09-01', ?, 0)`,
			store.NewID(), layerID, kind, qtyOut, cost, reverses)
		return err
	}

	// A sales return puts goods back, naming the draw it reverses.
	if err := movement("SALES_RETURN", -2, -20_000, &drawID); err != nil {
		t.Errorf("rejected a sales return: %v", err)
	}
	// A purchase return sends goods to the supplier: stock leaves.
	if err := movement("PURCHASE_RETURN", 2, 20_000, nil); err != nil {
		t.Errorf("rejected a purchase return: %v", err)
	}
	// The old catch-all is gone, so neither can be booked as the other by
	// reaching for a vaguer word.
	if err := movement("RETURN", 1, 10_000, nil); err == nil {
		t.Error("accepted the ambiguous RETURN movement type")
	}
}
