package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/aging"
	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// Reports is the ordinary reporting surface: sales, purchases, stock, and the
// two debt ledgers aged. TASKS 6.1–6.5.
//
// R5.7 is the user's own framing — reports can start simple provided the raw
// data can be pulled — so these are period roll-ups with a drill-down to the
// documents underneath, and the fidelity requirement is carried by the export
// ([Export], TASKS 6.6) instead.
//
// The exception is hutang and piutang. R5.8 says a balance without an age is
// not actionable, so those two go through internal/domain/aging, where what
// counts as overdue is a tested rule rather than a CASE expression.
//
// Everything here is a read. No transaction boundary: the history these report
// over is append-only, so there is nothing a concurrent sale can tear.
type Reports struct {
	db  *store.DB
	q   *gen.Queries
	now func() time.Time
}

// NewReports builds the service.
func NewReports(db *store.DB, now func() time.Time) *Reports {
	if now == nil {
		now = time.Now
	}
	return &Reports{db: db, q: gen.New(db), now: now}
}

// Period is the window a report covers, echoed back so no screen has to guess
// which dates produced the figures it is showing.
type Period struct {
	From string
	To   string
}

// --- sales (TASKS 6.1, R5.4) ------------------------------------------------

// SalesReport is trade for a period.
type SalesReport struct {
	Period Period

	SaleCount int64
	Gross     money.IDR
	Discount  money.IDR
	// DPP is revenue net of PPN and PPN is the tax collected on it; Total is
	// what customers actually paid. Kept apart for the same reason the margin
	// report keeps them apart: the tax was never the shop's money.
	DPP   money.IDR
	PPN   money.IDR
	Total money.IDR
	COGS  money.IDR
	// Credit is the part of Total sold on terms rather than taken at the till —
	// turnover that has not arrived yet, and the piutang report's opening
	// balance.
	Credit      money.IDR
	WithFaktur  money.IDR
	VoidCount   int64
	VoidTotal   money.IDR
	ReturnCount int64
	Refund      money.IDR
	RefundPPN   money.IDR
	ReturnCOGS  money.IDR

	ByDay     []gen.SalesByDayRow
	ByProduct []gen.SalesByProductRow
	ByMethod  []gen.SalesByPaymentMethodRow
}

// GrossMargin is revenue less cost, before returns.
//
// Labelled margin, never profit: shared costs are out of scope and settled
// outside this application (REQUIREMENTS §5), so this figure has not paid rent.
func (r SalesReport) GrossMargin() money.IDR { return r.DPP.Sub(r.COGS) }

// NetMargin is the same after the period's returns.
func (r SalesReport) NetMargin() money.IDR {
	return r.GrossMargin().Sub(r.Refund.Sub(r.RefundPPN)).Add(r.ReturnCOGS)
}

// Sales reports trade over a period. An empty from or to means the current
// month in the company's own timezone (INV-5).
func (r *Reports) Sales(ctx context.Context, entityID, from, to string) (SalesReport, error) {
	period, err := r.resolvePeriod(ctx, entityID, from, to)
	if err != nil {
		return SalesReport{}, err
	}
	arg := gen.SalesSummaryParams{EntityID: entityID, FromDate: period.From, ToDate: period.To}

	sum, err := r.q.SalesSummary(ctx, arg)
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: sales summary: %w", err)
	}
	voids, err := r.q.SalesVoidSummary(ctx, gen.SalesVoidSummaryParams(arg))
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: void summary: %w", err)
	}
	returns, err := r.q.SalesReturnSummary(ctx, gen.SalesReturnSummaryParams(arg))
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: return summary: %w", err)
	}
	byDay, err := r.q.SalesByDay(ctx, gen.SalesByDayParams(arg))
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: sales by day: %w", err)
	}
	byProduct, err := r.q.SalesByProduct(ctx, gen.SalesByProductParams(arg))
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: sales by product: %w", err)
	}
	byMethod, err := r.q.SalesByPaymentMethod(ctx, gen.SalesByPaymentMethodParams(arg))
	if err != nil {
		return SalesReport{}, fmt.Errorf("service: sales by method: %w", err)
	}

	return SalesReport{
		Period:    period,
		SaleCount: sum.SaleCount,
		Gross:     money.IDR(sum.GrossIdr), Discount: money.IDR(sum.DiscountIdr),
		DPP: money.IDR(sum.DppIdr), PPN: money.IDR(sum.PpnIdr),
		Total: money.IDR(sum.TotalIdr), COGS: money.IDR(sum.CogsIdr),
		Credit: money.IDR(sum.CreditIdr), WithFaktur: money.IDR(sum.WithFakturIdr),
		VoidCount: voids.VoidCount, VoidTotal: money.IDR(voids.TotalIdr),
		ReturnCount: returns.ReturnCount, Refund: money.IDR(returns.RefundIdr),
		RefundPPN: money.IDR(returns.PpnReversedIdr), ReturnCOGS: money.IDR(returns.CogsReversedIdr),
		ByDay: byDay, ByProduct: byProduct, ByMethod: byMethod,
	}, nil
}

// --- purchases (TASKS 6.2, R5.3) --------------------------------------------

// PurchasesReport is buying for a period.
type PurchasesReport struct {
	Period Period

	PurchaseCount int64
	Subtotal      money.IDR
	PPN           money.IDR
	Total         money.IDR
	WithFaktur    money.IDR
	WithoutFaktur money.IDR
	// PPNIntoCost is PPN paid on purchases no faktur arrived for. It is not
	// creditable and went into the cost of the goods instead (SPEC §3.2,
	// INV-9) — which is the figure this business cannot see today, and the
	// reason purchase tracking is worth building at all.
	PPNIntoCost money.IDR

	ReturnCount int64
	ReturnCost  money.IDR
	ReturnPPN   money.IDR

	BySupplier []gen.PurchasesBySupplierRow
	ByProduct  []gen.PurchasesByProductRow
}

// Purchases reports buying over a period.
func (r *Reports) Purchases(ctx context.Context, entityID, from, to string) (PurchasesReport, error) {
	period, err := r.resolvePeriod(ctx, entityID, from, to)
	if err != nil {
		return PurchasesReport{}, err
	}
	arg := gen.PurchasesSummaryParams{EntityID: entityID, FromDate: period.From, ToDate: period.To}

	sum, err := r.q.PurchasesSummary(ctx, arg)
	if err != nil {
		return PurchasesReport{}, fmt.Errorf("service: purchases summary: %w", err)
	}
	returns, err := r.q.PurchaseReturnSummary(ctx, gen.PurchaseReturnSummaryParams(arg))
	if err != nil {
		return PurchasesReport{}, fmt.Errorf("service: purchase return summary: %w", err)
	}
	bySupplier, err := r.q.PurchasesBySupplier(ctx, gen.PurchasesBySupplierParams(arg))
	if err != nil {
		return PurchasesReport{}, fmt.Errorf("service: purchases by supplier: %w", err)
	}
	byProduct, err := r.q.PurchasesByProduct(ctx, gen.PurchasesByProductParams(arg))
	if err != nil {
		return PurchasesReport{}, fmt.Errorf("service: purchases by product: %w", err)
	}

	return PurchasesReport{
		Period:        period,
		PurchaseCount: sum.PurchaseCount,
		Subtotal:      money.IDR(sum.SubtotalIdr), PPN: money.IDR(sum.PpnIdr),
		Total:         money.IDR(sum.TotalIdr),
		WithFaktur:    money.IDR(sum.WithFakturIdr),
		WithoutFaktur: money.IDR(sum.WithoutFakturIdr),
		PPNIntoCost:   money.IDR(sum.PpnIntoCostIdr),
		ReturnCount:   returns.ReturnCount,
		ReturnCost:    money.IDR(returns.CostIdr), ReturnPPN: money.IDR(returns.PpnReversedIdr),
		BySupplier: bySupplier, ByProduct: byProduct,
	}, nil
}

// --- stock (TASKS 6.3, R5.2) ------------------------------------------------

// StockReport is what is on the shelf and what moved.
type StockReport struct {
	Period Period

	// Value is stock at cost — the remaining slice of each layer, not a
	// quantity times a rounded unit cost (SPEC §1).
	Value    money.IDR
	Lines    int64
	QtyTotal int64

	OnHand     []gen.StockOnHandRow
	OutOfStock []gen.StockOutOfStockRow
	Movement   []gen.StockMovementByProductRow
	Intake     []gen.StockIntakeByProductRow
}

// Stock reports the shelf now and its movement over a period.
//
// On-hand is as of now and not as of the period end, deliberately: this system
// does not reconstruct historical balances, and a figure labelled "stock on 31
// August" that was actually today's would be worse than one honestly labelled.
func (r *Reports) Stock(ctx context.Context, entityID, from, to string) (StockReport, error) {
	period, err := r.resolvePeriod(ctx, entityID, from, to)
	if err != nil {
		return StockReport{}, err
	}

	onHand, err := r.q.StockOnHand(ctx, entityID)
	if err != nil {
		return StockReport{}, fmt.Errorf("service: stock on hand: %w", err)
	}
	outOfStock, err := r.q.StockOutOfStock(ctx, entityID)
	if err != nil {
		return StockReport{}, fmt.Errorf("service: out of stock: %w", err)
	}
	movement, err := r.q.StockMovementByProduct(ctx, gen.StockMovementByProductParams{
		EntityID: entityID, FromDate: period.From, ToDate: period.To,
	})
	if err != nil {
		return StockReport{}, fmt.Errorf("service: stock movement: %w", err)
	}
	intake, err := r.q.StockIntakeByProduct(ctx, gen.StockIntakeByProductParams{
		EntityID: entityID, FromDate: period.From, ToDate: period.To,
	})
	if err != nil {
		return StockReport{}, fmt.Errorf("service: stock intake: %w", err)
	}

	out := StockReport{
		Period: period, OnHand: onHand, OutOfStock: outOfStock,
		Movement: movement, Intake: intake,
		Lines: int64(len(onHand)),
	}
	for _, row := range onHand {
		out.Value = out.Value.Add(money.IDR(row.ValueIdr))
		out.QtyTotal += row.QtyOnHand
	}
	return out, nil
}

// --- hutang and piutang (TASKS 6.4–6.5, R5.5–5.6, R5.8) ---------------------

// Payables ages hutang as of a day. An empty asOf means today in the company's
// own timezone (INV-5).
func (r *Reports) Payables(ctx context.Context, entityID, asOf string) (aging.Report, error) {
	day, err := r.resolveDay(ctx, entityID, asOf)
	if err != nil {
		return aging.Report{}, err
	}

	rows, err := r.q.OutstandingPayablesForAging(ctx, entityID)
	if err != nil {
		return aging.Report{}, fmt.Errorf("service: outstanding payables: %w", err)
	}

	items := make([]aging.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, aging.Item{
			ID: row.ID, CounterpartyID: row.CounterpartyID, CounterpartyName: row.CounterpartyName,
			DocumentNo: derefString(row.InvoiceNo), Source: row.Source,
			IncurredOn: row.IncurredOn, DueDate: derefString(row.DueDate),
			Amount: money.IDR(row.AmountIdr), Paid: money.IDR(row.PaidIdr),
			Outstanding: money.IDR(row.OutstandingIdr),
		})
	}

	return ageOrRefuse(aging.Input{Kind: aging.Hutang, AsOf: day, Items: items})
}

// Receivables ages piutang as of a day.
func (r *Reports) Receivables(ctx context.Context, entityID, asOf string) (aging.Report, error) {
	day, err := r.resolveDay(ctx, entityID, asOf)
	if err != nil {
		return aging.Report{}, err
	}

	rows, err := r.q.OutstandingReceivablesForAging(ctx, entityID)
	if err != nil {
		return aging.Report{}, fmt.Errorf("service: outstanding receivables: %w", err)
	}

	items := make([]aging.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, aging.Item{
			ID: row.ID, CounterpartyID: row.CounterpartyID, CounterpartyName: row.CounterpartyName,
			DocumentNo: derefString(row.InvoiceNo), Source: row.Source,
			IncurredOn: row.IncurredOn, DueDate: derefString(row.DueDate),
			Amount: money.IDR(row.AmountIdr), Paid: money.IDR(row.PaidIdr),
			Outstanding: money.IDR(row.OutstandingIdr),
		})
	}

	return ageOrRefuse(aging.Input{Kind: aging.Piutang, AsOf: day, Items: items})
}

// ageOrRefuse turns a domain refusal into something the transport layer can
// classify.
//
// A balance that disagrees with the payments beneath it is a books problem, not
// a bad request: the message names the document to go and look at, and the
// report refuses rather than chasing somebody for a figure it cannot stand
// behind.
func ageOrRefuse(in aging.Input) (aging.Report, error) {
	report, err := aging.Age(in)
	if err != nil {
		return aging.Report{}, fmt.Errorf("%w: %w", ErrReportIntegrity, err)
	}
	return report, nil
}

// --- shared -----------------------------------------------------------------

// resolvePeriod defaults an unset window to the current month in the company's
// own timezone (INV-5), which is the window trade is actually read over.
func (r *Reports) resolvePeriod(ctx context.Context, entityID, from, to string) (Period, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)

	if from == "" || to == "" {
		day, err := r.resolveDay(ctx, entityID, "")
		if err != nil {
			return Period{}, err
		}
		today, err := time.Parse(margin.DateFormat, day)
		if err != nil {
			return Period{}, fmt.Errorf("%w: %w", ErrValidation, err)
		}
		current := margin.Month(today.Year(), today.Month())
		if from == "" {
			from = current.From
		}
		if to == "" {
			to = current.To
		}
	}

	period := margin.Period{From: from, To: to}
	if err := period.Validate(); err != nil {
		return Period{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return Period{From: period.From, To: period.To}, nil
}

// resolveDay defaults an unset date to today in the company's own timezone.
func (r *Reports) resolveDay(ctx context.Context, entityID, day string) (string, error) {
	day = strings.TrimSpace(day)
	if day != "" {
		if _, err := time.Parse(aging.DateFormat, day); err != nil {
			return "", fmt.Errorf("%w: tanggal harus YYYY-MM-DD", ErrValidation)
		}
		return day, nil
	}

	entity, err := r.q.GetLegalEntity(ctx, entityID)
	if err != nil {
		return "", fmt.Errorf("%w: perusahaan tidak ditemukan", ErrNotFound)
	}
	loc, err := time.LoadLocation(entity.Timezone)
	if err != nil {
		return "", fmt.Errorf("%w: zona waktu perusahaan %q tidak dikenal",
			ErrValidation, entity.Timezone)
	}
	return r.now().In(loc).Format(aging.DateFormat), nil
}
