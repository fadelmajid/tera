package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/aging"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
)

// --- shared -----------------------------------------------------------------

func periodBody(p service.Period) map[string]any {
	return map[string]any{"from": p.From, "to": p.To}
}

func window(r *stdhttp.Request) (from, to string) {
	q := r.URL.Query()
	return q.Get("from"), q.Get("to")
}

// --- sales (TASKS 6.1, R5.4) ------------------------------------------------

func handleSalesReport(rep *service.Reports) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		from, to := window(r)

		got, err := rep.Sales(r.Context(), entityID, from, to)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		byDay := make([]map[string]any, 0, len(got.ByDay))
		for _, d := range got.ByDay {
			byDay = append(byDay, map[string]any{
				"business_date": d.BusinessDate, "sale_count": d.SaleCount,
				"dpp_idr": money.IDR(d.DppIdr), "ppn_idr": money.IDR(d.PpnIdr),
				"total_idr": money.IDR(d.TotalIdr), "cogs_idr": money.IDR(d.CogsIdr),
				"margin_idr": money.IDR(d.DppIdr - d.CogsIdr),
			})
		}
		byProduct := make([]map[string]any, 0, len(got.ByProduct))
		for _, p := range got.ByProduct {
			byProduct = append(byProduct, map[string]any{
				"product_id": p.ProductID, "product_code": p.ProductCode,
				"product_name": p.ProductName, "product_unit": p.ProductUnit,
				"owner_id": p.OwnerID, "owner_name": p.OwnerName,
				"qty": p.Qty, "revenue_idr": money.IDR(p.RevenueIdr),
				"ppn_idr": money.IDR(p.PpnIdr), "cogs_idr": money.IDR(p.CogsIdr),
				"margin_idr": money.IDR(p.RevenueIdr - p.CogsIdr),
			})
		}
		byMethod := make([]map[string]any, 0, len(got.ByMethod))
		for _, m := range got.ByMethod {
			byMethod = append(byMethod, map[string]any{
				"method": m.Method, "payment_count": m.PaymentCount,
				"amount_idr": money.IDR(m.AmountIdr),
			})
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"title":  "Laporan Penjualan",
			"period": periodBody(got.Period),
			"summary": map[string]any{
				"sale_count": got.SaleCount,
				"gross_idr":  got.Gross, "discount_idr": got.Discount,
				// Revenue and the tax on it stay apart: the PPN is owed to the
				// state and was never the shop's (SPEC §2.4).
				"dpp_idr": got.DPP, "ppn_idr": got.PPN, "total_idr": got.Total,
				"cogs_idr": got.COGS,
				// Margin, never profit: shared costs are out of scope and
				// settled outside this application (REQUIREMENTS §5).
				"gross_margin_idr": got.GrossMargin(),
				"net_margin_idr":   got.NetMargin(),
				"credit_idr":       got.Credit,
				"with_faktur_idr":  got.WithFaktur,
				// Counted, not hidden: a week with eleven voids is a training
				// problem, and it is invisible on a report showing only what
				// stuck.
				"void_count": got.VoidCount, "void_total_idr": got.VoidTotal,
				"return_count": got.ReturnCount, "refund_idr": got.Refund,
				"refund_ppn_idr": got.RefundPPN, "return_cogs_idr": got.ReturnCOGS,
			},
			"by_day": byDay, "by_product": byProduct, "by_method": byMethod,
			"caveat": marginCaveatForReports,
		})
	}
}

// marginCaveatForReports travels with any payload carrying a margin figure, for
// the same reason the margin report carries one: this is gross margin and it
// has not paid rent (REQUIREMENTS §5).
const marginCaveatForReports = "Margin kotor: biaya bersama seperti listrik, gaji, dan sewa " +
	"tidak termasuk. Ini bukan laba rugi."

// --- purchases (TASKS 6.2, R5.3) --------------------------------------------

func handlePurchasesReport(rep *service.Reports) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		from, to := window(r)

		got, err := rep.Purchases(r.Context(), entityID, from, to)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		bySupplier := make([]map[string]any, 0, len(got.BySupplier))
		for _, s := range got.BySupplier {
			bySupplier = append(bySupplier, map[string]any{
				"supplier_id": s.SupplierID, "supplier_code": s.SupplierCode,
				"supplier_name": s.SupplierName, "issues_faktur": s.IssuesFaktur == 1,
				"purchase_count": s.PurchaseCount, "total_idr": money.IDR(s.TotalIdr),
				"ppn_idr": money.IDR(s.PpnIdr), "with_faktur_count": s.WithFakturCount,
				// R10.6's comparison: the same goods from a supplier who issues
				// a faktur are ~11% cheaper in real terms at a PKP company, and
				// the invoice never says so.
				"ppn_into_cost_idr": money.IDR(s.PpnIntoCostIdr),
			})
		}
		byProduct := make([]map[string]any, 0, len(got.ByProduct))
		for _, p := range got.ByProduct {
			byProduct = append(byProduct, map[string]any{
				"product_id": p.ProductID, "product_code": p.ProductCode,
				"product_name": p.ProductName, "product_unit": p.ProductUnit,
				"qty": p.Qty, "gross_idr": money.IDR(p.GrossIdr),
				"cost_total_idr":     money.IDR(p.CostTotalIdr),
				"creditable_ppn_idr": money.IDR(p.CreditablePpnIdr),
			})
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"title":  "Laporan Pembelian",
			"period": periodBody(got.Period),
			"summary": map[string]any{
				"purchase_count": got.PurchaseCount,
				"subtotal_idr":   got.Subtotal, "ppn_idr": got.PPN, "total_idr": got.Total,
				"with_faktur_idr": got.WithFaktur, "without_faktur_idr": got.WithoutFaktur,
				// INV-9 as a figure: PPN paid with no faktur is not creditable
				// and went into the cost of the goods instead (SPEC §3.2).
				"ppn_into_cost_idr": got.PPNIntoCost,
				"return_count":      got.ReturnCount,
				"return_cost_idr":   got.ReturnCost, "return_ppn_idr": got.ReturnPPN,
			},
			"by_supplier": bySupplier, "by_product": byProduct,
		})
	}
}

// --- stock (TASKS 6.3, R5.2) ------------------------------------------------

func handleStockReport(rep *service.Reports) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		from, to := window(r)

		got, err := rep.Stock(r.Context(), entityID, from, to)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		onHand := make([]map[string]any, 0, len(got.OnHand))
		for _, s := range got.OnHand {
			onHand = append(onHand, map[string]any{
				"product_id": s.ProductID, "product_code": s.ProductCode,
				"product_name": s.ProductName, "product_unit": s.ProductUnit,
				"category": s.Category,
				// Stock is owner-attributed; a total mixing two family members'
				// goods is not a figure either of them can use (INV-8).
				"owner_id": s.OwnerID, "owner_name": s.OwnerName,
				"qty_on_hand": s.QtyOnHand, "value_idr": money.IDR(s.ValueIdr),
				"layer_count": s.LayerCount, "layers_without_faktur": s.LayersWithoutFaktur,
				// Captured, not acted on: no FEFO picking (REQUIREMENTS §6.4).
				"earliest_expiry": s.EarliestExpiry,
			})
		}
		outOfStock := make([]map[string]any, 0, len(got.OutOfStock))
		for _, s := range got.OutOfStock {
			outOfStock = append(outOfStock, map[string]any{
				"product_id": s.ProductID, "product_code": s.ProductCode,
				"product_name": s.ProductName, "product_unit": s.ProductUnit,
				"owner_id": s.OwnerID,
			})
		}
		movement := make([]map[string]any, 0, len(got.Movement))
		for _, m := range got.Movement {
			movement = append(movement, map[string]any{
				"product_id": m.ProductID, "product_code": m.ProductCode,
				"product_name": m.ProductName, "movement_type": m.MovementType,
				"qty": m.Qty, "cost_idr": money.IDR(m.CostIdr),
			})
		}
		intake := make([]map[string]any, 0, len(got.Intake))
		for _, i := range got.Intake {
			intake = append(intake, map[string]any{
				"product_id": i.ProductID, "product_code": i.ProductCode,
				"product_name": i.ProductName, "source": i.Source,
				"qty": i.Qty, "cost_idr": money.IDR(i.CostIdr),
			})
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"title":  "Laporan Stok",
			"period": periodBody(got.Period),
			"summary": map[string]any{
				"lines": got.Lines, "qty_total": got.QtyTotal, "value_idr": got.Value,
			},
			// Stated rather than implied: this system does not reconstruct
			// historical balances, so on-hand is now and the movement is the
			// period.
			"on_hand_as_of": "sekarang",
			"on_hand":       onHand,
			"out_of_stock":  outOfStock,
			"movement":      movement,
			"intake":        intake,
		})
	}
}

// --- hutang and piutang (TASKS 6.4–6.5, R5.5–5.6, R5.8) ---------------------

func agingBody(report aging.Report) map[string]any {
	buckets := func(b aging.Buckets) map[string]any {
		out := make(map[string]any, len(aging.Order())+2)
		for _, name := range aging.Order() {
			out[string(name)] = b.Get(name)
		}
		out["total_idr"] = b.Total()
		// Deliberately excludes what could not be aged: those documents may
		// well be late, and saying so would state as fact something this
		// system cannot know.
		out["overdue_idr"] = b.Overdue()
		return out
	}

	parties := make([]map[string]any, 0, len(report.Counterparties))
	for _, c := range report.Counterparties {
		items := make([]map[string]any, 0, len(c.Items))
		for _, it := range c.Items {
			items = append(items, map[string]any{
				"id": it.ID, "document_no": it.DocumentNo, "source": it.Source,
				"incurred_on": it.IncurredOn, "due_date": it.DueDate,
				"amount_idr": it.Amount, "paid_idr": it.Paid,
				"outstanding_idr": it.Outstanding,
				"bucket":          string(it.Bucket), "days_overdue": it.DaysOverdue,
			})
		}
		parties = append(parties, map[string]any{
			"id": c.ID, "name": c.Name,
			"buckets": buckets(c.Buckets), "total_idr": c.Total(),
			// What the list is sorted on: the person to ring this morning is
			// at the top, not whoever is alphabetically first.
			"oldest_days": c.OldestDays, "without_due_date": c.WithoutDueDate,
			"items": items,
		})
	}

	credits := make([]map[string]any, 0, len(report.CreditItems))
	for _, it := range report.CreditItems {
		credits = append(credits, map[string]any{
			"id": it.ID, "counterparty_name": it.CounterpartyName,
			"document_no": it.DocumentNo, "incurred_on": it.IncurredOn,
			"amount_idr": it.Amount, "paid_idr": it.Paid,
			"overpaid_idr": it.Outstanding.Abs(),
		})
	}

	return map[string]any{
		"kind": string(report.Kind), "as_of": report.AsOf,
		"buckets": buckets(report.Buckets), "bucket_order": aging.Order(),
		"counterparties": parties,
		// The count that makes the gap fixable. Ageing needs a credit-terms
		// answer this business has not given yet (REQUIREMENTS §11), and every
		// undated document is one nobody can chase on a date.
		"without_due_date": report.WithoutDueDate,
		"credit_idr":       report.CreditBalance,
		"credits":          credits,
	}
}

func handlePayablesAging(rep *service.Reports) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		got, err := rep.Payables(r.Context(), entityID, r.URL.Query().Get("as_of"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		body := agingBody(got)
		body["title"] = "Laporan Hutang"
		writeJSON(w, stdhttp.StatusOK, body)
	}
}

func handleReceivablesAging(rep *service.Reports) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		got, err := rep.Receivables(r.Context(), entityID, r.URL.Query().Get("as_of"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		body := agingBody(got)
		body["title"] = "Laporan Piutang"
		writeJSON(w, stdhttp.StatusOK, body)
	}
}

func debtDetailBody(d service.DebtDetail) map[string]any {
	payments := make([]map[string]any, 0, len(d.Payments))
	for _, p := range d.Payments {
		payments = append(payments, map[string]any{
			"id": p.ID, "amount_idr": p.AmountIDR, "paid_on": p.PaidOn,
			"method": p.Method, "note": p.Note,
		})
	}

	out := map[string]any{"payments": payments}
	switch {
	case d.Payable != nil:
		out["document"] = map[string]any{
			"id": d.Payable.ID, "counterparty_id": d.Payable.SupplierID,
			"source": d.Payable.Source, "invoice_no": d.Payable.InvoiceNo,
			"amount_idr": money.IDR(d.Payable.AmountIdr),
			"paid_idr":   money.IDR(d.Payable.PaidIdr),
			// Derived from the payment rows, never stored: a carried balance
			// is a number that can disagree with the payments beneath it.
			"outstanding_idr": money.IDR(d.Payable.OutstandingIdr),
			"incurred_on":     d.Payable.IncurredOn, "due_date": d.Payable.DueDate,
			"note": d.Payable.Note,
		}
	case d.Receivable != nil:
		out["document"] = map[string]any{
			"id": d.Receivable.ID, "counterparty_id": d.Receivable.CustomerID,
			"source": d.Receivable.Source, "invoice_no": d.Receivable.InvoiceNo,
			"amount_idr":      money.IDR(d.Receivable.AmountIdr),
			"paid_idr":        money.IDR(d.Receivable.PaidIdr),
			"outstanding_idr": money.IDR(d.Receivable.OutstandingIdr),
			"incurred_on":     d.Receivable.IncurredOn, "due_date": d.Receivable.DueDate,
			"note": d.Receivable.Note,
		}
	}
	return out
}

func handlePayableDetail(op *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		got, err := op.PayableDetail(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, debtDetailBody(got))
	}
}

func handleReceivableDetail(op *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		got, err := op.ReceivableDetail(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, debtDetailBody(got))
	}
}

func mountReports(r chi.Router, cfg Config) {
	if cfg.Reports == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanSeeReports,
			"hanya pemilik dan manajer yang dapat melihat laporan"))

		r.Get("/reports/sales", handleSalesReport(cfg.Reports))
		r.Get("/reports/purchases", handlePurchasesReport(cfg.Reports))
		r.Get("/reports/stock", handleStockReport(cfg.Reports))
		r.Get("/reports/payables", handlePayablesAging(cfg.Reports))
		r.Get("/reports/receivables", handleReceivablesAging(cfg.Reports))

		if cfg.Opening != nil {
			// The partial payments behind one balance (R5.8). "Rp 4.000.000
			// outstanding" answers nothing when the supplier's question is
			// which invoice last month's transfer was against.
			r.Get("/payables/{id}", handlePayableDetail(cfg.Opening))
			r.Get("/receivables/{id}", handleReceivableDetail(cfg.Opening))
		}
	})
}
