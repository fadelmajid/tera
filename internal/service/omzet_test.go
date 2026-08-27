package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/omzet"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

func newOmzet(w world) *service.Omzet {
	now := func() time.Time { return fixedNow }
	return service.NewOmzet(w.db, service.NewAuditor(now), now)
}

// sellOn rings one sale on a given business date and returns it.
func (w world) sellOn(ctx context.Context, t *testing.T, day string, price money.IDR, qty int64) service.SaleResult {
	t.Helper()

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	got, err := w.sales.Ring(ctx, actor, service.SaleInput{
		SaleDate: day,
		Lines:    []service.SaleLineInput{{ProductID: w.gloves, Qty: qty, UnitPriceIDR: &price}},
	})
	if err != nil {
		t.Fatalf("ring on %s: %v", day, err)
	}
	return got
}

// stockedFor puts enough on Budi's shelf for a whole year of selling.
func (w world) stockedFor(ctx context.Context, t *testing.T, qty int64) {
	t.Helper()

	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.budi,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-01-01", Source: "PURCHASE",
		QtyIn: qty, CostTotalIdr: qty * 1_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("layer: %v", err)
	}
	w.till(ctx, t)
}

// TestASaleReachesTheOmzetClockInItsOwnTransaction is TASKS 7.4.
//
// Turnover that reached the books without reaching the clock would leave the
// business measuring itself against Rp 4,8 miliar on incomplete figures, and
// the error surfaces a year later as a registration deadline that was wrong all
// along.
func TestASaleReachesTheOmzetClockInItsOwnTransaction(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 100)
	om := newOmzet(w)

	sold := w.sellOn(ctx, t, "2026-10-15", 111_000, 2)

	rows, err := om.Ledger(ctx, w.entityID, 2026)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d ledger rows, want 1", len(rows))
	}
	row := rows[0]

	if row.EventType != string(omzet.Sale) || row.EffectiveDate != "2026-10-15" {
		t.Errorf("row is %s on %s", row.EventType, row.EffectiveDate)
	}
	// Net of PPN by default (SPEC §5.4): Rp 222.000 taken is Rp 200.000 of
	// turnover and Rp 22.000 collected for the state.
	if row.SignedAmountIdr != 200_000 {
		t.Errorf("recorded %s, want the DPP rather than the takings",
			money.IDR(row.SignedAmountIdr))
	}
	if row.SourceTxnID == nil || *row.SourceTxnID != sold.Sale.ID {
		t.Error("the row does not name the sale behind it")
	}
	if row.BookYear != 2026 {
		t.Errorf("book year = %d", row.BookYear)
	}
}

// TestTheOmzetLedgerIsAppendOnly. A crossing is a legal event with a date
// attached, and a ledger that can be edited cannot answer when it happened.
func TestTheOmzetLedgerIsAppendOnly(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 10)
	w.sellOn(ctx, t, "2026-10-15", 111_000, 1)

	rows, err := newOmzet(w).Ledger(ctx, w.entityID, 2026)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ledger: %v (%d rows)", err, len(rows))
	}

	if _, err := w.db.ExecContext(ctx,
		`UPDATE omzet_ledger SET signed_amount_idr = 1 WHERE id = ?`, rows[0].ID); err == nil {
		t.Error("a ledger row was edited")
	}
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM omzet_ledger WHERE id = ?`, rows[0].ID); err == nil {
		t.Error("a ledger row was deleted")
	}
}

// TestVoidingADecemberSaleInJanuaryComesOffDecember is TASKS 7.6 and SPEC §5.4,
// end to end through the real sale and void paths.
//
// The threshold resets between the two book years. Booking the reversal on the
// day somebody noticed would leave the closed year overstated and open the new
// one already short — two wrong answers from one mistake.
func TestVoidingADecemberSaleInJanuaryComesOffDecember(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 100)
	om := newOmzet(w)

	december := w.sellOn(ctx, t, "2026-12-28", 111_000, 4)
	w.sellOn(ctx, t, "2027-01-05", 111_000, 1)

	// Noticed and voided in January. The till is still the one that was open,
	// which is what makes a void available at all (R12.3).
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.Void(ctx, actor, december.Sale.ID, "Salah input"); err != nil {
		t.Fatalf("void: %v", err)
	}

	rows, err := om.Ledger(ctx, w.entityID, 2026)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows in book year 2026, want the sale and its reversal", len(rows))
	}

	var reversal *gen.ListOmzetForBookYearRow
	for i := range rows {
		if rows[i].EventType == string(omzet.Void) {
			reversal = &rows[i]
		}
	}
	if reversal == nil {
		t.Fatal("the void wrote no compensating row")
	}
	// The original sale's date, not today's.
	if reversal.EffectiveDate != "2026-12-28" {
		t.Errorf("the reversal is dated %s, want the original sale's 2026-12-28",
			reversal.EffectiveDate)
	}
	if reversal.BookYear != 2026 {
		t.Errorf("the reversal is stamped book year %d, want 2026", reversal.BookYear)
	}
	if reversal.SignedAmountIdr != -400_000 {
		t.Errorf("the reversal is %s, want exactly what the sale contributed",
			money.IDR(reversal.SignedAmountIdr))
	}

	// December nets to nothing; January keeps its own sale.
	closing, err := om.Position(ctx, w.entityID, "2026", "2027-01-31")
	if err != nil {
		t.Fatalf("position 2026: %v", err)
	}
	if !closing.Position.Cumulative.IsZero() {
		t.Errorf("2026 closed at %s, want zero after the void", closing.Position.Cumulative)
	}

	opening, err := om.Position(ctx, w.entityID, "2027", "2027-01-31")
	if err != nil {
		t.Fatalf("position 2027: %v", err)
	}
	if opening.Position.Cumulative != 100_000 {
		t.Errorf("2027 opened at %s, want only its own sale — the December void "+
			"must not land here", opening.Position.Cumulative)
	}
}

// TestAReturnComesOffTheDayTheGoodsCameBack, unlike a void.
//
// A return is not a void. The turnover happened and is being reduced now, which
// is the same distinction D-012 draws for margin — so the reversal carries the
// return's date, and a year that has already crossed stays crossed.
func TestAReturnComesOffTheDayTheGoodsCameBack(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 100)
	om := newOmzet(w)

	sold := w.sellOn(ctx, t, "2026-10-15", 111_000, 4)
	_, lines, _, err := w.sales.GetSale(ctx, w.entityID, sold.Sale.ID)
	if err != nil {
		t.Fatalf("get sale: %v", err)
	}

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.sales.CreateReturn(ctx, actor, service.SaleReturnInput{
		SaleID: sold.Sale.ID, ReturnDate: "2026-11-03", Reason: "Ukuran salah",
		Lines: []service.SaleReturnLineInput{{SaleLineID: lines[0].ID, Qty: 1}},
	}); err != nil {
		t.Fatalf("return: %v", err)
	}

	rows, err := om.Ledger(ctx, w.entityID, 2026)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}

	var reversal *gen.ListOmzetForBookYearRow
	for i := range rows {
		if rows[i].EventType == string(omzet.Return) {
			reversal = &rows[i]
		}
	}
	if reversal == nil {
		t.Fatal("the return wrote no compensating row")
	}
	if reversal.EffectiveDate != "2026-11-03" {
		t.Errorf("the reversal is dated %s, want the day the goods came back",
			reversal.EffectiveDate)
	}
	// One of four boxes back, so a quarter of what the sale contributed:
	// Rp 400.000 recorded, Rp 100.000 off.
	if reversal.SignedAmountIdr != -100_000 {
		t.Errorf("the reversal is %s, want a quarter of the sale",
			money.IDR(reversal.SignedAmountIdr))
	}

	got, err := om.Position(ctx, w.entityID, "2026", "2026-12-31")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if got.Position.Cumulative != 300_000 {
		t.Errorf("cumulative = %s, want Rp 300.000", got.Position.Cumulative)
	}
}

// TestTheClockCountsPerBookYearAndResets is SPEC §5.1 through the real paths.
func TestTheClockCountsPerBookYearAndResets(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 100)
	om := newOmzet(w)

	w.sellOn(ctx, t, "2026-11-01", 111_000, 3)
	w.sellOn(ctx, t, "2026-12-31", 111_000, 2)
	w.sellOn(ctx, t, "2027-01-01", 111_000, 1)

	closing, err := om.Position(ctx, w.entityID, "2026", "2026-12-31")
	if err != nil {
		t.Fatalf("position 2026: %v", err)
	}
	if closing.Position.Cumulative != 500_000 {
		t.Errorf("2026 = %s, want Rp 500.000", closing.Position.Cumulative)
	}

	opening, err := om.Position(ctx, w.entityID, "2027", "2027-01-31")
	if err != nil {
		t.Fatalf("position 2027: %v", err)
	}
	// Reset annually. That is the half of the rule a rolling-twelve-month
	// reading loses.
	if opening.Position.Cumulative != 100_000 {
		t.Errorf("2027 = %s, want only its own Rp 100.000", opening.Position.Cumulative)
	}
	// And the estimate does span the boundary, which is exactly why it is not
	// the legal figure.
	if opening.Position.Trailing12 != 600_000 {
		t.Errorf("trailing twelve = %s, want the whole Rp 600.000 across both years",
			opening.Position.Trailing12)
	}

	// The threshold travelled with the report, so the screen can cite the
	// regulation rather than assert a number.
	if closing.Position.Threshold.AmountIDR != 4_800_000_000 {
		t.Errorf("threshold = %s", closing.Position.Threshold.AmountIDR)
	}
	if closing.Position.Threshold.LegalRef == "" {
		t.Error("the report carries no legal reference for its threshold")
	}
	if len(closing.BookYears) != 2 {
		t.Errorf("book years with turnover = %v, want 2026 and 2027", closing.BookYears)
	}
}

// TestTheBaseIsConfigurableAndAudited is SPEC §5.4's fourth edge case.
func TestTheBaseIsConfigurableAndAudited(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stockedFor(ctx, t, 100)
	om := newOmzet(w)

	// Net by default: the PPN was collected for the state.
	w.sellOn(ctx, t, "2026-10-15", 111_000, 1)
	net, err := om.Position(ctx, w.entityID, "2026", "2026-12-31")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if net.Position.Cumulative != 100_000 || net.Base != omzet.NetOfVAT {
		t.Errorf("default base %s recorded %s", net.Base, net.Position.Cumulative)
	}

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := om.SetBase(ctx, actor, "GROSS", "Konsultan pajak bilang bruto"); err != nil {
		t.Fatalf("set base: %v", err)
	}

	// The change applies from here on. Rows already written are not rewritten:
	// the ledger is append-only, and a figure that silently restated itself
	// would be worse than one that changed on a stated date.
	w.sellOn(ctx, t, "2026-10-16", 111_000, 1)
	gross, err := om.Position(ctx, w.entityID, "2026", "2026-12-31")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if gross.Base != omzet.Gross {
		t.Errorf("base = %s, want GROSS", gross.Base)
	}
	if gross.Position.Cumulative != 211_000 {
		t.Errorf("cumulative = %s, want Rp 100.000 net plus Rp 111.000 gross",
			gross.Position.Cumulative)
	}

	if _, err := om.SetBase(ctx, actor, "BRUTO", ""); !errors.Is(err, service.ErrValidation) {
		t.Errorf("an unrecognised base was accepted: %v", err)
	}
}

// TestACompanyWithNoThresholdIsRefused is INV-4 reaching the clock.
//
// A threshold is a legal figure. An omzet clock running against a number nobody
// configured is a number the owner plans a year around and cannot check, so the
// report refuses rather than falling back to a constant.
func TestACompanyWithNoThresholdIsRefused(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	if _, err := w.db.ExecContext(ctx, `DELETE FROM omzet_threshold WHERE entity_id = ?`, w.entityID); err != nil {
		t.Fatalf("clear thresholds: %v", err)
	}

	_, err := newOmzet(w).Position(ctx, w.entityID, "2026", "2026-12-31")
	if !errors.Is(err, service.ErrOmzetConfig) {
		t.Fatalf("want ErrOmzetConfig, got %v", err)
	}
	if !errors.Is(err, omzet.ErrNoThreshold) {
		t.Errorf("the refusal does not carry the domain's reason: %v", err)
	}
}

// TestCreatingACompanySeedsItsOmzetThreshold is TASKS 7.1's other half: a
// company arrives able to measure itself.
func TestCreatingACompanySeedsItsOmzetThreshold(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	now := func() time.Time { return fixedNow }
	master := service.NewMasterData(w.db, service.NewAuditor(now), now)
	om := newOmzet(w)

	for _, isPKP := range []bool{true, false} {
		code := "NONPKP2"
		if isPKP {
			code = "PKP2"
		}

		actor := w.actor
		actor.ClientRequestID = store.NewID()
		entity, err := master.CreateEntity(ctx, actor, service.EntityInput{
			Code: code, Name: "PT " + code, IsPKP: isPKP,
			Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
		})
		if err != nil {
			t.Fatalf("create entity: %v", err)
		}

		// Both are tracked (SPEC §5.3): the owner's question is how each
		// company is doing against the line.
		rows, err := om.ListThresholds(ctx, entity.ID)
		if err != nil {
			t.Fatalf("thresholds: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s got %d thresholds, want 1", code, len(rows))
		}
		if rows[0].AmountIdr != 4_800_000_000 || rows[0].LegalRef == "" {
			t.Errorf("%s seeded %d with ref %q", code, rows[0].AmountIdr, rows[0].LegalRef)
		}
	}
}
