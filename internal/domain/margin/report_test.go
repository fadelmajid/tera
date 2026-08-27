package margin_test

import (
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// october is the month every test below reports on.
//
//	Budi   sells 12 boxes of gloves for Rp 180.000, drawing two layers:
//	       10 units out of a Rp 100.000 layer bought with a faktur, then
//	       2 units out of a Rp 120.000 layer bought without one. COGS
//	       Rp 124.000 -- and the second layer is dearer per unit for exactly
//	       the reason INV-9 exists.
//	Sari   sells 5 syringes for Rp 75.000 against Rp 40.000 of stock.
//	Company sells 3 masks for Rp 30.000 against Rp 18.000 -- unowned stock,
//	       its own line beside the family's (R2.2).
//
// Two returns come back: one syringe inside October, and two boxes of gloves in
// November against the October sale. The second is the whole of SPEC §4.4.
func october() ([]margin.Sale, []margin.Return) {
	sales := []margin.Sale{
		{
			ID: "sale-1", InvoiceNo: "20261010-0001", BusinessDate: "2026-10-10",
			OccurredAt: acquired(10), CustomerName: "Klinik Sehat",
			Lines: []margin.SaleLine{
				saleLine("sl-1", "p-glove", "P-GLOVE", "Sarung Tangan", budi, 12, 180_000, 124_000),
			},
			Draws: []margin.Draw{
				draw("d-1", "layer-1", "p-glove", budi, 1, 10, 100_000),
				draw("d-2", "layer-2", "p-glove", budi, 5, 2, 24_000, noFaktur),
			},
		},
		{
			ID: "sale-2", InvoiceNo: "20261012-0001", BusinessDate: "2026-10-12",
			OccurredAt: acquired(12), CustomerName: "Apotek Melati",
			Lines: []margin.SaleLine{
				saleLine("sl-2", "p-syr", "P-SYR", "Spuit 3ml", sari, 5, 75_000, 40_000),
				saleLine("sl-3", "p-mask", "P-MASK", "Masker", margin.Company, 3, 30_000, 18_000),
			},
			Draws: []margin.Draw{
				draw("d-3", "layer-3", "p-syr", sari, 2, 5, 40_000),
				draw("d-4", "layer-4", "p-mask", margin.Company, 3, 3, 18_000),
			},
		},
	}

	returns := []margin.Return{
		{
			ID: "ret-1", SaleID: "sale-2", SaleInvoiceNo: "20261012-0001",
			BusinessDate: "2026-10-20", SaleBusinessDate: "2026-10-12",
			Reason: "Kemasan rusak",
			Lines: []margin.ReturnLine{
				returnLine("rl-1", "sl-2", "p-syr", "P-SYR", "Spuit 3ml", sari, 1, 15_000, 8_000),
			},
			Draws: []margin.Draw{
				reversal("d-5", "d-3", "layer-3", "p-syr", sari, 2, 1, 8_000),
			},
		},
		{
			ID: "ret-2", SaleID: "sale-1", SaleInvoiceNo: "20261010-0001",
			BusinessDate: "2026-11-03", SaleBusinessDate: "2026-10-10",
			Reason: "Ukuran salah",
			Lines: []margin.ReturnLine{
				returnLine("rl-2", "sl-1", "p-glove", "P-GLOVE", "Sarung Tangan", budi, 2, 30_000, 24_000),
			},
			Draws: []margin.Draw{
				reversal("d-6", "d-2", "layer-2", "p-glove", budi, 5, 2, 24_000),
			},
		},
	}
	return sales, returns
}

// TestMarginIsRevenueLessTheLayersActuallyDrawn is SPEC §4.1.
//
// COGS is the sum of the specific consumption rows, never an average. The two
// glove layers cost Rp 10.000 and Rp 12.000 a unit; an average would have
// produced Rp 132.000 against the same twelve boxes and quietly moved Rp 8.000
// of Budi's money.
func TestMarginIsRevenueLessTheLayersActuallyDrawn(t *testing.T) {
	t.Parallel()

	sales, returns := october()
	got := compute(t, margin.Input{Period: oct, ReturnPeriod: atReturnDate, Sales: sales, Returns: returns})

	b := ownerLine(t, got, budi)
	mustEqual(t, "Budi revenue", b.Revenue, 180_000)
	mustEqual(t, "Budi COGS", b.COGS, 124_000)
	mustEqual(t, "Budi margin", b.Margin(), 56_000)

	// The average-cost answer, spelled out so a future refactor towards it
	// fails here rather than in a family argument.
	if b.COGS == 132_000 {
		t.Fatal("COGS is the average of the layers, not the layers drawn (SPEC §4.1)")
	}
}

func TestOwnerTotals(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	tests := []struct {
		name                     string
		rule                     margin.ReturnPeriodRule
		period                   margin.Period
		owner                    margin.OwnerID
		revenue, cogs            money.IDR
		returnRefund, returnCOGS money.IDR
		margin                   money.IDR
	}{
		{
			name: "Budi in October, November return booked to November",
			rule: atReturnDate, period: oct, owner: budi,
			revenue: 180_000, cogs: 124_000, margin: 56_000,
		},
		{
			name: "Budi in November carries only the return",
			rule: atReturnDate, period: nov, owner: budi,
			returnRefund: 30_000, returnCOGS: 24_000, margin: -6_000,
		},
		{
			name: "Budi in October, November return booked back to October",
			rule: atSaleDate, period: oct, owner: budi,
			revenue: 180_000, cogs: 124_000,
			returnRefund: 30_000, returnCOGS: 24_000, margin: 50_000,
		},
		{
			name: "Sari's own-month return lands in October under either rule",
			rule: atReturnDate, period: oct, owner: sari,
			revenue: 75_000, cogs: 40_000,
			returnRefund: 15_000, returnCOGS: 8_000, margin: 28_000,
		},
		{
			name: "the company bucket is a line of its own",
			rule: atReturnDate, period: oct, owner: margin.Company,
			revenue: 30_000, cogs: 18_000, margin: 12_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := compute(t, margin.Input{
				Period: tt.period, ReturnPeriod: tt.rule, Sales: sales, Returns: returns,
			})
			o := ownerLine(t, got, tt.owner)

			mustEqual(t, "revenue", o.Revenue, tt.revenue)
			mustEqual(t, "COGS", o.COGS, tt.cogs)
			mustEqual(t, "return refund", o.ReturnRefund, tt.returnRefund)
			mustEqual(t, "return COGS", o.ReturnCOGS, tt.returnCOGS)
			mustEqual(t, "margin", o.Margin(), tt.margin)
		})
	}
}

// TestOwnerLinesSumToTheTotal is the arithmetic the settlement itself relies
// on: what the family divides is what the shop made.
func TestOwnerLinesSumToTheTotal(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	for _, rule := range []margin.ReturnPeriodRule{atReturnDate, atSaleDate} {
		t.Run(string(rule), func(t *testing.T) {
			t.Parallel()

			got := compute(t, margin.Input{Period: oct, ReturnPeriod: rule, Sales: sales, Returns: returns})

			var sum margin.Figures
			for _, o := range got.Owners {
				sum.Revenue = sum.Revenue.Add(o.Revenue)
				sum.COGS = sum.COGS.Add(o.COGS)
				sum.ReturnRefund = sum.ReturnRefund.Add(o.ReturnRefund)
				sum.ReturnCOGS = sum.ReturnCOGS.Add(o.ReturnCOGS)
			}
			if sum != got.Totals {
				t.Errorf("owner lines sum to %+v, report totals say %+v", sum, got.Totals)
			}
		})
	}
}

// TestDrillDownDecomposesToTheLayer is TASKS 3.3 and SPEC §4.2, asserted as
// arithmetic rather than as the existence of a field: every level must equal
// the level beneath it, all the way down to individual layer draws. When
// someone asks why theirs is lower this month, this is the chain that answers.
func TestDrillDownDecomposesToTheLayer(t *testing.T) {
	t.Parallel()

	sales, returns := october()
	got := compute(t, margin.Input{Period: oct, ReturnPeriod: atSaleDate, Sales: sales, Returns: returns})

	if len(got.Owners) == 0 {
		t.Fatal("no owners on the report")
	}

	for _, o := range got.Owners {
		var revenue, cogs, refund, returnCOGS money.IDR

		for _, s := range o.Sales {
			var saleRevenue, saleCOGS money.IDR
			for _, p := range s.Products {
				var layered money.IDR
				for _, l := range p.Layers {
					if l.IsReversal {
						t.Errorf("%s: sale %s carries a reversal draw", o.OwnerName, s.InvoiceNo)
					}
					layered = layered.Add(l.Cost)
				}
				if layered != p.COGS {
					t.Errorf("%s sale %s product %s: layers total %s, product line says %s",
						o.OwnerName, s.InvoiceNo, p.ProductCode, layered, p.COGS)
				}
				if len(p.Layers) == 0 {
					t.Errorf("%s sale %s product %s: no layers to drill into (SPEC §4.2)",
						o.OwnerName, s.InvoiceNo, p.ProductCode)
				}
				saleRevenue = saleRevenue.Add(p.Revenue)
				saleCOGS = saleCOGS.Add(p.COGS)
			}
			if saleRevenue != s.Revenue || saleCOGS != s.COGS {
				t.Errorf("%s sale %s: products total %s/%s, sale says %s/%s",
					o.OwnerName, s.InvoiceNo, saleRevenue, saleCOGS, s.Revenue, s.COGS)
			}
			revenue = revenue.Add(s.Revenue)
			cogs = cogs.Add(s.COGS)
		}

		for _, r := range o.Returns {
			var retRefund, retCOGS money.IDR
			for _, p := range r.Products {
				var layered money.IDR
				for _, l := range p.Layers {
					if !l.IsReversal {
						t.Errorf("%s: return %s carries a forward draw", o.OwnerName, r.ReturnID)
					}
					layered = layered.Add(l.Cost)
				}
				if layered != p.COGS {
					t.Errorf("%s return %s product %s: layers total %s, product line says %s",
						o.OwnerName, r.ReturnID, p.ProductCode, layered, p.COGS)
				}
				retRefund = retRefund.Add(p.Revenue)
				retCOGS = retCOGS.Add(p.COGS)
			}
			if retRefund != r.Refund || retCOGS != r.COGSReversed {
				t.Errorf("%s return %s: products total %s/%s, return says %s/%s",
					o.OwnerName, r.ReturnID, retRefund, retCOGS, r.Refund, r.COGSReversed)
			}
			refund = refund.Add(r.Refund)
			returnCOGS = returnCOGS.Add(r.COGSReversed)
		}

		if revenue != o.Revenue || cogs != o.COGS || refund != o.ReturnRefund || returnCOGS != o.ReturnCOGS {
			t.Errorf("%s: the rows underneath total %s/%s/%s/%s, the owner line says %s/%s/%s/%s",
				o.OwnerName, revenue, cogs, refund, returnCOGS,
				o.Revenue, o.COGS, o.ReturnRefund, o.ReturnCOGS)
		}
	}
}

// The drill-down has to carry the faktur status: it is the reason two
// otherwise identical purchases produce different costs (INV-9, SPEC §3.2), and
// on this screen it is usually the whole answer to "why is my margin lower".
func TestDrillDownShowsWhichLayerCarriedAFaktur(t *testing.T) {
	t.Parallel()

	sales, _ := october()
	got := compute(t, margin.Input{Period: oct, ReturnPeriod: atReturnDate, Sales: sales})

	layers := ownerLine(t, got, budi).Sales[0].Products[0].Layers
	if len(layers) != 2 {
		t.Fatalf("got %d layers, want the 2 the sale drew", len(layers))
	}
	// Oldest first: the order FIFO took them, so the drill-down reads as the
	// sequence of events it describes (SPEC §3.3).
	if !layers[0].AcquiredAt.Before(layers[1].AcquiredAt) {
		t.Error("layers are not in FIFO order")
	}
	if !layers[0].FakturReceived || layers[1].FakturReceived {
		t.Errorf("faktur status = %v/%v, want true/false",
			layers[0].FakturReceived, layers[1].FakturReceived)
	}
	mustEqual(t, "first layer draw", layers[0].Cost, 100_000)
	mustEqual(t, "second layer draw", layers[1].Cost, 24_000)
}

// R2.2: unowned stock reports as its own bucket beside the named owners, and
// last, because it is not one of the family.
func TestCompanyBucketIsALineNotAResidual(t *testing.T) {
	t.Parallel()

	sales, returns := october()
	got := compute(t, margin.Input{Period: oct, ReturnPeriod: atReturnDate, Sales: sales, Returns: returns})

	if len(got.Owners) != 3 {
		t.Fatalf("got %d owner lines, want Budi, Sari and the company", len(got.Owners))
	}
	last := got.Owners[len(got.Owners)-1]
	if !last.IsCompany || last.OwnerID != margin.Company {
		t.Errorf("last line is %s (company=%v), want the company bucket", last.OwnerName, last.IsCompany)
	}
	if got.Owners[0].OwnerName != "Budi" || got.Owners[1].OwnerName != "Sari" {
		t.Errorf("named owners are %s, %s; want them in name order",
			got.Owners[0].OwnerName, got.Owners[1].OwnerName)
	}
	// And it drills down like any other line -- no summary-only views.
	if len(last.Sales) != 1 || len(last.Sales[0].Products[0].Layers) != 1 {
		t.Error("the company bucket does not decompose to its layers (SPEC §4.2)")
	}
}

// SPEC §4.4 again, from the return's side: whichever rule is in force, a return
// is its own line and never disappears into revenue. "Margin is down" and
// "margin is down because half of October came back" are different answers.
func TestReturnsAreAlwaysADistinctLine(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	for _, tc := range []struct {
		rule   margin.ReturnPeriodRule
		period margin.Period
	}{
		{atReturnDate, nov},
		{atSaleDate, oct},
	} {
		t.Run(string(tc.rule), func(t *testing.T) {
			t.Parallel()

			got := compute(t, margin.Input{
				Period: tc.period, ReturnPeriod: tc.rule, Sales: sales, Returns: returns,
			})
			b := ownerLine(t, got, budi)

			if len(b.Returns) != 1 {
				t.Fatalf("got %d return rows, want 1", len(b.Returns))
			}
			ret := b.Returns[0]
			if !ret.CrossesPeriod {
				t.Error("a November return of an October sale is not flagged as crossing the period")
			}
			if ret.EffectiveDate != tc.period.From[:7]+ret.EffectiveDate[7:] {
				t.Errorf("effective date %s is outside the reported month %s", ret.EffectiveDate, tc.period)
			}
			// Netting the refund into revenue would leave the same margin and
			// hide the reason for it. Revenue must not have absorbed it.
			if b.Revenue != 180_000 && b.Revenue != 0 {
				t.Errorf("revenue = %s: a return was netted into it", b.Revenue)
			}
			mustEqual(t, "refund", ret.Refund, 30_000)
			mustEqual(t, "cost put back", ret.COGSReversed, 24_000)
		})
	}
}

// A return whose sale is outside the window must not drag the sale in with it,
// under either rule. Under AtSaleDate the November report is empty; under
// AtReturnDate it holds the return and nothing else.
func TestAReturnDoesNotDragItsSaleIntoThePeriod(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	underSaleDate := compute(t, margin.Input{Period: nov, ReturnPeriod: atSaleDate, Sales: sales, Returns: returns})
	if len(underSaleDate.Owners) != 0 {
		t.Errorf("November under AtSaleDate has %d owner lines, want none", len(underSaleDate.Owners))
	}

	underReturnDate := compute(t, margin.Input{Period: nov, ReturnPeriod: atReturnDate, Sales: sales, Returns: returns})
	b := ownerLine(t, underReturnDate, budi)
	if len(b.Sales) != 0 {
		t.Errorf("November under AtReturnDate pulled in %d sales", len(b.Sales))
	}
	mustEqual(t, "November revenue", b.Revenue, 0)
	mustEqual(t, "November margin", b.Margin(), -6_000)
}

func TestEmptyPeriodIsAnEmptyReportNotAnError(t *testing.T) {
	t.Parallel()

	got := compute(t, margin.Input{Period: margin.Month(2026, time.September), ReturnPeriod: atReturnDate})
	if got.Owners == nil {
		t.Error("owners is nil; an empty report should still marshal as []")
	}
	if got.Totals != (margin.Figures{}) {
		t.Errorf("totals = %+v, want zero", got.Totals)
	}
}

// D-012's mitigation, and the reason the decision is safe to make.
//
// Booking a November return into November keeps October — a month whose money
// was already split — from moving. The cost is that October reads as if nothing
// came back. LaterReturns closes that: October's own report says two boxes went
// out this month and came back in November, without moving a single figure.
//
// Under AtSaleDate the section is empty by construction, because the return is
// already counted in the sale's own month.
func TestOctoberSaysWhatCameBackInNovember(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	t.Run("AtReturnDate lists it as context", func(t *testing.T) {
		t.Parallel()

		got := compute(t, margin.Input{Period: oct, ReturnPeriod: atReturnDate, Sales: sales, Returns: returns})
		b := ownerLine(t, got, budi)

		if len(b.LaterReturns) != 1 {
			t.Fatalf("October shows %d later returns, want the 1 that came back in November", len(b.LaterReturns))
		}
		later := b.LaterReturns[0]
		if later.SaleBusinessDate != "2026-10-10" || later.BusinessDate != "2026-11-03" {
			t.Errorf("later return dates = sold %s, back %s", later.SaleBusinessDate, later.BusinessDate)
		}
		if later.EffectiveDate != "2026-11-03" {
			t.Errorf("effective date = %s, want the month it was actually counted in", later.EffectiveDate)
		}
		// It drills down like everything else — no summary-only views.
		if len(later.Products) != 1 || len(later.Products[0].Layers) != 1 {
			t.Error("a later return does not decompose to its layers")
		}

		// And it moves nothing. This is the whole contract.
		mustEqual(t, "October revenue", b.Revenue, 180_000)
		mustEqual(t, "October COGS", b.COGS, 124_000)
		mustEqual(t, "October return refund", b.ReturnRefund, 0)
		mustEqual(t, "October return COGS", b.ReturnCOGS, 0)
		mustEqual(t, "October margin", b.Margin(), 56_000)
		mustEqual(t, "October total margin", got.Totals.Margin(), 96_000)
	})

	t.Run("AtSaleDate has nothing to list", func(t *testing.T) {
		t.Parallel()

		got := compute(t, margin.Input{Period: oct, ReturnPeriod: atSaleDate, Sales: sales, Returns: returns})
		for _, o := range got.Owners {
			if len(o.LaterReturns) != 0 {
				t.Errorf("%s has %d later returns under AtSaleDate, where every return is counted in its sale's month",
					o.OwnerName, len(o.LaterReturns))
			}
		}
		// Sari's return came back inside October, so it is a counted return
		// under either rule and never a later one.
		if len(ownerLine(t, got, sari).Returns) != 1 {
			t.Error("an own-month return stopped being a counted return")
		}
	})

	// The same return, seen from the month it was counted in, is a real one.
	t.Run("November counts it for real", func(t *testing.T) {
		t.Parallel()

		got := compute(t, margin.Input{Period: nov, ReturnPeriod: atReturnDate, Sales: sales, Returns: returns})
		b := ownerLine(t, got, budi)
		if len(b.Returns) != 1 || len(b.LaterReturns) != 0 {
			t.Fatalf("November has %d counted and %d later returns, want 1 and 0",
				len(b.Returns), len(b.LaterReturns))
		}
		mustEqual(t, "November margin", b.Margin(), -6_000)
	})
}

// The report states the rule its figures were produced under, so no consumer
// can render a settlement figure without being able to say which rule placed
// the returns (SPEC §4.4, D-012).
func TestTheReportCarriesTheRuleItRanUnder(t *testing.T) {
	t.Parallel()

	sales, returns := october()

	for _, rule := range []margin.ReturnPeriodRule{atReturnDate, atSaleDate} {
		got := compute(t, margin.Input{Period: oct, ReturnPeriod: rule, Sales: sales, Returns: returns})
		if got.ReturnPeriod != rule {
			t.Errorf("report says %q, ran under %q", got.ReturnPeriod, rule)
		}
	}
}
