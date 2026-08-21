package service_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// TestOpnameVarianceNeverCrossesOwners is INV-8 at the opname screen.
//
// Budi is short three boxes and Sari has forty of the same item on the same
// shelf. Posting the shortfall must draw only from Budi's layers. Covering it
// from Sari's would move real money between two family members who settle
// monthly on these figures (R2.4) -- and it would do so inside an operation
// labelled "correction", which is the worst possible place to hide it.
func TestOpnameVarianceNeverCrossesOwners(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	// Budi holds 10 gloves; Sari holds 40 of the same product.
	w.buy(ctx, t, 10, 10_000, 0, false)
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.sari,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 40, CostTotalIdr: 400_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sari layer: %v", err)
	}

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{CountDate: "2026-10-15"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Budi's shelf counts 7, not 10.
	line, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 7, ReasonCode: "HILANG",
	})
	if err != nil {
		t.Fatalf("save line: %v", err)
	}
	if line.SystemQty != 10 || line.Variance != -3 {
		t.Fatalf("system %d variance %d, want 10 and -3", line.SystemQty, line.Variance)
	}

	if _, err := w.opname.Post(ctx, w.actor, op.ID, "Opname bulanan Oktober"); err != nil {
		t.Fatalf("post: %v", err)
	}

	// Budi is down to 7. Sari is untouched at 40.
	onHand, err := w.opname.CountSheet(ctx, w.entityID)
	if err != nil {
		t.Fatalf("count sheet: %v", err)
	}
	for _, row := range onHand {
		switch {
		case row.OwnerID != nil && *row.OwnerID == w.budi && row.ProductID == w.gloves:
			if row.QtyOnHand != 7 {
				t.Errorf("Budi holds %d gloves, want 7", row.QtyOnHand)
			}
		case row.OwnerID != nil && *row.OwnerID == w.sari && row.ProductID == w.gloves:
			if row.QtyOnHand != 40 {
				t.Errorf("Sari holds %d gloves, want 40 -- Budi's shortfall came out of her stock", row.QtyOnHand)
			}
		}
	}
}

// A shortfall bigger than that owner actually holds is refused rather than
// covered from elsewhere or driven negative.
func TestOpnameShortfallBeyondTheOwnersStockIsRefused(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 5, 10_000, 0, false) // Budi: 5

	// Sari has plenty, and none of it is available to cover Budi.
	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.sari,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 100, CostTotalIdr: 1_000_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sari layer: %v", err)
	}

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// The count sheet says 5; someone types 0 and then someone else sells 5
	// before posting. The snapshot still says 5, so posting tries to draw 5
	// that are no longer there.
	if _, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 0, ReasonCode: "HILANG",
	}); err != nil {
		t.Fatalf("save line: %v", err)
	}
	layers, err := w.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: w.entityID, ProductID: w.gloves,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	for _, l := range layers {
		if l.OwnerID != nil && *l.OwnerID == w.budi {
			if _, err := w.q.RecordConsumption(ctx, gen.RecordConsumptionParams{
				ID: store.NewID(), LayerID: l.ID, MovementID: store.NewID(), MovementType: "SALE",
				QtyOut: l.QtyRemaining, CostIdr: l.CostTotalIdr,
				OccurredAt: fixedNow.Unix(), BusinessDate: "2026-10-14", CreatedAt: fixedNow.Unix(),
			}); err != nil {
				t.Fatalf("sale: %v", err)
			}
		}
	}

	_, err = w.opname.Post(ctx, w.actor, op.ID, "Opname")
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("got %v, want a refusal rather than a negative layer", err)
	}

	// And nothing was half-applied: the count is still a draft.
	header, _, err := w.opname.Lines(ctx, w.entityID, op.ID)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	if header.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT -- a failed posting must not leave the books half-counted", header.Status)
	}
}

// A surplus becomes a layer, and a layer has to say what it cost (SPEC §3.2).
func TestOpnameSurplusCreatesAPricedLayer(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 12_000, 0, false) // Budi: 10 at Rp 12.000 each

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// 13 on the shelf. The unit cost defaults from the most recent layer,
	// because found stock is nearly always a miscounted recent delivery.
	line, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 13, ReasonCode: "SALAH_CATAT",
	})
	if err != nil {
		t.Fatalf("save line: %v", err)
	}
	if line.Variance != 3 {
		t.Fatalf("variance = %d, want 3", line.Variance)
	}
	if line.UnitCostIdr == nil || *line.UnitCostIdr != 12_000 {
		t.Fatalf("unit cost = %v, want 12000 defaulted from the latest layer", line.UnitCostIdr)
	}

	got, err := w.opname.Post(ctx, w.actor, op.ID, "Opname bulanan")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if got.SurplusUnits != 3 || got.CostDelta != 36_000 {
		t.Errorf("surplus %d cost %s, want 3 and %s", got.SurplusUnits, got.CostDelta, money.IDR(36_000))
	}

	// The found stock carries no faktur of its own. Whatever paperwork the
	// original delivery had belongs to that delivery's layer; inventing a
	// credit here would claim the same input PPN twice (INV-9).
	layers, err := w.q.ListLayersBySourceDoc(ctx, gen.ListLayersBySourceDocParams{
		Source: "ADJUSTMENT", SourceDocID: &op.ID,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("got %d adjustment layers, want 1", len(layers))
	}
	if layers[0].FakturReceived != 0 || layers[0].PpnPaidIdr != 0 {
		t.Errorf("found stock claimed a faktur: %+v", layers[0])
	}
	if layers[0].OwnerID == nil || *layers[0].OwnerID != w.budi {
		t.Errorf("the surplus landed with %v, want Budi -- the shelf that was counted", layers[0].OwnerID)
	}
}

// R12.5: every adjustment carries a reason code and lands in the audit log.
func TestOpnameVarianceRequiresAReasonCode(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 8,
	}); !errors.Is(err, service.ErrReasonRequired) {
		t.Errorf("accepted an unexplained variance: %v", err)
	}

	// A flat line needs no explanation, because nothing is being adjusted.
	if _, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 10,
	}); err != nil {
		t.Errorf("a matching count was refused: %v", err)
	}

	// Posting itself needs a reason too, and writes an ADJUST row (R7.2).
	if _, err := w.opname.Post(ctx, w.actor, op.ID, ""); !errors.Is(err, service.ErrReasonRequired) {
		t.Errorf("posted without a reason: %v", err)
	}
	if _, err := w.opname.Post(ctx, w.actor, op.ID, "Opname rutin"); err != nil {
		t.Fatalf("post: %v", err)
	}

	trail, err := w.q.ListAuditForRecord(ctx, gen.ListAuditForRecordParams{
		RecordType: "stock_opname", RecordID: op.ID,
	})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	var adjust int
	for _, row := range trail {
		if row.Action == "ADJUST" {
			adjust++
			if row.Reason == nil || *row.Reason != "Opname rutin" {
				t.Errorf("ADJUST row carries reason %v, want the posting reason", row.Reason)
			}
			if row.BeforeJson == nil || row.AfterJson == nil {
				t.Error("ADJUST row is missing a before/after snapshot (INV-10)")
			}
		}
	}
	if adjust != 1 {
		t.Errorf("got %d ADJUST rows, want 1", adjust)
	}
}

// A posted count is final (INV-2). Correcting it means counting again, which
// leaves both counts on the record.
func TestPostedOpnameIsFinal(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 9, ReasonCode: "RUSAK",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := w.opname.Post(ctx, w.actor, op.ID, "Opname"); err != nil {
		t.Fatalf("post: %v", err)
	}

	if _, err := w.opname.Post(ctx, w.actor, op.ID, "lagi"); !errors.Is(err, service.ErrAlreadyPosted) {
		t.Errorf("posted twice: %v", err)
	}
	if _, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 10, ReasonCode: "SALAH_CATAT",
	}); !errors.Is(err, service.ErrAlreadyPosted) {
		t.Errorf("edited a posted count: %v", err)
	}
}

// system_qty is snapshotted at counting time, not re-read at posting time.
// Re-reading it would absorb any sale rung in between and report a variance of
// zero -- the one thing a stock count must never do.
func TestOpnameSnapshotsTheSystemQuantityWhenCounted(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 0, false)

	op, err := w.opname.Start(ctx, w.actor, service.StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	line, err := w.opname.SaveLine(ctx, w.actor, op.ID, service.LineInput{
		ProductID: w.gloves, OwnerID: w.budi, CountedQty: 10,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if line.SystemQty != 10 || line.Variance != 0 {
		t.Fatalf("system %d variance %d, want 10 and 0", line.SystemQty, line.Variance)
	}

	// Two boxes sell between the count and the posting.
	layers, err := w.q.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
		EntityID: w.entityID, ProductID: w.gloves,
	})
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if _, err := w.q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: layers[0].ID, MovementID: store.NewID(), MovementType: "SALE",
		QtyOut: 2, CostIdr: 20_000, OccurredAt: fixedNow.Unix(),
		BusinessDate: "2026-10-15", CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sale: %v", err)
	}

	_, lines, err := w.opname.Lines(ctx, w.entityID, op.ID)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	if lines[0].SystemQty != 10 {
		t.Errorf("system_qty moved to %d after a sale; the count sheet must record what was on the books when it was counted",
			lines[0].SystemQty)
	}
}
