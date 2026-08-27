package margin

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
)

// Figures are the four raw numbers every level of the report is built from.
//
// Returns are kept apart from sales rather than netted into them. "Your margin
// is down" and "your margin is down because half of last month came back" are
// different answers, and only one of them is actionable (SPEC §4.4).
type Figures struct {
	// Revenue is what the goods sold for, net of discounts and net of PPN,
	// before any return. See [SaleLine.Revenue] for why the tax comes out.
	Revenue money.IDR
	// PPN is the output PPN collected alongside that revenue. Reported, never
	// added to margin: it is owed to the state (SPEC §2.4).
	PPN money.IDR
	// COGS is the sum of the actual layer draws behind that revenue.
	COGS money.IDR

	// ReturnRefund, ReturnPPN and ReturnCOGS are positive magnitudes: money
	// handed back, how much of it was PPN, and the cost taken off the books
	// with it.
	ReturnRefund money.IDR
	ReturnPPN    money.IDR
	ReturnCOGS   money.IDR
}

// Tendered is what customers actually handed over: revenue plus the PPN inside
// it.
//
// On the report so the owner can reconcile a margin line against a day's
// takings without doing the arithmetic themselves. Under inclusive pricing this
// is the sum of the shelf prices; under exclusive it is the base plus the tax
// added at the till. Either way it is not margin.
func (f Figures) Tendered() money.IDR { return f.Revenue.Add(f.PPN) }

// GrossMargin is revenue less cost, before returns.
func (f Figures) GrossMargin() money.IDR { return f.Revenue.Sub(f.COGS) }

// ReturnRevenue is the revenue the returns took back out: the refund less the
// PPN inside it. The tax goes back to the state's column, not the owner's.
func (f Figures) ReturnRevenue() money.IDR { return f.ReturnRefund.Sub(f.ReturnPPN) }

// ReturnMargin is the margin the returns took back out. Positive means margin
// was removed.
func (f Figures) ReturnMargin() money.IDR { return f.ReturnRevenue().Sub(f.ReturnCOGS) }

// NetRevenue is revenue after refunds.
func (f Figures) NetRevenue() money.IDR { return f.Revenue.Sub(f.ReturnRevenue()) }

// NetCOGS is cost after the cost the returns put back.
func (f Figures) NetCOGS() money.IDR { return f.COGS.Sub(f.ReturnCOGS) }

// Margin is the figure money is settled on: net revenue less net cost.
func (f Figures) Margin() money.IDR { return f.NetRevenue().Sub(f.NetCOGS()) }

// HasReturns reports whether any goods came back in this period.
func (f Figures) HasReturns() bool { return !f.ReturnRefund.IsZero() || !f.ReturnCOGS.IsZero() }

func (f *Figures) addSale(revenue, ppn, cogs money.IDR) {
	f.Revenue = f.Revenue.Add(revenue)
	f.PPN = f.PPN.Add(ppn)
	f.COGS = f.COGS.Add(cogs)
}

func (f *Figures) addReturn(refund, ppn, cogs money.IDR) {
	f.ReturnRefund = f.ReturnRefund.Add(refund)
	f.ReturnPPN = f.ReturnPPN.Add(ppn)
	f.ReturnCOGS = f.ReturnCOGS.Add(cogs)
}

// LayerDraw is the bottom of the drill-down: one slice of one FIFO layer, and
// what it cost (SPEC §4.2).
type LayerDraw struct {
	ConsumptionID string
	LayerID       string

	// Qty and Cost are positive on a sale draw and positive magnitudes on a
	// return's reversal, with IsReversal saying which this is. The signed form
	// lives on the stock_consumption row; the report reads left to right.
	Qty  int64
	Cost money.IDR

	IsReversal bool

	AcquiredAt time.Time
	Source     string

	// LayerQtyIn and LayerCostTotal are the whole layer this slice came out of,
	// so the drill-down can show the arithmetic rather than assert it: 3 of 7
	// units from a Rp 100.000 layer.
	LayerQtyIn     int64
	LayerCostTotal money.IDR

	// FakturReceived is why two identical purchases can carry different costs
	// (INV-9, SPEC §3.2). Shown here because this is where the question
	// "why is my margin lower" actually gets answered.
	FakturReceived bool
}

// ProductLine groups one product within one sale or one return, with the layers
// behind it.
//
// On a sale, Revenue and COGS are what the goods earned net of PPN and what
// they cost. On a return they are the revenue given back and the cost put back,
// both positive magnitudes. PPN is the tax either collected or handed back
// alongside, and is never part of the margin.
type ProductLine struct {
	ProductID   string
	ProductCode string
	ProductName string

	Qty     int64
	Revenue money.IDR
	PPN     money.IDR
	COGS    money.IDR

	Layers []LayerDraw
}

// Margin is this product's contribution.
func (p ProductLine) Margin() money.IDR { return p.Revenue.Sub(p.COGS) }

// SaleReport is one sale as it appears under one owner.
//
// Revenue and COGS are this owner's slice of the sale, not the invoice total. A
// cart holding Budi's gloves and Sari's syringes appears under both, each
// showing only their own lines — which is the only way the owner totals can
// add up.
type SaleReport struct {
	SaleID       string
	InvoiceNo    string
	BusinessDate string
	OccurredAt   time.Time
	CustomerName string

	Revenue money.IDR
	PPN     money.IDR
	COGS    money.IDR

	Products []ProductLine
}

// Tendered is what the customer handed over for this owner's share of the sale.
func (s SaleReport) Tendered() money.IDR { return s.Revenue.Add(s.PPN) }

// Margin is this owner's margin on this sale.
func (s SaleReport) Margin() money.IDR { return s.Revenue.Sub(s.COGS) }

// ReturnReport is one return as it appears under one owner.
type ReturnReport struct {
	ReturnID      string
	SaleID        string
	SaleInvoiceNo string

	// BusinessDate is when the goods came back, SaleBusinessDate when they were
	// sold, and EffectiveDate is the one the rule in force actually counted.
	// All three are on the row so nobody has to reconstruct which rule ran.
	BusinessDate     string
	SaleBusinessDate string
	EffectiveDate    string
	// CrossesPeriod marks the rows SPEC §4.4 is about: goods sold in one month
	// and returned in another.
	CrossesPeriod bool

	Reason string

	// Refund is the whole sum handed back to the customer, PPN included -- what
	// the document says and what left the till. PPNReversed is the tax inside
	// it, which goes back to the state's column rather than the owner's.
	Refund       money.IDR
	PPNReversed  money.IDR
	COGSReversed money.IDR

	Products []ProductLine
}

// RevenueReversed is the revenue this return took back: the refund less its PPN.
func (r ReturnReport) RevenueReversed() money.IDR { return r.Refund.Sub(r.PPNReversed) }

// Margin is the margin this return took back out.
func (r ReturnReport) Margin() money.IDR { return r.RevenueReversed().Sub(r.COGSReversed) }

// OwnerReport is one family member's line on the report — or the company
// bucket — with everything behind it.
type OwnerReport struct {
	OwnerID   OwnerID
	OwnerName string
	// IsCompany marks the unowned-stock bucket (R2.2). A real line, not a
	// residual: some stock genuinely belongs to the company rather than to a
	// family member, and it settles differently.
	IsCompany bool

	Figures

	Sales   []SaleReport
	Returns []ReturnReport

	// LaterReturns are goods sold in this period that came back in another one
	// and were counted there — informational, and excluded from every figure
	// above.
	//
	// This is the mitigation for D-012. Under [AtReturnDate] a return of an
	// October sale accepted in November reduces November, which keeps a settled
	// month from moving but leaves October reading as if nothing came back.
	// Listing them here means "Budi's October included two boxes that came
	// back" is answerable from October's report rather than only from
	// November's. Under [AtSaleDate] this is empty by construction: the return
	// is already counted in the sale's own month.
	LaterReturns []ReturnReport
}

// Report is the whole of Laporan Margin per Owner for one period.
//
// Never Laba Rugi. Shared costs — listrik, gaji, sewa — are out of scope and
// settled outside the application (REQUIREMENTS §5), so this has not paid rent.
type Report struct {
	Period Period
	// ReturnPeriod is the rule the figures below were produced under. It rides
	// on the report rather than living only in configuration, so no consumer
	// can render a settlement figure without being able to say which rule
	// placed the returns (SPEC §4.4, D-012).
	ReturnPeriod ReturnPeriodRule

	// Owners are the named owners in name order, with the company bucket last.
	Owners []OwnerReport

	Totals Figures
}

// Input is everything Compute needs. Sales and returns arrive unfiltered; the
// period and the policy decide which of them count.
type Input struct {
	Period Period
	// ReturnPeriod is required. [ReturnPeriodUnset] is an error, not a default:
	// which period a cross-boundary return lands in decides whose money moves
	// and when, so this package will not choose it (SPEC §4.4).
	ReturnPeriod ReturnPeriodRule

	// OwnerNames must name every owner appearing in the data, including
	// [Company]. Naming is the caller's job because the display string for the
	// company bucket is a UI decision, and this package holds no UI text.
	OwnerNames map[OwnerID]string

	// Sales must contain only finalised sales. See [Sale].
	Sales   []Sale
	Returns []Return
}

// Compute builds the report.
//
// Every figure it produces decomposes into the rows underneath it, all the way
// to individual layer draws, because that is a hard requirement rather than a
// nicety (SPEC §4.2). It refuses on any disagreement between the two
// independent attribution paths — revenue from the sale line, cost from the
// stock layer — rather than picking one and printing a number.
func Compute(in Input) (Report, error) {
	if err := in.Period.Validate(); err != nil {
		return Report{}, err
	}
	if err := in.ReturnPeriod.Validate(); err != nil {
		return Report{}, err
	}

	acc := newAccumulator(in.OwnerNames)

	for _, sale := range in.Sales {
		if !in.Period.Contains(sale.BusinessDate) {
			continue
		}
		if err := acc.addSale(sale); err != nil {
			return Report{}, err
		}
	}

	for _, ret := range in.Returns {
		switch counted := in.Period.Contains(ret.EffectiveDate(in.ReturnPeriod)); {
		case counted:
			if err := acc.addReturn(ret, in.ReturnPeriod); err != nil {
				return Report{}, err
			}
		case in.Period.Contains(ret.SaleBusinessDate):
			// Sold in this period, counted in another one. Reported as context,
			// never in a total — see [OwnerReport.LaterReturns].
			if err := acc.addLaterReturn(ret, in.ReturnPeriod); err != nil {
				return Report{}, err
			}
		}
	}

	owners, totals := acc.finish()
	return Report{
		Period: in.Period, ReturnPeriod: in.ReturnPeriod,
		Owners: owners, Totals: totals,
	}, nil
}

// --- accumulation -----------------------------------------------------------

type accumulator struct {
	names  map[OwnerID]string
	owners map[OwnerID]*OwnerReport
}

func newAccumulator(names map[OwnerID]string) *accumulator {
	return &accumulator{names: names, owners: make(map[OwnerID]*OwnerReport)}
}

func (a *accumulator) bucket(id OwnerID) (*OwnerReport, error) {
	if o, ok := a.owners[id]; ok {
		return o, nil
	}
	name, ok := a.names[id]
	if !ok || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: %s", ErrUnknownOwner, id)
	}
	o := &OwnerReport{OwnerID: id, OwnerName: name, IsCompany: id.IsCompany()}
	a.owners[id] = o
	return o, nil
}

func (a *accumulator) addSale(sale Sale) error {
	where := fmt.Sprintf("sale %s (%s)", sale.ID, sale.InvoiceNo)

	products, err := groupProducts(sale.Draws, sale.Lines, nil, doc{where: where, saleID: sale.ID, invoiceNo: sale.InvoiceNo})
	if err != nil {
		return err
	}

	byOwner := make(map[OwnerID][]ProductLine, 2)
	for _, p := range products {
		byOwner[p.owner] = append(byOwner[p.owner], p.line)
	}

	for owner, lines := range byOwner {
		bucket, err := a.bucket(owner)
		if err != nil {
			return err
		}
		sortProducts(lines)

		var revenue, ppn, cogs money.IDR
		for _, l := range lines {
			revenue = revenue.Add(l.Revenue)
			ppn = ppn.Add(l.PPN)
			cogs = cogs.Add(l.COGS)
		}

		bucket.Sales = append(bucket.Sales, SaleReport{
			SaleID: sale.ID, InvoiceNo: sale.InvoiceNo, BusinessDate: sale.BusinessDate,
			OccurredAt: sale.OccurredAt, CustomerName: sale.CustomerName,
			Revenue: revenue, PPN: ppn, COGS: cogs, Products: lines,
		})
		bucket.addSale(revenue, ppn, cogs)
	}
	return nil
}

func (a *accumulator) addReturn(ret Return, rule ReturnPeriodRule) error {
	return a.foldReturn(ret, rule, true)
}

// addLaterReturn records a return against a sale in this period that was
// counted in a different one. Context only: it touches no figure.
func (a *accumulator) addLaterReturn(ret Return, rule ReturnPeriodRule) error {
	return a.foldReturn(ret, rule, false)
}

// foldReturn splits one return across the owners it belongs to. counted says
// whether it moves the figures or is only being shown.
func (a *accumulator) foldReturn(ret Return, rule ReturnPeriodRule, counted bool) error {
	where := fmt.Sprintf("return %s against sale %s (%s)", ret.ID, ret.SaleID, ret.SaleInvoiceNo)

	products, err := groupProducts(ret.Draws, nil, ret.Lines, doc{where: where, saleID: ret.SaleID, invoiceNo: ret.SaleInvoiceNo})
	if err != nil {
		return err
	}

	byOwner := make(map[OwnerID][]ProductLine, 2)
	for _, p := range products {
		byOwner[p.owner] = append(byOwner[p.owner], p.line)
	}

	for owner, lines := range byOwner {
		bucket, err := a.bucket(owner)
		if err != nil {
			return err
		}
		sortProducts(lines)

		var revenue, ppn, cogs money.IDR
		for _, l := range lines {
			revenue = revenue.Add(l.Revenue)
			ppn = ppn.Add(l.PPN)
			cogs = cogs.Add(l.COGS)
		}

		row := ReturnReport{
			ReturnID: ret.ID, SaleID: ret.SaleID, SaleInvoiceNo: ret.SaleInvoiceNo,
			BusinessDate: ret.BusinessDate, SaleBusinessDate: ret.SaleBusinessDate,
			EffectiveDate: ret.EffectiveDate(rule), CrossesPeriod: ret.CrossesPeriod(),
			Reason: ret.Reason, Refund: revenue.Add(ppn), PPNReversed: ppn,
			COGSReversed: cogs, Products: lines,
		}

		if !counted {
			bucket.LaterReturns = append(bucket.LaterReturns, row)
			continue
		}
		bucket.Returns = append(bucket.Returns, row)
		bucket.addReturn(row.Refund, ppn, cogs)
	}
	return nil
}

func (a *accumulator) finish() ([]OwnerReport, Figures) {
	owners := make([]OwnerReport, 0, len(a.owners))
	var totals Figures

	for _, o := range a.owners {
		sortSales(o.Sales)
		sortReturns(o.Returns)
		sortReturns(o.LaterReturns)
		owners = append(owners, *o)

		totals.addSale(o.Revenue, o.PPN, o.COGS)
		totals.addReturn(o.ReturnRefund, o.ReturnPPN, o.ReturnCOGS)
	}

	// Named owners by name, company bucket last. The company line is not a
	// person and reads as a footnote to the family's figures, not as one of
	// them.
	sort.SliceStable(owners, func(i, j int) bool {
		switch {
		case owners[i].IsCompany != owners[j].IsCompany:
			return owners[j].IsCompany
		case owners[i].OwnerName != owners[j].OwnerName:
			return owners[i].OwnerName < owners[j].OwnerName
		default:
			return owners[i].OwnerID < owners[j].OwnerID
		}
	})

	return owners, totals
}

// --- grouping ---------------------------------------------------------------

// grouped is one product's aggregation within one document, with the owner both
// sides agreed on.
type grouped struct {
	owner OwnerID
	line  ProductLine
}

// doc is what an error message needs to point a person at the paperwork: the
// document being folded, and the sale it ultimately concerns.
type doc struct {
	where     string
	saleID    string
	invoiceNo string
}

// groupProducts folds a document's draws and its lines into one row per
// product, checking as it goes that the two agree.
//
// Exactly one of saleLines and returnLines is non-nil. Both sides carry an
// owner and a cost, written at different times by different people; this is
// where they are made to match. Attribution is by product rather than by a
// stored line reference because that is what the stock_consumption row can
// support — it names the movement and the layer, and the layer carries the
// product and the owner (SPEC §3.1).
func groupProducts(draws []Draw, saleLines []SaleLine, returnLines []ReturnLine, d doc) ([]grouped, error) {
	type agg struct {
		owner     OwnerID
		ownerSeen bool
		line      ProductLine
		recorded  money.IDR
		drawn     money.IDR
		hasDraws  bool
	}

	order := make([]string, 0, 4)
	byProduct := make(map[string]*agg, 4)

	at := func(productID string) *agg {
		if g, ok := byProduct[productID]; ok {
			return g
		}
		g := &agg{line: ProductLine{ProductID: productID}}
		byProduct[productID] = g
		order = append(order, productID)
		return g
	}

	for _, l := range saleLines {
		if l.Qty <= 0 {
			return nil, fmt.Errorf("%w: %s line %s has quantity %d", ErrInvalidRecord, d.where, l.ID, l.Qty)
		}
		g := at(l.ProductID)
		g.owner, g.ownerSeen = l.OwnerID, true
		g.line.ProductCode, g.line.ProductName = l.ProductCode, l.ProductName
		g.line.Qty += l.Qty
		g.line.Revenue = g.line.Revenue.Add(l.Revenue)
		g.line.PPN = g.line.PPN.Add(l.PPN)
		g.recorded = g.recorded.Add(l.RecordedCOGS)
	}

	for _, l := range returnLines {
		if l.Qty <= 0 {
			return nil, fmt.Errorf("%w: %s line %s has quantity %d", ErrInvalidRecord, d.where, l.ID, l.Qty)
		}
		g := at(l.ProductID)
		g.owner, g.ownerSeen = l.OwnerID, true
		g.line.ProductCode, g.line.ProductName = l.ProductCode, l.ProductName
		g.line.Qty += l.Qty
		// The revenue given back, with the tax kept apart. Adding the whole
		// refund here would reverse more margin than the sale ever created.
		g.line.Revenue = g.line.Revenue.Add(l.Refund.Sub(l.PPNReversed))
		g.line.PPN = g.line.PPN.Add(l.PPNReversed)
		g.recorded = g.recorded.Add(l.COGSReversed)
	}

	for _, dr := range draws {
		if err := dr.validate(d.where); err != nil {
			return nil, err
		}
		g, known := byProduct[dr.ProductID]
		if !known {
			return nil, fmt.Errorf("%w: %s drew layer %s of product %s, which no line sold",
				ErrOrphanDraw, d.where, dr.LayerID, dr.ProductID)
		}
		if g.ownerSeen && g.owner != dr.OwnerID {
			return nil, &OwnerMismatchError{
				SaleID: d.saleID, InvoiceNo: d.invoiceNo, ProductID: dr.ProductID,
				LineOwner: g.owner, LayerOwner: dr.OwnerID,
			}
		}

		// A sale draw carries a positive cost, a return's reversal a negative
		// one. Both are reported as magnitudes; the sign lives on the stored
		// row and is checked by Draw.validate.
		qty, cost := dr.QtyOut, dr.Cost
		if dr.IsReversal() {
			qty, cost = -qty, cost.Neg()
		}

		g.hasDraws = true
		g.drawn = g.drawn.Add(cost)
		g.line.COGS = g.line.COGS.Add(cost)
		g.line.Layers = append(g.line.Layers, LayerDraw{
			ConsumptionID: dr.ID, LayerID: dr.LayerID, Qty: qty, Cost: cost,
			IsReversal: dr.IsReversal(), AcquiredAt: dr.LayerAcquiredAt, Source: dr.LayerSource,
			LayerQtyIn: dr.LayerQtyIn, LayerCostTotal: dr.LayerCostTotal,
			FakturReceived: dr.FakturReceived,
		})
	}

	out := make([]grouped, 0, len(order))
	for _, productID := range order {
		g := byProduct[productID]

		// The header figure and the layer rows are written in one transaction
		// from the same numbers. If they have since diverged, the report says
		// so instead of printing whichever it happened to read.
		if g.drawn != g.recorded {
			return nil, fmt.Errorf("%w: %s product %s records %s but the layers drawn total %s",
				ErrCOGSMismatch, d.where, productID, g.recorded, g.drawn)
		}
		if !g.hasDraws && !g.recorded.IsZero() {
			return nil, fmt.Errorf("%w: %s product %s records %s against no layers at all",
				ErrCOGSMismatch, d.where, productID, g.recorded)
		}

		sortLayers(g.line.Layers)
		out = append(out, grouped{owner: g.owner, line: g.line})
	}
	return out, nil
}

// --- ordering ---------------------------------------------------------------

func sortProducts(lines []ProductLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].ProductCode != lines[j].ProductCode {
			return lines[i].ProductCode < lines[j].ProductCode
		}
		return lines[i].ProductID < lines[j].ProductID
	})
}

// sortLayers puts the draws in the order FIFO took them: oldest layer first,
// ties broken by id, which is insertion order because ids are UUIDv7 (D-003).
// The drill-down then reads as the sequence of events it describes.
func sortLayers(layers []LayerDraw) {
	sort.SliceStable(layers, func(i, j int) bool {
		if c := layers[i].AcquiredAt.Compare(layers[j].AcquiredAt); c != 0 {
			return c < 0
		}
		if layers[i].LayerID != layers[j].LayerID {
			return layers[i].LayerID < layers[j].LayerID
		}
		return layers[i].ConsumptionID < layers[j].ConsumptionID
	})
}

func sortSales(sales []SaleReport) {
	sort.SliceStable(sales, func(i, j int) bool {
		if sales[i].BusinessDate != sales[j].BusinessDate {
			return sales[i].BusinessDate < sales[j].BusinessDate
		}
		if sales[i].InvoiceNo != sales[j].InvoiceNo {
			return sales[i].InvoiceNo < sales[j].InvoiceNo
		}
		return sales[i].SaleID < sales[j].SaleID
	})
}

func sortReturns(returns []ReturnReport) {
	sort.SliceStable(returns, func(i, j int) bool {
		if returns[i].EffectiveDate != returns[j].EffectiveDate {
			return returns[i].EffectiveDate < returns[j].EffectiveDate
		}
		return returns[i].ReturnID < returns[j].ReturnID
	})
}
