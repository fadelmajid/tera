package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/tax"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// stocked puts ten boxes of gloves on Budi's shelf and opens the till, so a
// sale has something to draw.
func (w world) stocked(ctx context.Context, t *testing.T) {
	t.Helper()

	if _, err := w.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: w.entityID, ProductID: w.gloves, OwnerID: &w.budi,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-01", Source: "PURCHASE",
		QtyIn: 10, CostTotalIdr: 100_000, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("layer: %v", err)
	}
	w.till(ctx, t)
}

func (w world) ringFaktur(ctx context.Context, t *testing.T, faktur bool, price money.IDR, qty int64) service.SaleResult {
	t.Helper()

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	got, err := w.sales.Ring(ctx, actor, service.SaleInput{
		SaleDate: "2026-10-15", FakturIssued: faktur,
		Lines: []service.SaleLineInput{{ProductID: w.gloves, Qty: qty, UnitPriceIDR: &price}},
	})
	if err != nil {
		t.Fatalf("ring: %v", err)
	}
	return got
}

// TestAPKPSaleWithNoFakturStillAccruesOutputPPN is TASKS 5.4.
//
// Two identical sales, differing only in whether the buyer took a faktur. The
// PPN is the same on both, because the liability attaches to the delivery of
// taxable goods and not to the paperwork (SPEC §2.3).
//
// This is the most expensive thing in Phase 5 to get wrong. Nobody chases the
// walk-in half, no counter-party reconciles against it, and nothing on the
// receipt mentions it — so a system that charged nothing here would be found
// out at the masa pajak filing, by which time the shortfall has been paid out
// of margin.
func TestAPKPSaleWithNoFakturStillAccruesOutputPPN(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stocked(ctx, t)

	withFaktur := w.ringFaktur(ctx, t, true, 111_000, 1)
	without := w.ringFaktur(ctx, t, false, 111_000, 1)

	// Rp 111.000 inclusive at an effective 11% is Rp 100.000 + Rp 11.000.
	for _, sale := range []struct {
		name string
		got  service.SaleResult
	}{{"with a faktur", withFaktur}, {"without one", without}} {
		if sale.got.DPP != 100_000 || sale.got.PPN != 11_000 || sale.got.Total != 111_000 {
			t.Errorf("%s: DPP %s + PPN %s = %s, want 100.000 + 11.000 = 111.000",
				sale.name, sale.got.DPP, sale.got.PPN, sale.got.Total)
		}
		if sale.got.Sale.PpnIdr != 11_000 {
			t.Errorf("%s: the sale row carries PPN of %s", sale.name, money.IDR(sale.got.Sale.PpnIdr))
		}
	}

	// The one thing that does differ is the flag the position report reads.
	if withFaktur.AccruedWithoutFaktur {
		t.Error("a sale with a faktur is flagged as accruing without one")
	}
	if !without.AccruedWithoutFaktur {
		t.Error("a walk-in sale owing PPN is not flagged")
	}

	// And the report separates them rather than summing them away, because the
	// walk-in half is the figure nothing else in the business will mention.
	pos, err := w.taxes.Position(ctx, w.entityID, "2026-10")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if pos.Position.OutputWithFaktur != 11_000 || pos.Position.OutputWithoutFaktur != 11_000 {
		t.Errorf("output split = %s with a faktur, %s without, want 11.000 each",
			pos.Position.OutputWithFaktur, pos.Position.OutputWithoutFaktur)
	}
	if pos.Position.Output() != 22_000 {
		t.Errorf("output PPN = %s, want the whole 22.000", pos.Position.Output())
	}
}

// TestTheNonPKPCompanyChargesNothingAndIssuesNothing is TASKS 5.5.
func TestTheNonPKPCompanyChargesNothingAndIssuesNothing(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, false)
	w.stocked(ctx, t)

	got := w.ringFaktur(ctx, t, false, 111_000, 1)

	// The whole amount is the DPP and no PPN is levied -- not because a rate of
	// zero was applied, but because no rate was consulted.
	if got.PPN != 0 || got.DPP != 111_000 || got.Total != 111_000 {
		t.Errorf("DPP %s + PPN %s = %s, want the whole 111.000 untaxed",
			got.DPP, got.PPN, got.Total)
	}
	if len(got.TaxSnapshot) != 0 {
		t.Errorf("a non-PKP sale wrote %d tax snapshot rows", len(got.TaxSnapshot))
	}
	if got.Sale.PpnInclusive != 0 {
		t.Error("an untaxed sale claims its prices included PPN")
	}

	// And a faktur is refused rather than recorded and ignored: a faktur number
	// on a document the buyer takes away is a claim somebody else will try to
	// credit.
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	_, err := w.sales.Ring(ctx, actor, service.SaleInput{
		SaleDate: "2026-10-15", FakturIssued: true,
		Lines: []service.SaleLineInput{{ProductID: w.gloves, Qty: 1}},
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("a non-PKP company issued a faktur: %v", err)
	}
}

// TestTheStorageLayerRefusesPPNAtANonPKPCompany is TASKS 5.5's "enforced, not
// conventional".
//
// The service refuses first and says why. This is the layer underneath: a row
// written by anything at all -- an importer, a repair script, a future handler
// that forgets -- is refused by the database.
func TestTheStorageLayerRefusesPPNAtANonPKPCompany(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, false)

	_, err := w.q.CreateSale(ctx, gen.CreateSaleParams{
		ID: store.NewID(), EntityID: w.entityID, InvoiceNo: "PAKSA-1",
		OccurredAt: fixedNow.Unix(), BusinessDate: "2026-10-15",
		FakturIssued: 0, GrossIdr: 111_000, DiscountIdr: 0,
		DppIdr: 100_000, PpnIdr: 11_000, PpnInclusive: 1,
		TotalIdr: 111_000, CreatedAt: fixedNow.Unix(),
	})
	if err == nil {
		t.Fatal("a non-PKP company charged PPN at the storage layer")
	}

	_, err = w.q.CreateSale(ctx, gen.CreateSaleParams{
		ID: store.NewID(), EntityID: w.entityID, InvoiceNo: "PAKSA-2",
		OccurredAt: fixedNow.Unix(), BusinessDate: "2026-10-15",
		FakturIssued: 1, GrossIdr: 111_000, DiscountIdr: 0,
		DppIdr: 111_000, PpnIdr: 0, PpnInclusive: 0,
		TotalIdr: 111_000, CreatedAt: fixedNow.Unix(),
	})
	if err == nil {
		t.Fatal("a non-PKP company issued a faktur at the storage layer")
	}
}

// TestTheStorageLayerRefusesCreditableInputAtANonPKPCompany is the purchase
// side of the same rule (SPEC §2.3, INV-9).
//
// A non-PKP company may well receive a faktur from a PKP supplier. It is
// recorded, and it credits nothing: the PPN belongs in the cost of the goods.
func TestTheStorageLayerRefusesCreditableInputAtANonPKPCompany(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, false)

	purchase, err := w.q.CreatePurchase(ctx, gen.CreatePurchaseParams{
		ID: store.NewID(), EntityID: w.entityID, SupplierID: w.supplier,
		OccurredAt: fixedNow.Unix(), BusinessDate: "2026-10-01",
		FakturReceived: 1, SubtotalIdr: 100_000, PpnIdr: 11_000, TotalIdr: 111_000,
		CreatedAt: fixedNow.Unix(),
	})
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}

	if _, err := w.q.CreatePurchaseLine(ctx, gen.CreatePurchaseLineParams{
		ID: store.NewID(), PurchaseID: purchase.ID, ProductID: w.gloves,
		Qty: 10, UnitPriceIdr: 10_000, SubtotalIdr: 100_000, PpnIdr: 11_000,
		GrossIdr: 111_000, CostTotalIdr: 100_000, CreditablePpnIdr: 11_000,
		CreatedAt: fixedNow.Unix(),
	}); err == nil {
		t.Fatal("a non-PKP company credited input PPN at the storage layer")
	}
}

// TestTheTillStopsWhenAPKPCompanyHasNoRuleInForce is the other half of
// TASKS 5.4.
//
// Refusing costs an hour. Pricing the sale at zero costs 11% of turnover, paid
// out of margin, discovered at the filing -- so the till stops and the message
// says which date has no rule and where to add one.
func TestTheTillStopsWhenAPKPCompanyHasNoRuleInForce(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stocked(ctx, t)

	// Close the seeded rule the day before the sale, leaving the day uncovered.
	rules, err := w.taxes.ListRules(ctx, w.entityID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules: %v (%d)", err, len(rules))
	}
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CloseRule(ctx, actor, rules[0].ID, "2026-10-14", "diganti"); err != nil {
		t.Fatalf("close: %v", err)
	}

	actor.ClientRequestID = store.NewID()
	_, err = w.sales.Ring(ctx, actor, service.SaleInput{
		SaleDate: "2026-10-15",
		Lines:    []service.SaleLineInput{{ProductID: w.gloves, Qty: 1}},
	})
	if !errors.Is(err, service.ErrTaxConfig) {
		t.Fatalf("a PKP company rang a sale with no rule in force: %v", err)
	}
	// The message has to be actionable at a till with a customer waiting.
	for _, want := range []string{"2026-10-15", "PKP", "Pengaturan Pajak"} {
		if !contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %s", want, err)
		}
	}
}

// TestChangingARateAltersNoHistoricalTransaction is TASKS 5.11 and INV-3.
//
// The whole reason a sale carries a snapshot rather than a reference. Tax law
// moved three times in eighteen months; if a rate change reached backwards,
// every historical margin figure and every filed masa pajak would move with it.
func TestChangingARateAltersNoHistoricalTransaction(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.stocked(ctx, t)

	sold := w.ringFaktur(ctx, t, true, 111_000, 1)
	if sold.PPN != 11_000 {
		t.Fatalf("PPN = %s, want 11.000", sold.PPN)
	}

	before, err := w.taxes.Position(ctx, w.entityID, "2026-10")
	if err != nil {
		t.Fatalf("position: %v", err)
	}

	// The rate changes from November: the old row is closed and a new one
	// opened, which is the only way a rate is ever changed (INV-4).
	rules, err := w.taxes.ListRules(ctx, w.entityID)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CloseRule(ctx, actor, rules[0].ID, "2026-10-31", "tarif berubah"); err != nil {
		t.Fatalf("close: %v", err)
	}
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CreateRule(ctx, actor, service.TaxRuleInput{
		Type: "PPN", RateBP: 1200, DPPNum: 1, DPPDen: 1, Inclusive: true,
		CalculationLevel: "INVOICE", RoundingMode: "HALF_UP", RoundingUnit: 1,
		ValidFrom: "2026-11-01", LegalRef: "UU 7/2021 (HPP) Pasal 7",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Nothing about the October sale moved.
	again, _, _, err := w.sales.GetSale(ctx, w.entityID, sold.Sale.ID)
	if err != nil {
		t.Fatalf("get sale: %v", err)
	}
	if again.PpnIdr != 11_000 || again.DppIdr != 100_000 || again.TotalIdr != 111_000 {
		t.Errorf("the settled sale now reads DPP %s + PPN %s = %s",
			money.IDR(again.DppIdr), money.IDR(again.PpnIdr), money.IDR(again.TotalIdr))
	}

	// Nor did the snapshot beneath it: it still cites the regulation that was
	// in force, which is what a faktur queried in three years has to be
	// answerable with.
	snapshot, err := w.q.ListSaleTax(ctx, sold.Sale.ID)
	if err != nil || len(snapshot) != 1 {
		t.Fatalf("snapshot: %v (%d rows)", err, len(snapshot))
	}
	if snapshot[0].LegalRef != "PMK 131/2024" {
		t.Errorf("the snapshot now cites %q", snapshot[0].LegalRef)
	}
	if snapshot[0].RateBp != 1200 || snapshot[0].DppFactorNum != 11 || snapshot[0].DppFactorDen != 12 {
		t.Errorf("the snapshot now reads %d bp on %d/%d",
			snapshot[0].RateBp, snapshot[0].DppFactorNum, snapshot[0].DppFactorDen)
	}

	// And neither did the masa pajak that was already reported.
	after, err := w.taxes.Position(ctx, w.entityID, "2026-10")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if after.Position != before.Position {
		t.Errorf("October's PPN position moved: %+v became %+v", before.Position, after.Position)
	}
}

// TestARateIsNeverUpdated is INV-4 at the storage layer.
func TestARateIsNeverUpdated(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	rules, err := w.taxes.ListRules(ctx, w.entityID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules: %v (%d)", err, len(rules))
	}

	// A rule already closed cannot be closed again or reopened.
	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CloseRule(ctx, actor, rules[0].ID, "2026-12-31", "selesai"); err != nil {
		t.Fatalf("close: %v", err)
	}
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CloseRule(ctx, actor, rules[0].ID, "2027-01-31", "lagi"); !errors.Is(err, service.ErrRuleClosed) {
		t.Fatalf("a closed rule was closed again: %v", err)
	}
}

// TestARuleCannotOverlapOneAlreadyInForce is the half-done rate change: a new
// row inserted and the old one never closed, leaving two rates claiming the
// same day and row order deciding which a sale gets.
func TestARuleCannotOverlapOneAlreadyInForce(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	_, err := w.taxes.CreateRule(ctx, actor, service.TaxRuleInput{
		Type: "PPN", RateBP: 1200, DPPNum: 1, DPPDen: 1, Inclusive: true,
		CalculationLevel: "INVOICE", RoundingMode: "HALF_UP", RoundingUnit: 1,
		ValidFrom: "2026-11-01", LegalRef: "UU 7/2021 (HPP) Pasal 7",
	})
	if !errors.Is(err, service.ErrValidation) || !errors.Is(err, tax.ErrRuleOverlap) {
		t.Fatalf("an overlapping rule was accepted: %v", err)
	}
	// The message names the rule to close, not just "conflict".
	if !contains(err.Error(), "valid_to") {
		t.Errorf("the refusal does not say what to do: %s", err)
	}
}

// TestAStagedRuleMayBeDeletedButOneInForceMayNot separates a typo from history.
//
// Staging a rule ahead of a registration date is the correct thing to do after
// crossing the threshold (PMK 164/2023 Pasal 18), and a typo in one that has
// priced nothing is worth removing. A rule already in force priced real sales,
// and the report that explains them reads its citation, so it is closed instead.
func TestAStagedRuleMayBeDeletedButOneInForceMayNot(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)

	rules, err := w.taxes.ListRules(ctx, w.entityID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules: %v (%d)", err, len(rules))
	}

	actor := w.actor
	actor.ClientRequestID = store.NewID()
	if err := w.taxes.DeleteRule(ctx, actor, rules[0].ID, "salah"); !errors.Is(err, service.ErrRuleInForce) {
		t.Fatalf("a rule in force was deleted: %v", err)
	}

	// Close it, then stage its replacement, then think better of the
	// replacement.
	actor.ClientRequestID = store.NewID()
	if _, err := w.taxes.CloseRule(ctx, actor, rules[0].ID, "2026-10-31", "diganti"); err != nil {
		t.Fatalf("close: %v", err)
	}
	actor.ClientRequestID = store.NewID()
	staged, err := w.taxes.CreateRule(ctx, actor, service.TaxRuleInput{
		Type: "PPN", RateBP: 1200, DPPNum: 1, DPPDen: 1, Inclusive: true,
		CalculationLevel: "INVOICE", RoundingMode: "HALF_UP", RoundingUnit: 1,
		ValidFrom: "2026-11-01", LegalRef: "UU 7/2021 (HPP) Pasal 7",
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	actor.ClientRequestID = store.NewID()
	if err := w.taxes.DeleteRule(ctx, actor, staged.ID, "salah ketik"); err != nil {
		t.Fatalf("a staged rule could not be removed: %v", err)
	}
}

// TestThePPNPositionCreditsOnlyPurchasesWithAFaktur is SPEC §2.4, and the
// reason purchase tracking exists at all (INV-9).
//
// Two purchases at the same price from the same supplier. Only the one with the
// faktur reduces what is owed; the other one's PPN went into the cost of the
// goods (SPEC §3.2), and crediting it here as well would claim the same rupiah
// twice -- once against output PPN and once as a lower COGS in the margin the
// family settles on.
func TestThePPNPositionCreditsOnlyPurchasesWithAFaktur(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 11_000, true)
	w.buy(ctx, t, 10, 10_000, 11_000, false)
	w.till(ctx, t)
	w.ringFaktur(ctx, t, true, 111_000, 1)

	got, err := w.taxes.Position(ctx, w.entityID, "2026-10")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	p := got.Position

	if p.InputCreditable != 11_000 {
		t.Errorf("creditable input = %s, want the 11.000 backed by a faktur", p.InputCreditable)
	}
	if p.InputNonCreditable != 11_000 {
		t.Errorf("non-creditable input = %s, want the 11.000 that arrived without one", p.InputNonCreditable)
	}
	// Reported, never netted.
	if p.Input() != 11_000 {
		t.Errorf("input PPN = %s, want 11.000", p.Input())
	}
	if p.Payable() != 0 {
		t.Errorf("payable = %s, want zero: 11.000 out against 11.000 creditable in", p.Payable())
	}

	// The same month with the faktur missing on both purchases owes the full
	// output PPN -- which is the 11% the business cannot see today.
	if p.Output().Sub(p.InputCreditable) != 0 {
		t.Errorf("output less credit = %s", p.Output().Sub(p.InputCreditable))
	}

	// Every figure decomposes into the documents behind it (R2.4's principle,
	// applied here because this report goes to a konsultan pajak).
	if len(got.Sales) != 1 {
		t.Errorf("the position lists %d sales, want 1", len(got.Sales))
	}
	if len(got.Purchases) != 2 {
		t.Errorf("the position lists %d purchases, want both", len(got.Purchases))
	}
}

// TestALebihBayarIsNotClampedToZero. A month that bought more than it sold is
// owed the difference: carried to the next masa or claimed at the end of the
// book year (UU PPN Pasal 9 ayat (4)). Clamping would forget it.
func TestALebihBayarIsNotClampedToZero(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	w.buy(ctx, t, 10, 10_000, 11_000, true)

	got, err := w.taxes.Position(ctx, w.entityID, "2026-10")
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if got.Position.Payable() != -11_000 || !got.Position.IsOverpaid() {
		t.Errorf("payable = %s, overpaid = %v; want -11.000 and true",
			got.Position.Payable(), got.Position.IsOverpaid())
	}
}

// TestCreatingACompanySeedsItsTaxConfiguration is TASKS 5.2.
//
// A PKP company cannot ring a sale without a rule in force, so its
// configuration is part of creating it rather than a second step somebody
// discovers at the till. A non-PKP company gets nothing, which is what it may
// charge.
func TestCreatingACompanySeedsItsTaxConfiguration(t *testing.T) {
	t.Parallel()

	w, ctx := newWorld(t, true)
	master := service.NewMasterData(w.db, service.NewAuditor(func() time.Time { return fixedNow }),
		func() time.Time { return fixedNow })

	for _, tt := range []struct {
		name  string
		code  string
		isPKP bool
		want  int
	}{
		{"a PKP company is given the rule it needs to trade", "PKP2", true, 1},
		{"a non-PKP company is given nothing to charge with", "NONPKP", false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actor := w.actor
			actor.ClientRequestID = store.NewID()
			entity, err := master.CreateEntity(ctx, actor, service.EntityInput{
				Code: tt.code, Name: "PT " + tt.code, IsPKP: tt.isPKP,
				Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
			})
			if err != nil {
				t.Fatalf("create entity: %v", err)
			}

			rules, err := w.taxes.ListRules(ctx, entity.ID)
			if err != nil {
				t.Fatalf("rules: %v", err)
			}
			if len(rules) != tt.want {
				t.Fatalf("got %d seeded rules, want %d", len(rules), tt.want)
			}
			if tt.want == 0 {
				return
			}

			// The seeded rule is the one the worked examples were checked
			// against, and it cites the regulation it implements so the
			// konsultan pajak can verify it without reading code.
			r := rules[0]
			if r.RateBp != 1200 || r.DppFactorNum != 11 || r.DppFactorDen != 12 {
				t.Errorf("seeded %d bp on %d/%d, want 1200 on 11/12",
					r.RateBp, r.DppFactorNum, r.DppFactorDen)
			}
			if r.LegalRef == "" {
				t.Error("the seeded rule cites no regulation")
			}
			// No PPnBM row: nothing in an alat kesehatan catalogue is a luxury
			// good, and a rate nobody has verified sitting on an admin screen
			// is a trap. See seed.TaxRulesFor.
			for _, rule := range rules {
				if rule.TaxType == string(tax.PPnBM) {
					t.Error("a PPnBM rate was seeded without being verified")
				}
			}
		})
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
