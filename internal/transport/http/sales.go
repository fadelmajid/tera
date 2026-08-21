package http

import (
	"errors"
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- wire shapes ------------------------------------------------------------

type saleDTO struct {
	ID           string  `json:"id"`
	InvoiceNo    string  `json:"invoice_no"`
	BusinessDate string  `json:"business_date"`
	OccurredAt   int64   `json:"occurred_at"`
	Status       string  `json:"status"`
	CustomerID   *string `json:"customer_id"`
	// A PKP owes output PPN whether or not the buyer took a faktur
	// (SPEC §2.3). The two facts stay separate on the wire so no client can
	// collapse them.
	FakturIssued bool      `json:"faktur_issued"`
	FakturNo     *string   `json:"faktur_no"`
	GrossIDR     money.IDR `json:"gross_idr"`
	DiscountIDR  money.IDR `json:"discount_idr"`
	PPNIDR       money.IDR `json:"ppn_idr"`
	TotalIDR     money.IDR `json:"total_idr"`
	COGSIDR      money.IDR `json:"cogs_idr"`
	IsCredit     bool      `json:"is_credit"`
	DueDate      *string   `json:"due_date"`
	VoidReason   *string   `json:"void_reason"`
}

func toSaleDTO(s gen.Sale) saleDTO {
	return saleDTO{
		ID: s.ID, InvoiceNo: s.InvoiceNo, BusinessDate: s.BusinessDate,
		OccurredAt: s.OccurredAt, Status: s.Status, CustomerID: s.CustomerID,
		FakturIssued: s.FakturIssued == 1, FakturNo: s.FakturNo,
		GrossIDR: money.IDR(s.GrossIdr), DiscountIDR: money.IDR(s.DiscountIdr),
		PPNIDR: money.IDR(s.PpnIdr), TotalIDR: money.IDR(s.TotalIdr),
		COGSIDR: money.IDR(s.CogsIdr), IsCredit: s.IsCredit == 1,
		DueDate: s.DueDate, VoidReason: s.VoidReason,
	}
}

type saleLineDTO struct {
	ID          string  `json:"id"`
	ProductID   string  `json:"product_id"`
	ProductCode string  `json:"product_code"`
	ProductName string  `json:"product_name"`
	ProductUnit string  `json:"product_unit"`
	OwnerID     *string `json:"owner_id"`
	OwnerName   *string `json:"owner_name"`
	Qty         int64   `json:"qty"`

	UnitPriceIDR     money.IDR `json:"unit_price_idr"`
	GrossIDR         money.IDR `json:"gross_idr"`
	LineDiscountIDR  money.IDR `json:"line_discount_idr"`
	AllocDiscountIDR money.IDR `json:"alloc_discount_idr"`
	NetIDR           money.IDR `json:"net_idr"`
	COGSIDR          money.IDR `json:"cogs_idr"`
}

func toSaleLineDTO(l gen.ListSaleLinesRow) saleLineDTO {
	return saleLineDTO{
		ID: l.ID, ProductID: l.ProductID, ProductCode: l.ProductCode,
		ProductName: l.ProductName, ProductUnit: l.ProductUnit,
		OwnerID: l.OwnerID, OwnerName: l.OwnerName, Qty: l.Qty,
		UnitPriceIDR: money.IDR(l.UnitPriceIdr), GrossIDR: money.IDR(l.GrossIdr),
		LineDiscountIDR:  money.IDR(l.LineDiscountIdr),
		AllocDiscountIDR: money.IDR(l.AllocDiscountIdr),
		NetIDR:           money.IDR(l.NetIdr), COGSIDR: money.IDR(l.CogsIdr),
	}
}

type sessionDTO struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	BusinessDate    string     `json:"business_date"`
	OpeningFloatIDR money.IDR  `json:"opening_float_idr"`
	CountedCashIDR  *money.IDR `json:"counted_cash_idr"`
	ExpectedCashIDR *money.IDR `json:"expected_cash_idr"`
	VarianceIDR     *money.IDR `json:"variance_idr"`
}

func toSessionDTO(s gen.CashSession) sessionDTO {
	out := sessionDTO{
		ID: s.ID, Status: s.Status, BusinessDate: s.BusinessDate,
		OpeningFloatIDR: money.IDR(s.OpeningFloatIdr),
	}
	out.CountedCashIDR = idrPtr(s.CountedCashIdr)
	out.ExpectedCashIDR = idrPtr(s.ExpectedCashIdr)
	out.VarianceIDR = idrPtr(s.VarianceIdr)
	return out
}

func idrPtr(v *int64) *money.IDR {
	if v == nil {
		return nil
	}
	m := money.IDR(*v)
	return &m
}

// --- requests ---------------------------------------------------------------

type saleRequest struct {
	CustomerID string `json:"customer_id"`
	SaleDate   string `json:"sale_date"`
	// Whole rupiah. A percentage is resolved in the UI and never sent, because
	// a stored percentage has to be re-multiplied to be read and that is a
	// second place for the total to stop matching the sum of its parts.
	InvoiceDiscountIDR money.IDR `json:"invoice_discount_idr"`
	FakturIssued       bool      `json:"faktur_issued"`
	FakturNo           string    `json:"faktur_no"`
	IsCredit           bool      `json:"is_credit"`
	DueDate            string    `json:"due_date"`
	Note               string    `json:"note"`
	Lines              []struct {
		ProductID       string     `json:"product_id"`
		Qty             int64      `json:"qty"`
		UnitPriceIDR    *money.IDR `json:"unit_price_idr"`
		LineDiscountIDR money.IDR  `json:"line_discount_idr"`
	} `json:"lines"`
	Payments []struct {
		Method    string    `json:"method"`
		AmountIDR money.IDR `json:"amount_idr"`
		Reference string    `json:"reference"`
	} `json:"payments"`
}

func (r saleRequest) toInput() service.SaleInput {
	lines := make([]service.SaleLineInput, 0, len(r.Lines))
	for _, l := range r.Lines {
		lines = append(lines, service.SaleLineInput{
			ProductID: l.ProductID, Qty: l.Qty,
			UnitPriceIDR: l.UnitPriceIDR, LineDiscountIDR: l.LineDiscountIDR,
		})
	}
	payments := make([]service.PaymentInputLine, 0, len(r.Payments))
	for _, p := range r.Payments {
		payments = append(payments, service.PaymentInputLine{
			Method: p.Method, AmountIDR: p.AmountIDR, Reference: p.Reference,
		})
	}
	return service.SaleInput{
		CustomerID: r.CustomerID, SaleDate: r.SaleDate,
		InvoiceDiscountIDR: r.InvoiceDiscountIDR,
		FakturIssued:       r.FakturIssued, FakturNo: r.FakturNo,
		IsCredit: r.IsCredit, DueDate: r.DueDate, Note: r.Note,
		Lines: lines, Payments: payments,
	}
}

// --- handlers ---------------------------------------------------------------

func handleRingSale(s *service.Sales, p *service.Printing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req saleRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		got, err := s.Ring(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// The sale is committed. Printing is attempted afterwards and never
		// rolls it back: paper jamming must not undo a transaction the customer
		// has paid for. A failed print comes back as a flag and a reprint
		// button, not as a failed sale.
		printed, printError := false, ""
		if p != nil && p.Configured() {
			if err := p.PrintSale(r.Context(), got.Sale.EntityID, got.Sale.ID, true); err != nil {
				printError = err.Error()
			} else {
				printed = true
			}
		}

		lines := make([]saleLineDTO, 0, len(got.Lines))
		for _, l := range got.Lines {
			lines = append(lines, saleLineDTO{
				ID: l.ID, ProductID: l.ProductID, OwnerID: l.OwnerID, Qty: l.Qty,
				UnitPriceIDR: money.IDR(l.UnitPriceIdr), GrossIDR: money.IDR(l.GrossIdr),
				LineDiscountIDR:  money.IDR(l.LineDiscountIdr),
				AllocDiscountIDR: money.IDR(l.AllocDiscountIdr),
				NetIDR:           money.IDR(l.NetIdr), COGSIDR: money.IDR(l.CogsIdr),
			})
		}

		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"sale":        toSaleDTO(got.Sale),
			"lines":       lines,
			"cogs_idr":    got.COGS,
			"margin_idr":  got.Margin,
			"receivable":  receivableOrNil(got.Receivable),
			"printed":     printed,
			"print_error": printError,
		})
	}
}

func receivableOrNil(r *gen.Receivable) any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id": r.ID, "amount_idr": money.IDR(r.AmountIdr), "due_date": r.DueDate,
	}
}

func handleListSales(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := s.ListSales(r.Context(), entityID,
			r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]saleDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toSaleDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleGetSale(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		sale, lines, payments, err := s.GetSale(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}

		lineOut := make([]saleLineDTO, 0, len(lines))
		for _, l := range lines {
			lineOut = append(lineOut, toSaleLineDTO(l))
		}
		payOut := make([]map[string]any, 0, len(payments))
		for _, p := range payments {
			payOut = append(payOut, map[string]any{
				"method": p.Method, "amount_idr": money.IDR(p.AmountIdr), "reference": p.Reference,
			})
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"sale": toSaleDTO(sale), "lines": lineOut, "payments": payOut,
		})
	}
}

// The drill-down's first level (SPEC §4.2): which layers this sale drew from
// and what each cost. Every figure on the margin report must expand to this.
func handleSaleDrillDown(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := s.SaleDrillDown(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, c := range rows {
			out = append(out, map[string]any{
				"layer_id": c.LayerID, "product_id": c.ProductID, "owner_id": c.OwnerID,
				"qty_out": c.QtyOut, "cost_idr": money.IDR(c.CostIdr),
				"layer_acquired_at":     c.LayerAcquiredAt,
				"layer_cost_total_idr":  money.IDR(c.LayerCostTotalIdr),
				"layer_qty_in":          c.LayerQtyIn,
				"layer_faktur_received": c.LayerFakturReceived == 1,
				"reverses_id":           c.ReversesID,
			})
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleVoidSale(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			Reason string `json:"reason"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		got, err := s.Void(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.Reason)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, toSaleDTO(got))
	}
}

func handleReturnSale(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			ReturnDate   string `json:"return_date"`
			Reason       string `json:"reason"`
			RefundMethod string `json:"refund_method"`
			Lines        []struct {
				SaleLineID string `json:"sale_line_id"`
				Qty        int64  `json:"qty"`
			} `json:"lines"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		lines := make([]service.SaleReturnLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			lines = append(lines, service.SaleReturnLineInput{SaleLineID: l.SaleLineID, Qty: l.Qty})
		}

		got, err := s.CreateReturn(r.Context(), actorFrom(r), service.SaleReturnInput{
			SaleID: chi.URLParam(r, "id"), ReturnDate: req.ReturnDate,
			Reason: req.Reason, RefundMethod: req.RefundMethod, Lines: lines,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id":                 got.Return.ID,
			"refund_idr":         got.Refund,
			"cogs_reversed_idr":  got.COGSReversed,
			"business_date":      got.Return.BusinessDate,
			"sale_business_date": got.Return.SaleBusinessDate,
		})
	}
}

// --- cash session -----------------------------------------------------------

func handleOpenSession(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			OpeningFloatIDR money.IDR `json:"opening_float_idr"`
			BusinessDate    string    `json:"business_date"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		got, err := s.OpenSession(r.Context(), actorFrom(r), service.OpenSessionInput{
			OpeningFloatIDR: req.OpeningFloatIDR, BusinessDate: req.BusinessDate,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, toSessionDTO(got))
	}
}

func handleCurrentSession(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		got, err := s.OpenSessionFor(r.Context(), entityID)
		if err != nil {
			// Not an error condition: a shop before opening time has no till.
			writeJSON(w, stdhttp.StatusOK, nil)
			return
		}
		writeJSON(w, stdhttp.StatusOK, toSessionDTO(got))
	}
}

func handleSessionTotals(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		got, err := s.Totals(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, zReport(got))
	}
}

func handleCloseSession(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			CountedCashIDR money.IDR `json:"counted_cash_idr"`
			Note           string    `json:"note"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		got, err := s.CloseSession(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.CountedCashIDR, req.Note)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, zReport(got))
	}
}

func zReport(t service.SessionTotals) map[string]any {
	byMethod := make(map[string]money.IDR, len(t.ByMethod))
	for k, v := range t.ByMethod {
		byMethod[k] = v
	}
	return map[string]any{
		"session":       toSessionDTO(t.Session),
		"by_method":     byMethod,
		"cash_refunds":  t.CashRefunds,
		"expected_cash": t.ExpectedCash,
		"total_takings": t.TotalTakings,
		"payment_count": t.PaymentCount,
	}
}

func handleListSessions(s *service.Sales) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := s.ListSessions(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]sessionDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toSessionDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

// --- printing ---------------------------------------------------------------

func handlePrintReceipt(p *service.Printing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		saleID := chi.URLParam(r, "id")

		err := p.PrintSale(r.Context(), entityID, saleID, r.URL.Query().Get("drawer") == "1")
		switch {
		case err == nil:
			writeJSON(w, stdhttp.StatusOK, map[string]any{"printed": true})
		case errors.Is(err, service.ErrPrinterNotConfigured):
			// Not a failure of the sale. A shop with no printer yet must still
			// be able to trade, and the preview is the fallback.
			writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{
				"error": "printer belum dikonfigurasi", "printed": false,
			})
		case errors.Is(err, service.ErrNotFound):
			writeServiceError(w, err)
		default:
			writeJSON(w, stdhttp.StatusBadGateway, map[string]any{
				"error": "gagal mencetak: " + err.Error(), "printed": false,
			})
		}
	}
}

func handleReceiptPreview(p *service.Printing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		text, err := p.Preview(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"text": text, "printer_configured": p.Configured(),
		})
	}
}

func handleOpenDrawer(p *service.Printing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if err := p.OpenDrawer(r.Context()); err != nil {
			if errors.Is(err, service.ErrPrinterNotConfigured) {
				writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{
					"error": "printer belum dikonfigurasi",
				})
				return
			}
			writeJSON(w, stdhttp.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{"opened": true})
	}
}

// --- routes -----------------------------------------------------------------

// mountSales wires the till.
//
// Any role in the company may ring a sale: the cashier is staff, and selling is
// their whole job. Voiding and returning reach backwards into finalised
// transactions, so those need manager or above (R7.1).
func mountSales(r chi.Router, cfg Config) {
	if cfg.Sales == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(anyRole, "tidak punya akses"))

		r.Post("/sales", handleRingSale(cfg.Sales, cfg.Printing))
		r.Get("/sales", handleListSales(cfg.Sales))
		r.Get("/sales/{id}", handleGetSale(cfg.Sales))
		r.Get("/sales/{id}/layers", handleSaleDrillDown(cfg.Sales))

		r.Get("/cash-sessions", handleListSessions(cfg.Sales))
		r.Get("/cash-sessions/current", handleCurrentSession(cfg.Sales))
		r.Post("/cash-sessions", handleOpenSession(cfg.Sales))
		r.Get("/cash-sessions/{id}/totals", handleSessionTotals(cfg.Sales))
		r.Post("/cash-sessions/{id}/close", handleCloseSession(cfg.Sales))

		if cfg.Printing != nil {
			r.Post("/sales/{id}/print", handlePrintReceipt(cfg.Printing))
			r.Get("/sales/{id}/receipt", handleReceiptPreview(cfg.Printing))
			r.Post("/drawer/open", handleOpenDrawer(cfg.Printing))
		}
	})

	// Reaching backwards into a finalised transaction (R7.1, R12.3).
	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanEditHistory,
			"hanya pemilik dan manajer yang dapat membatalkan atau meretur penjualan"))

		r.Post("/sales/{id}/void", handleVoidSale(cfg.Sales))
		r.Post("/sales/{id}/returns", handleReturnSale(cfg.Sales))
	})
}
