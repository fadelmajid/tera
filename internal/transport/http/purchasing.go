package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- wire shapes ------------------------------------------------------------

type purchaseDTO struct {
	ID           string  `json:"id"`
	SupplierID   string  `json:"supplier_id"`
	InvoiceNo    *string `json:"invoice_no"`
	BusinessDate string  `json:"business_date"`
	// The field the whole screen exists for (INV-9). Explicit on the wire so
	// the list can show it without the client inferring anything.
	FakturReceived bool      `json:"faktur_received"`
	FakturNo       *string   `json:"faktur_no"`
	SubtotalIDR    money.IDR `json:"subtotal_idr"`
	PPNIDR         money.IDR `json:"ppn_idr"`
	TotalIDR       money.IDR `json:"total_idr"`
	IsCredit       bool      `json:"is_credit"`
	DueDate        *string   `json:"due_date"`
	Note           *string   `json:"note"`
}

func toPurchaseDTO(p gen.Purchase) purchaseDTO {
	return purchaseDTO{
		ID: p.ID, SupplierID: p.SupplierID, InvoiceNo: p.InvoiceNo, BusinessDate: p.BusinessDate,
		FakturReceived: p.FakturReceived == 1, FakturNo: p.FakturNo,
		SubtotalIDR: money.IDR(p.SubtotalIdr), PPNIDR: money.IDR(p.PpnIdr),
		TotalIDR: money.IDR(p.TotalIdr), IsCredit: p.IsCredit == 1,
		DueDate: p.DueDate, Note: p.Note,
	}
}

type purchaseLineDTO struct {
	ID          string  `json:"id"`
	ProductID   string  `json:"product_id"`
	ProductCode string  `json:"product_code"`
	ProductName string  `json:"product_name"`
	ProductUnit string  `json:"product_unit"`
	OwnerID     *string `json:"owner_id"`
	Qty         int64   `json:"qty"`

	UnitPriceIDR money.IDR `json:"unit_price_idr"`
	SubtotalIDR  money.IDR `json:"subtotal_idr"`
	PPNIDR       money.IDR `json:"ppn_idr"`
	GrossIDR     money.IDR `json:"gross_idr"`
	// The two halves of SPEC §3.2, both on the wire. The screen shows them side
	// by side so the difference the faktur made is visible on the document
	// itself, not only in a report months later.
	CostTotalIDR     money.IDR `json:"cost_total_idr"`
	CreditablePPNIDR money.IDR `json:"creditable_ppn_idr"`
	StockLayerID     string    `json:"stock_layer_id"`
}

func toPurchaseLineDTO(l gen.ListPurchaseLinesRow) purchaseLineDTO {
	return purchaseLineDTO{
		ID: l.ID, ProductID: l.ProductID, ProductCode: l.ProductCode,
		ProductName: l.ProductName, ProductUnit: l.ProductUnit,
		OwnerID: l.OwnerID, Qty: l.Qty,
		UnitPriceIDR: money.IDR(l.UnitPriceIdr), SubtotalIDR: money.IDR(l.SubtotalIdr),
		PPNIDR: money.IDR(l.PpnIdr), GrossIDR: money.IDR(l.GrossIdr),
		CostTotalIDR: money.IDR(l.CostTotalIdr), CreditablePPNIDR: money.IDR(l.CreditablePpnIdr),
		StockLayerID: l.StockLayerID,
	}
}

type layerDTO struct {
	ID             string    `json:"id"`
	ProductID      string    `json:"product_id"`
	OwnerID        *string   `json:"owner_id"`
	QtyIn          int64     `json:"qty_in"`
	CostTotalIDR   money.IDR `json:"cost_total_idr"`
	FakturReceived bool      `json:"faktur_received"`
	PPNPaidIDR     money.IDR `json:"ppn_paid_idr"`
}

func toLayerDTO(l gen.StockLayer) layerDTO {
	return layerDTO{
		ID: l.ID, ProductID: l.ProductID, OwnerID: l.OwnerID, QtyIn: l.QtyIn,
		CostTotalIDR: money.IDR(l.CostTotalIdr), FakturReceived: l.FakturReceived == 1,
		PPNPaidIDR: money.IDR(l.PpnPaidIdr),
	}
}

// --- requests ---------------------------------------------------------------

type purchaseLineRequest struct {
	ProductID    string    `json:"product_id"`
	Qty          int64     `json:"qty"`
	UnitPriceIDR money.IDR `json:"unit_price_idr"`
	PPNIDR       money.IDR `json:"ppn_idr"`
	ExpiryDate   string    `json:"expiry_date"`
}

type purchaseRequest struct {
	SupplierID     string                `json:"supplier_id"`
	InvoiceNo      string                `json:"invoice_no"`
	PurchaseDate   string                `json:"purchase_date"`
	FakturReceived bool                  `json:"faktur_received"`
	FakturNo       string                `json:"faktur_no"`
	IsCredit       bool                  `json:"is_credit"`
	DueDate        string                `json:"due_date"`
	Note           string                `json:"note"`
	Lines          []purchaseLineRequest `json:"lines"`
}

func (r purchaseRequest) toInput() service.PurchaseInput {
	lines := make([]service.PurchaseLineInput, 0, len(r.Lines))
	for _, l := range r.Lines {
		lines = append(lines, service.PurchaseLineInput{
			ProductID: l.ProductID, Qty: l.Qty, UnitPriceIDR: l.UnitPriceIDR,
			PPNIDR: l.PPNIDR, ExpiryDate: l.ExpiryDate,
		})
	}
	return service.PurchaseInput{
		SupplierID: r.SupplierID, InvoiceNo: r.InvoiceNo, PurchaseDate: r.PurchaseDate,
		FakturReceived: r.FakturReceived, FakturNo: r.FakturNo,
		IsCredit: r.IsCredit, DueDate: r.DueDate, Note: r.Note, Lines: lines,
	}
}

type purchaseReturnRequest struct {
	ReturnDate string `json:"return_date"`
	Reason     string `json:"reason"`
	Lines      []struct {
		PurchaseLineID string `json:"purchase_line_id"`
		Qty            int64  `json:"qty"`
	} `json:"lines"`
}

// --- handlers ---------------------------------------------------------------

func handleCreatePurchase(p *service.Purchasing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req purchaseRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		got, err := p.CreatePurchase(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}

		lines := make([]purchaseLineDTO, 0, len(got.Lines))
		for i, l := range got.Lines {
			lines = append(lines, purchaseLineDTO{
				ID: l.ID, ProductID: l.ProductID, OwnerID: l.OwnerID, Qty: l.Qty,
				UnitPriceIDR: money.IDR(l.UnitPriceIdr), SubtotalIDR: money.IDR(l.SubtotalIdr),
				PPNIDR: money.IDR(l.PpnIdr), GrossIDR: money.IDR(l.GrossIdr),
				CostTotalIDR: money.IDR(l.CostTotalIdr), CreditablePPNIDR: money.IDR(l.CreditablePpnIdr),
				StockLayerID: got.Layers[i].ID,
			})
		}
		layers := make([]layerDTO, 0, len(got.Layers))
		for _, l := range got.Layers {
			layers = append(layers, toLayerDTO(l))
		}

		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"purchase": toPurchaseDTO(got.Purchase),
			"lines":    lines,
			"layers":   layers,
			"payable":  payableOrNil(got.Payable),
		})
	}
}

func payableOrNil(p *gen.Payable) any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"id": p.ID, "amount_idr": money.IDR(p.AmountIdr), "due_date": p.DueDate,
	}
}

func handleListPurchases(p *service.Purchasing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := p.ListPurchases(r.Context(), entityID,
			r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]purchaseDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toPurchaseDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleGetPurchase(p *service.Purchasing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		purchase, lines, err := p.GetPurchase(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]purchaseLineDTO, 0, len(lines))
		for _, l := range lines {
			out = append(out, toPurchaseLineDTO(l))
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"purchase": toPurchaseDTO(purchase), "lines": out,
		})
	}
}

func handleCreatePurchaseReturn(p *service.Purchasing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req purchaseReturnRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		lines := make([]service.PurchaseReturnLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			lines = append(lines, service.PurchaseReturnLineInput{PurchaseLineID: l.PurchaseLineID, Qty: l.Qty})
		}

		got, err := p.CreatePurchaseReturn(r.Context(), actorFrom(r), service.PurchaseReturnInput{
			PurchaseID: chi.URLParam(r, "id"), ReturnDate: req.ReturnDate,
			Reason: req.Reason, Lines: lines,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id":                got.Return.ID,
			"cost_idr":          money.IDR(got.Return.CostIdr),
			"ppn_reversed_idr":  money.IDR(got.Return.PpnReversedIdr),
			"business_date":     got.Return.BusinessDate,
			"lines_returned":    len(got.Lines),
			"compliance_notice": complianceNotice,
		})
	}
}

// complianceNotice accompanies every figure with a tax consequence. We are not
// tax advisors, and the guardrail in CLAUDE.md is not decorative: an owner
// acting on an estimate from this system without checking it is exactly the
// outcome to avoid.
const complianceNotice = "Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda."

func handleInputPPN(p *service.Purchasing) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		if from == "" || to == "" {
			writeJSON(w, stdhttp.StatusBadRequest, map[string]any{
				"error": "rentang tanggal wajib diisi (from dan to)",
			})
			return
		}

		pos, err := p.InputPPN(r.Context(), entityID, from, to)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"creditable_idr":    pos.Creditable,
			"reversed_idr":      pos.Reversed,
			"net_idr":           pos.Net,
			"compliance_notice": complianceNotice,
		})
	}
}

// --- routes -----------------------------------------------------------------

// mountPurchasing wires the purchasing, opname, and opening-balance routes.
//
// All of it sits behind CanEnterPurchases (R10.4): entered by admin or manager,
// not cashier staff. These screens set the faktur status that decides a stock
// layer's cost basis (INV-9) and post stock adjustments (R12.5) -- they are
// margin-bearing, not data entry.
func mountPurchasing(r chi.Router, cfg Config) {
	if cfg.Purchasing == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanEnterPurchases,
			"hanya pemilik dan manajer yang dapat mencatat pembelian dan penyesuaian stok"))

		r.Post("/purchases", handleCreatePurchase(cfg.Purchasing))
		r.Get("/purchases", handleListPurchases(cfg.Purchasing))
		r.Get("/purchases/{id}", handleGetPurchase(cfg.Purchasing))
		r.Post("/purchases/{id}/returns", handleCreatePurchaseReturn(cfg.Purchasing))
		r.Get("/ppn/input", handleInputPPN(cfg.Purchasing))

		if cfg.Opname != nil {
			r.Get("/opname", handleListOpnames(cfg.Opname))
			r.Post("/opname", handleStartOpname(cfg.Opname))
			r.Get("/opname/count-sheet", handleCountSheet(cfg.Opname))
			r.Get("/opname/{id}", handleGetOpname(cfg.Opname))
			r.Post("/opname/{id}/lines", handleSaveOpnameLine(cfg.Opname))
			r.Post("/opname/{id}/post", handlePostOpname(cfg.Opname))
		}

		if cfg.Opening != nil {
			r.Post("/opening/stock", handleCarryInStock(cfg.Opening))
			r.Post("/opening/payables", handleCarryInPayable(cfg.Opening))
			r.Post("/opening/receivables", handleCarryInReceivable(cfg.Opening))
			r.Get("/payables", handleListPayables(cfg.Opening))
			r.Get("/receivables", handleListReceivables(cfg.Opening))
			r.Post("/payables/{id}/payments", handlePayPayable(cfg.Opening))
			r.Post("/receivables/{id}/payments", handlePayReceivable(cfg.Opening))
		}
	})
}
