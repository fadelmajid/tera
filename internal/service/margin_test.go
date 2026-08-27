package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- a simulated October ----------------------------------------------------

// month is one full trading month, built through the real purchase, sale and
// return paths rather than by writing rows.
//
// TASKS 3.6 asks for attribution to be exact across a full simulated month, and
// exact means against figures worked out by hand, not against whatever the code
// happens to produce. Every number below is derivable from the comments:
//
//	Budi    P-GLOVE  L1  2026-10-01  10 @ Rp 10.000 + PPN 11.000, faktur    -> layer Rp 100.000
//	                 L2  2026-10-05  10 @ Rp 10.000 + PPN 11.000, NO faktur -> layer Rp 111.000
//	Sari    P-SYR    L3  2026-10-02  20 @ Rp  5.000 + PPN 11.000, faktur    -> layer Rp 100.000
//	Company P-MASK   L4  2026-10-03  30 @ Rp  2.000 + PPN  6.600, NO faktur -> layer Rp  66.600
//
// L1 and L2 are the same goods at the same price from the same supplier. The
// faktur is the only difference and it makes L2 11% dearer per unit (INV-9,
// SPEC §3.2) -- which is exactly what the drill-down has to be able to explain.
//
// # Revenue here is the DPP, not what the customer handed over
//
// The company is PKP and its seeded rule prices inclusive of PPN at an
// effective 11% (TASKS 5.2), so a shelf price of Rp 240.000 is Rp 216.216 of
// revenue and Rp 23.784 of PPN. Margin is computed on the former: COGS comes
// off a stock layer already net of creditable PPN (SPEC §3.2), so revenue has
// to be net of it too, and the Rp 23.784 was never anybody's to divide -- it is
// owed to the state and shows up in the PPN position instead (SPEC §2.4).
//
// Every DPP below is round(price x 100/111), rounded once on the invoice and
// allocated to the lines by weight:
//
//	Rp 240.000 -> Rp 216.216 + Rp 23.784
//	Rp 100.000 -> Rp  90.090 + Rp  9.910   (Rp 54.054 + Rp 36.036 across two lines)
//	Rp  60.000 -> Rp  54.054 + Rp  5.946
type month struct {
	world
	mrg  *service.Margin
	mask string
}

func newMonth(t *testing.T) (month, context.Context) {
	t.Helper()

	// PKP, so the faktur actually changes the cost basis. In the non-PKP entity
	// input PPN is never creditable and both layers would cost the same.
	w, ctx := newWorld(t, true)
	m := month{world: w, mrg: service.NewMargin(w.db, service.NewAuditor(func() time.Time { return fixedNow }), func() time.Time { return fixedNow })}

	// Company-owned stock: a real bucket beside the family, not a residual
	// (R2.2, INV-8).
	mask, err := w.q.CreateProduct(ctx, gen.CreateProductParams{
		ID: store.NewID(), Code: "P-MASK", Name: "Masker Bedah", Unit: "box",
		OwnerID: nil, SalePriceIdr: 4_000,
	})
	if err != nil {
		t.Fatalf("mask product: %v", err)
	}
	m.mask = mask.ID

	m.purchase(ctx, t, "2026-10-01", true, mline{w.gloves, 10, 10_000, 11_000})
	m.purchase(ctx, t, "2026-10-05", false, mline{w.gloves, 10, 10_000, 11_000})
	m.purchase(ctx, t, "2026-10-02", true, mline{w.syringe, 20, 5_000, 11_000})
	m.purchase(ctx, t, "2026-10-03", false, mline{m.mask, 30, 2_000, 6_600})

	w.till(ctx, t)
	return m, ctx
}

type mline struct {
	productID string
	qty       int64
	unit      money.IDR
	ppn       money.IDR
}

func (m month) purchase(ctx context.Context, t *testing.T, day string, faktur bool, lines ...mline) {
	t.Helper()

	in := service.PurchaseInput{
		SupplierID: m.supplier, PurchaseDate: day, FakturReceived: faktur,
	}
	if faktur {
		in.FakturNo = "010.000-26." + day
	}
	for _, l := range lines {
		in.Lines = append(in.Lines, service.PurchaseLineInput{
			ProductID: l.productID, Qty: l.qty, UnitPriceIDR: l.unit, PPNIDR: l.ppn,
		})
	}

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.purch.CreatePurchase(ctx, actor, in); err != nil {
		t.Fatalf("purchase on %s: %v", day, err)
	}
}

func (m month) sell(ctx context.Context, t *testing.T, day string, lines ...service.SaleLineInput) service.SaleResult {
	t.Helper()

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	got, err := m.sales.Ring(ctx, actor, service.SaleInput{SaleDate: day, Lines: lines})
	if err != nil {
		t.Fatalf("sale on %s: %v", day, err)
	}
	return got
}

func (m month) giveBack(ctx context.Context, t *testing.T, day, saleID, saleLineID string, qty int64) service.SaleReturnResult {
	t.Helper()

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	got, err := m.sales.CreateReturn(ctx, actor, service.SaleReturnInput{
		SaleID: saleID, ReturnDate: day, Reason: "Barang tidak sesuai",
		Lines: []service.SaleReturnLineInput{{SaleLineID: saleLineID, Qty: qty}},
	})
	if err != nil {
		t.Fatalf("return on %s: %v", day, err)
	}
	return got
}

// trade rings the month's sales and returns, and hands back what a hand
// calculation says each figure should be.
//
//	2026-10-10  S1  12 gloves @ Rp 20.000 = Rp 240.000
//	                draws L1 in full (Rp 100.000) then 2 of L2 (Rp 22.200)
//	                COGS Rp 122.200
//	2026-10-12  S2   5 syringes @ Rp 12.000 = Rp  60.000, COGS Rp 25.000
//	                10 masks    @ Rp  4.000 = Rp  40.000, COGS Rp 22.200
//	2026-10-20  S3   3 gloves @ Rp 20.000 = Rp  60.000
//	                draws 3 more of L2: Rp 55.500 - Rp 22.200 = Rp 33.300
//	2026-10-22  R1   2 syringes back: refund Rp 24.000, cost back Rp 10.000
//	2026-11-05  R2   2 gloves back against S1: refund Rp 40.000, cost back
//	                Rp 22.200 -- the November return of an October sale, which
//	                is the whole of SPEC §4.4
func (m month) trade(ctx context.Context, t *testing.T) (s1, s2 service.SaleResult) {
	t.Helper()

	s1 = m.sell(ctx, t, "2026-10-10", service.SaleLineInput{ProductID: m.gloves, Qty: 12, UnitPriceIDR: idr(20_000)})
	s2 = m.sell(ctx, t, "2026-10-12",
		service.SaleLineInput{ProductID: m.syringe, Qty: 5, UnitPriceIDR: idr(12_000)},
		service.SaleLineInput{ProductID: m.mask, Qty: 10, UnitPriceIDR: idr(4_000)},
	)
	m.sell(ctx, t, "2026-10-20", service.SaleLineInput{ProductID: m.gloves, Qty: 3, UnitPriceIDR: idr(20_000)})

	m.giveBack(ctx, t, "2026-10-22", s2.Sale.ID, lineFor(t, &s2, m.syringe).ID, 2)
	m.giveBack(ctx, t, "2026-11-05", s1.Sale.ID, lineFor(t, &s1, m.gloves).ID, 2)

	return s1, s2
}

func idr(v money.IDR) *money.IDR { return &v }

func lineFor(t *testing.T, got *service.SaleResult, productID string) gen.SaleLine {
	t.Helper()
	for _, l := range got.Lines {
		if l.ProductID == productID {
			return l
		}
	}
	t.Fatalf("sale %s has no line for product %s", got.Sale.InvoiceNo, productID)
	return gen.SaleLine{}
}

func (m month) report(ctx context.Context, t *testing.T, from, to string) margin.Report {
	t.Helper()
	got, err := m.mrg.Report(ctx, m.entityID, from, to)
	if err != nil {
		t.Fatalf("margin report %s..%s: %v", from, to, err)
	}
	return got
}

func line(t *testing.T, r margin.Report, name string) margin.OwnerReport {
	t.Helper()
	for _, o := range r.Owners {
		if o.OwnerName == name {
			return o
		}
	}
	t.Fatalf("%s has no line on the report", name)
	return margin.OwnerReport{}
}

// --- the tests --------------------------------------------------------------

// TestMarginAttributionIsExactAcrossAMonth is TASKS 3.6.
//
// Every figure is worked out by hand in month's comments. If this test ever
// needs its expectations "updated" to match new output, that is the signal to
// stop: this is the report family members divide money on, and the numbers are
// the specification, not the observation.
//
// They have moved exactly once, and deliberately: when the tax engine landed
// (TASKS 5.3) revenue became the DPP rather than the amount tendered, so every
// figure here fell by the PPN inside it. That is a change to what the report
// means, recorded as D-015, and it was worked through by hand -- not accepted
// because the output changed.
func TestMarginAttributionIsExactAcrossAMonth(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	got := m.report(ctx, t, "2026-10-01", "2026-10-31")

	tests := []struct {
		owner                    string
		revenue, cogs            money.IDR
		returnRefund, returnCOGS money.IDR
		want                     money.IDR
	}{
		// Rp 240.000 + Rp 60.000 taken at the till is Rp 216.216 + Rp 54.054
		// of revenue, against 122.200 + 33.300 drawn. November's return is not
		// here: the default rule books it into November (SPEC §4.4).
		{"Budi", 270_270, 155_500, 0, 0, 114_770},
		// Rp 60.000 taken is Rp 54.054 of revenue against Rp 25.000 of cost,
		// less a refund inside the same month. The customer got Rp 24.000 back,
		// which is what ReturnRefund reports; Rp 2.378 of it was PPN, so the
		// margin only loses Rp 21.622 - Rp 10.000.
		{"Sari", 54_054, 25_000, 24_000, 10_000, 17_432},
		// Unowned stock, its own line (R2.2). Rp 40.000 taken, Rp 36.036 of it
		// revenue.
		{"Perusahaan", 36_036, 22_200, 0, 0, 13_836},
	}

	for _, tt := range tests {
		o := line(t, got, tt.owner)
		if o.Revenue != tt.revenue || o.COGS != tt.cogs {
			t.Errorf("%s: revenue/COGS = %s/%s, want %s/%s",
				tt.owner, o.Revenue, o.COGS, tt.revenue, tt.cogs)
		}
		if o.ReturnRefund != tt.returnRefund || o.ReturnCOGS != tt.returnCOGS {
			t.Errorf("%s: returns = %s/%s, want %s/%s",
				tt.owner, o.ReturnRefund, o.ReturnCOGS, tt.returnRefund, tt.returnCOGS)
		}
		if o.Margin() != tt.want {
			t.Errorf("%s: margin = %s, want %s", tt.owner, o.Margin(), tt.want)
		}
	}

	// 114.770 + 17.432 + 13.836.
	if got.Totals.Margin() != 146_038 {
		t.Errorf("total margin = %s, want %s", got.Totals.Margin(), money.IDR(146_038))
	}

	// What the family divides has to be what the shop made.
	var sum money.IDR
	for _, o := range got.Owners {
		sum = sum.Add(o.Margin())
	}
	if sum != got.Totals.Margin() {
		t.Errorf("owner margins sum to %s against a total of %s", sum, got.Totals.Margin())
	}
}

// The report's whole reason for existing, asserted directly: two purchases at
// the same price from the same supplier produce different costs, and the
// drill-down can say which layer and why (INV-9, SPEC §3.2, acceptance 5).
func TestDrillDownExplainsWhyTheSecondLayerCostMore(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	budi := line(t, m.report(ctx, t, "2026-10-01", "2026-10-31"), "Budi")
	if len(budi.Sales) != 2 {
		t.Fatalf("Budi has %d sales, want 2", len(budi.Sales))
	}

	layers := budi.Sales[0].Products[0].Layers
	if len(layers) != 2 {
		t.Fatalf("the first sale shows %d layers, want the 2 it drew", len(layers))
	}

	withFaktur, without := layers[0], layers[1]
	if !withFaktur.FakturReceived || without.FakturReceived {
		t.Fatalf("faktur status = %v/%v, want true then false",
			withFaktur.FakturReceived, without.FakturReceived)
	}
	// Rp 10.000 a unit against Rp 11.100 a unit: the PPN on the second layer
	// was never creditable, so it is cost.
	if withFaktur.Cost != 100_000 || withFaktur.Qty != 10 {
		t.Errorf("faktur layer: %d units at %s, want 10 at %s",
			withFaktur.Qty, withFaktur.Cost, money.IDR(100_000))
	}
	if without.Cost != 22_200 || without.Qty != 2 {
		t.Errorf("no-faktur layer: %d units at %s, want 2 at %s",
			without.Qty, without.Cost, money.IDR(22_200))
	}
	// The whole layers behind those slices: Rp 111.000 for ten units against
	// Rp 100.000 for ten, on the same invoice price from the same supplier.
	if without.LayerCostTotal <= withFaktur.LayerCostTotal {
		t.Errorf("layer totals %s vs %s: the layer bought without a faktur is not dearer, so INV-9 is not reaching the report",
			without.LayerCostTotal, withFaktur.LayerCostTotal)
	}
}

// SPEC §4.2: no summary-only views. Every figure decomposes into the rows
// beneath it, all the way to individual layer draws, and the arithmetic holds
// at each step. This is the chain that answers "why is mine lower this month".
func TestEveryFigureDecomposes(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	for _, window := range [][2]string{{"2026-10-01", "2026-10-31"}, {"2026-11-01", "2026-11-30"}} {
		got := m.report(ctx, t, window[0], window[1])

		for _, o := range got.Owners {
			var revenue, ppn, cogs, refund, returnPPN, returnCOGS money.IDR

			for _, s := range o.Sales {
				var saleRevenue, salePPN, saleCOGS money.IDR
				for _, p := range s.Products {
					var layered money.IDR
					for _, l := range p.Layers {
						layered = layered.Add(l.Cost)
					}
					if layered != p.COGS {
						t.Errorf("%s %s %s: layers total %s, product says %s",
							o.OwnerName, s.InvoiceNo, p.ProductCode, layered, p.COGS)
					}
					saleRevenue, saleCOGS = saleRevenue.Add(p.Revenue), saleCOGS.Add(p.COGS)
					salePPN = salePPN.Add(p.PPN)
				}
				if saleRevenue != s.Revenue || saleCOGS != s.COGS {
					t.Errorf("%s %s: products total %s/%s, sale says %s/%s",
						o.OwnerName, s.InvoiceNo, saleRevenue, saleCOGS, s.Revenue, s.COGS)
				}
				// The tax decomposes too, and revenue plus tax is what the
				// customer handed over -- which is the figure somebody will
				// check the report against (SPEC §2.2).
				if salePPN != s.PPN || s.Revenue.Add(s.PPN) != s.Tendered() {
					t.Errorf("%s %s: PPN %s against the sale's %s, tendered %s",
						o.OwnerName, s.InvoiceNo, salePPN, s.PPN, s.Tendered())
				}
				revenue, cogs = revenue.Add(s.Revenue), cogs.Add(s.COGS)
				ppn = ppn.Add(s.PPN)
			}

			for _, r := range o.Returns {
				var retRevenue, retPPN, retCOGS money.IDR
				for _, p := range r.Products {
					var layered money.IDR
					for _, l := range p.Layers {
						if !l.IsReversal {
							t.Errorf("%s return %s carries a forward draw", o.OwnerName, r.ReturnID)
						}
						layered = layered.Add(l.Cost)
					}
					if layered != p.COGS {
						t.Errorf("%s return %s %s: layers total %s, product says %s",
							o.OwnerName, r.ReturnID, p.ProductCode, layered, p.COGS)
					}
					retRevenue, retCOGS = retRevenue.Add(p.Revenue), retCOGS.Add(p.COGS)
					retPPN = retPPN.Add(p.PPN)
				}
				// The product rows carry the revenue given back and the tax
				// given back separately; together they are the sum the customer
				// actually received.
				if retRevenue.Add(retPPN) != r.Refund || retCOGS != r.COGSReversed {
					t.Errorf("%s return %s: products total %s/%s, return says %s/%s",
						o.OwnerName, r.ReturnID, retRevenue.Add(retPPN), retCOGS,
						r.Refund, r.COGSReversed)
				}
				if retPPN != r.PPNReversed || r.RevenueReversed() != retRevenue {
					t.Errorf("%s return %s: PPN %s against the return's %s",
						o.OwnerName, r.ReturnID, retPPN, r.PPNReversed)
				}
				refund, returnCOGS = refund.Add(r.Refund), returnCOGS.Add(r.COGSReversed)
				returnPPN = returnPPN.Add(r.PPNReversed)
			}

			if revenue != o.Revenue || cogs != o.COGS || refund != o.ReturnRefund || returnCOGS != o.ReturnCOGS {
				t.Errorf("%s in %s..%s: the rows beneath total %s/%s/%s/%s, the owner line says %s/%s/%s/%s",
					o.OwnerName, window[0], window[1], revenue, cogs, refund, returnCOGS,
					o.Revenue, o.COGS, o.ReturnRefund, o.ReturnCOGS)
			}
			if ppn != o.PPN || returnPPN != o.ReturnPPN {
				t.Errorf("%s in %s..%s: PPN beneath totals %s/%s, the owner line says %s/%s",
					o.OwnerName, window[0], window[1], ppn, returnPPN, o.PPN, o.ReturnPPN)
			}
		}
	}
}

// TASKS 3.4 end to end: the same month, the same data, read under both rules.
//
// The difference between the two answers is Rp 17.800 of Budi's money. That is
// the size of the decision the user has not yet made, and it is the reason the
// rule is configuration rather than a choice made in code.
func TestReturnsPeriodRuleMovesMoneyBetweenMonths(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	// The default (D-012): the November return comes out of November.
	rule, err := m.mrg.ReturnRuleFor(ctx, m.entityID)
	if err != nil {
		t.Fatalf("return rule: %v", err)
	}
	if rule.Rule != margin.AtReturnDate || rule.Chosen {
		t.Fatalf("default rule = %s chosen=%v, want an untouched RETURN_DATE", rule.Rule, rule.Chosen)
	}

	october := m.report(ctx, t, "2026-10-01", "2026-10-31")
	if got := line(t, october, "Budi").Margin(); got != 114_770 {
		t.Errorf("Budi's October under RETURN_DATE = %s, want %s", got, money.IDR(114_770))
	}
	november := m.report(ctx, t, "2026-11-01", "2026-11-30")
	budiNov := line(t, november, "Budi")
	// The customer got Rp 40.000 back, Rp 3.964 of it PPN, so Rp 36.036 of
	// revenue reversed against Rp 22.200 of cost put back.
	if budiNov.Margin() != -13_836 {
		t.Errorf("Budi's November under RETURN_DATE = %s, want %s", budiNov.Margin(), money.IDR(-13_836))
	}
	if len(budiNov.Sales) != 0 {
		t.Errorf("November pulled in %d October sales", len(budiNov.Sales))
	}
	// The return is a line of its own, flagged as the cross-period case it is.
	if len(budiNov.Returns) != 1 || !budiNov.Returns[0].CrossesPeriod {
		t.Error("the November return is not shown as a distinct, cross-period line")
	}

	// The owner decides the other way.
	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.mrg.SetReturnRule(ctx, actor, "SALE_DATE", "Retur dibukukan ke bulan penjualan asal"); err != nil {
		t.Fatalf("set rule: %v", err)
	}

	october = m.report(ctx, t, "2026-10-01", "2026-10-31")
	// 114.770 less the 13.836 the November return takes out of October.
	if got := line(t, october, "Budi").Margin(); got != 100_934 {
		t.Errorf("Budi's October under SALE_DATE = %s, want %s", got, money.IDR(100_934))
	}
	if october.ReturnPeriod != margin.AtSaleDate {
		t.Errorf("the report says it ran under %q after the owner switched to SALE_DATE", october.ReturnPeriod)
	}
	if got := m.report(ctx, t, "2026-11-01", "2026-11-30"); len(got.Owners) != 0 {
		t.Errorf("November under SALE_DATE has %d owner lines, want none", len(got.Owners))
	}
}

// Changing the rule is audited, because it changes what every past report says.
// R7.3: there is no period locking, so this log is the only trace that October
// was read one way in November and another way in December.
func TestChangingTheReturnRuleIsAudited(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	before := m.report(ctx, t, "2026-10-01", "2026-10-31")
	if before.ReturnPeriod != margin.AtReturnDate {
		t.Fatalf("an untouched company runs under %q, want the RETURN_DATE default", before.ReturnPeriod)
	}

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.mrg.SetReturnRule(ctx, actor, "SALE_DATE", "Disepakati keluarga 21 Agustus 2026"); err != nil {
		t.Fatalf("set rule: %v", err)
	}

	rule, err := m.mrg.ReturnRuleFor(ctx, m.entityID)
	if err != nil {
		t.Fatalf("return rule: %v", err)
	}
	if !rule.Chosen || rule.Rule != margin.AtSaleDate || rule.Note == "" {
		t.Errorf("after choosing: %+v", rule)
	}

	entries, err := m.q.ListAuditForRecord(ctx, gen.ListAuditForRecordParams{
		RecordType: "margin_setting", RecordID: m.entityID,
	})
	if err != nil {
		t.Fatalf("audit log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("changing the returns rule left no audit entry (INV-10)")
	}
	if entries[0].BeforeJson == nil || entries[0].AfterJson == nil {
		t.Error("the audit entry does not carry both sides of the change")
	}
}

// INV-8 on the report. Another owner's stock on the same shelf must not appear
// in anybody else's figures, and must not move a single rupiah of margin.
func TestForeignOwnersStockNeverEntersAnothersMargin(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)

	// Sari holds a hundred boxes of gloves -- a product attributed to Budi.
	// Nothing about the sales below may touch it.
	if _, err := m.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: m.entityID, ProductID: m.gloves, OwnerID: &m.sari,
		AcquiredAt: fixedNow.AddDate(-1, 0, 0).Unix(), BusinessDate: "2025-10-01",
		Source: "PURCHASE", QtyIn: 100, CostTotalIdr: 1, CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("sari's gloves: %v", err)
	}

	m.trade(ctx, t)
	got := m.report(ctx, t, "2026-10-01", "2026-10-31")

	// Sari's layer is older and vastly cheaper, so drawing it would have shown
	// up as a suspiciously good month for whoever got it.
	if b := line(t, got, "Budi"); b.COGS != 155_500 {
		t.Errorf("Budi's COGS = %s, want %s -- a cheaper layer of Sari's was drawn", b.COGS, money.IDR(155_500))
	}
	for _, s := range line(t, got, "Sari").Sales {
		for _, p := range s.Products {
			if p.ProductID == m.gloves {
				t.Errorf("Sari's line carries gloves from sale %s", s.InvoiceNo)
			}
		}
	}
	if got.Totals.Margin() != 146_038 {
		t.Errorf("total margin = %s, want %s", got.Totals.Margin(), money.IDR(146_038))
	}
}

// A voided sale did not happen, and must not appear anywhere on the report --
// not as a row, not as a zero.
func TestAVoidedSaleLeavesNoTrailOnTheReport(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	s1, _ := m.trade(ctx, t)

	// A fourth sale, rung and then voided while the till is still open.
	mistake := m.sell(ctx, t, "2026-10-20", service.SaleLineInput{ProductID: m.syringe, Qty: 4, UnitPriceIDR: idr(12_000)})

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.sales.Void(ctx, actor, mistake.Sale.ID, "Salah input"); err != nil {
		t.Fatalf("void: %v", err)
	}

	got := m.report(ctx, t, "2026-10-01", "2026-10-31")

	sari := line(t, got, "Sari")
	// Rp 60.000 at the till, Rp 54.054 of it revenue.
	if sari.Revenue != 54_054 || sari.COGS != 25_000 {
		t.Errorf("Sari after a void: revenue/COGS = %s/%s, want 54.054/25.000", sari.Revenue, sari.COGS)
	}
	for _, s := range sari.Sales {
		if s.SaleID == mistake.Sale.ID {
			t.Error("a voided sale is on the report")
		}
	}
	if got.Totals.Margin() != 146_038 {
		t.Errorf("total margin = %s, want %s", got.Totals.Margin(), money.IDR(146_038))
	}
	// And the sale that did happen is still there.
	if len(line(t, got, "Budi").Sales) != 2 || s1.Sale.Status != "FINAL" {
		t.Error("voiding one sale disturbed another")
	}
}

// An unrecognised stored rule is refused rather than defaulted: reporting under
// a rule nobody can name is how a settlement gets argued about twice.
func TestAnUnknownStoredRuleIsRefused(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)

	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.mrg.SetReturnRule(ctx, actor, "SEMAUNYA", ""); !errors.Is(err, service.ErrValidation) {
		t.Fatalf("got %v, want ErrValidation", err)
	}
}

// The window defaults to the current month in the company's own timezone, never
// the server's (INV-5, D-005). fixedNow is 2026-10-15 14:30 UTC, which is
// already 21:30 in Jakarta -- same day, but the report must be reasoning in WIB
// to say so.
func TestTheDefaultWindowIsTheEntitysCurrentMonth(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	m.trade(ctx, t)

	got, err := m.mrg.Report(ctx, m.entityID, "", "")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if got.Period.From != "2026-10-01" || got.Period.To != "2026-10-31" {
		t.Errorf("default window = %s, want October", got.Period)
	}
}

// D-011 against the real database: when the two attribution paths disagree, the
// report refuses instead of printing a figure.
//
// The corruption here is the one that matters. A draw against Sari's layer is
// recorded under Budi's sale, so Budi's revenue would sit against Sari's cost.
// Printed naively it would look entirely reasonable — the totals would add up,
// the drill-down would agree with itself, and the only trace would be one
// family member quietly receiving less than they earned. Nothing downstream
// would ever question it, which is exactly why this stops here.
func TestTheReportRefusesWhenRevenueAndCostDisagreeAboutTheOwner(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	s1, _ := m.trade(ctx, t)

	// Sari's own layer of Budi's product, and a draw against it booked to
	// Budi's sale. Both tables are append-only, so this is the shape a real
	// corruption would take: an extra row, not an edited one (INV-7).
	layer, err := m.q.CreateStockLayer(ctx, gen.CreateStockLayerParams{
		ID: store.NewID(), EntityID: m.entityID, ProductID: m.gloves, OwnerID: &m.sari,
		AcquiredAt: fixedNow.Unix(), BusinessDate: "2026-10-06", Source: "PURCHASE",
		QtyIn: 5, CostTotalIdr: 50_000, CreatedAt: fixedNow.Unix(),
	})
	if err != nil {
		t.Fatalf("sari's layer: %v", err)
	}
	if _, err := m.q.RecordConsumption(ctx, gen.RecordConsumptionParams{
		ID: store.NewID(), LayerID: layer.ID, MovementID: s1.Sale.ID, MovementType: "SALE",
		QtyOut: 1, CostIdr: 10_000, OccurredAt: fixedNow.Unix(),
		BusinessDate: "2026-10-10", CreatedAt: fixedNow.Unix(),
	}); err != nil {
		t.Fatalf("crossed draw: %v", err)
	}

	_, err = m.mrg.Report(ctx, m.entityID, "2026-10-01", "2026-10-31")
	if !errors.Is(err, service.ErrReportIntegrity) {
		t.Fatalf("got %v, want ErrReportIntegrity", err)
	}
	// And the refusal says where to look, not just that something is wrong.
	for _, want := range []string{s1.Sale.InvoiceNo, m.gloves} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// The report and the till both assume a sale is never both voided and
// returned. Two guards hold that up, and this covers each.
//
// The service refuses the combination outright (R12.3): a return is proof the
// goods left the shop, which is what a void says never happened. And if the
// state ever exists anyway — a hand-edited database is a real possibility for a
// system whose backup story is "copy the file" and whose recovery story is
// "open it in sqlite3" (D-003) — the report ignores it rather than reducing an
// owner's margin against revenue that is no longer there.
func TestAVoidedSaleNeverCarriesReturnsOntoTheReport(t *testing.T) {
	t.Parallel()

	m, ctx := newMonth(t)
	_, s2 := m.trade(ctx, t)

	before := m.report(ctx, t, "2026-10-01", "2026-10-31")
	if sari := line(t, before, "Sari"); len(sari.Returns) != 1 {
		t.Fatalf("Sari has %d returns, want the 1 she took back", len(sari.Returns))
	}

	// The service will not let the two meet.
	actor := m.actor
	actor.ClientRequestID = store.NewID()
	if _, err := m.sales.Void(ctx, actor, s2.Sale.ID, "Salah pelanggan"); !errors.Is(err, service.ErrVoidAfterReturn) {
		t.Fatalf("voiding a returned sale gave %v, want ErrVoidAfterReturn", err)
	}
	if got := m.report(ctx, t, "2026-10-01", "2026-10-31"); got.Totals.Margin() != 146_038 {
		t.Errorf("a refused void moved the total to %s", got.Totals.Margin())
	}

	// Force the state the service refuses, at the storage layer, and check the
	// report still refuses to read it.
	now := fixedNow.Unix()
	reason := "dipaksa lewat sqlite3"
	if _, err := m.q.VoidSale(ctx, gen.VoidSaleParams{
		ID: s2.Sale.ID, VoidedAt: &now, VoidReason: &reason,
	}); err != nil {
		t.Fatalf("forced void: %v", err)
	}

	after := m.report(ctx, t, "2026-10-01", "2026-10-31")
	for _, o := range after.Owners {
		for _, r := range o.Returns {
			if r.SaleID == s2.Sale.ID {
				t.Errorf("%s still shows a return against a voided sale", o.OwnerName)
			}
		}
		if o.OwnerName == "Sari" && (o.ReturnRefund != 0 || o.ReturnCOGS != 0) {
			t.Errorf("Sari still carries %s/%s of returns from a voided sale",
				o.ReturnRefund, o.ReturnCOGS)
		}
	}
	// Budi sold separately and is untouched by any of it.
	if got := line(t, after, "Budi").Margin(); got != 114_770 {
		t.Errorf("Budi's margin = %s, want %s", got, money.IDR(114_770))
	}
}
