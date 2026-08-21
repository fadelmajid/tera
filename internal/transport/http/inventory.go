package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- stock opname -----------------------------------------------------------

type opnameDTO struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	BusinessDate string  `json:"business_date"`
	Note         *string `json:"note"`
	PostedAt     *int64  `json:"posted_at"`
}

func toOpnameDTO(o gen.StockOpname) opnameDTO {
	return opnameDTO{
		ID: o.ID, Status: o.Status, BusinessDate: o.BusinessDate,
		Note: o.Note, PostedAt: o.PostedAt,
	}
}

type opnameLineDTO struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	ProductCode string `json:"product_code"`
	ProductName string `json:"product_name"`
	ProductUnit string `json:"product_unit"`
	// Null is the company bucket, and it is a real attribution rather than a
	// missing value (R2.2). Explicitly null on the wire so the distinction
	// survives.
	OwnerID   *string `json:"owner_id"`
	OwnerName *string `json:"owner_name"`

	SystemQty   int64      `json:"system_qty"`
	CountedQty  int64      `json:"counted_qty"`
	Variance    int64      `json:"variance"`
	ReasonCode  *string    `json:"reason_code"`
	ReasonNote  *string    `json:"reason_note"`
	UnitCostIDR *money.IDR `json:"unit_cost_idr"`
}

func toOpnameLineDTO(l gen.ListOpnameLinesRow) opnameLineDTO {
	var cost *money.IDR
	if l.UnitCostIdr != nil {
		v := money.IDR(*l.UnitCostIdr)
		cost = &v
	}
	return opnameLineDTO{
		ID: l.ID, ProductID: l.ProductID, ProductCode: l.ProductCode,
		ProductName: l.ProductName, ProductUnit: l.ProductUnit,
		OwnerID: l.OwnerID, OwnerName: l.OwnerName,
		SystemQty: l.SystemQty, CountedQty: l.CountedQty, Variance: l.Variance,
		ReasonCode: l.ReasonCode, ReasonNote: l.ReasonNote, UnitCostIDR: cost,
	}
}

type onHandDTO struct {
	ProductID   string  `json:"product_id"`
	ProductCode string  `json:"product_code"`
	ProductName string  `json:"product_name"`
	ProductUnit string  `json:"product_unit"`
	OwnerID     *string `json:"owner_id"`
	OwnerName   *string `json:"owner_name"`
	QtyOnHand   int64   `json:"qty_on_hand"`
}

func handleCountSheet(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := o.CountSheet(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]onHandDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, onHandDTO{
				ProductID: row.ProductID, ProductCode: row.ProductCode,
				ProductName: row.ProductName, ProductUnit: row.ProductUnit,
				OwnerID: row.OwnerID, OwnerName: row.OwnerName, QtyOnHand: row.QtyOnHand,
			})
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleListOpnames(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := o.List(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]opnameDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toOpnameDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleStartOpname(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			CountDate string `json:"count_date"`
			Note      string `json:"note"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := o.Start(r.Context(), actorFrom(r), service.StartInput{
			CountDate: req.CountDate, Note: req.Note,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, toOpnameDTO(row))
	}
}

func handleGetOpname(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		header, lines, err := o.Lines(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]opnameLineDTO, 0, len(lines))
		for _, l := range lines {
			out = append(out, toOpnameLineDTO(l))
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"opname": toOpnameDTO(header), "lines": out,
		})
	}
}

func handleSaveOpnameLine(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			ProductID  string `json:"product_id"`
			OwnerID    string `json:"owner_id"`
			CountedQty int64  `json:"counted_qty"`
			ReasonCode string `json:"reason_code"`
			ReasonNote string `json:"reason_note"`
			// Absent asks the server for the default: the most recent layer of
			// the same product and owner. Present-and-zero is a different
			// statement -- free samples do turn up -- which is why it is a
			// pointer rather than a value with a magic zero.
			UnitCostIDR *money.IDR `json:"unit_cost_idr"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := o.SaveLine(r.Context(), actorFrom(r), chi.URLParam(r, "id"), service.LineInput{
			ProductID: req.ProductID, OwnerID: req.OwnerID, CountedQty: req.CountedQty,
			ReasonCode: req.ReasonCode, ReasonNote: req.ReasonNote, UnitCostIDR: req.UnitCostIDR,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		var cost *money.IDR
		if row.UnitCostIdr != nil {
			v := money.IDR(*row.UnitCostIdr)
			cost = &v
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"id": row.ID, "product_id": row.ProductID, "owner_id": row.OwnerID,
			"system_qty": row.SystemQty, "counted_qty": row.CountedQty,
			"variance": row.Variance, "reason_code": row.ReasonCode, "unit_cost_idr": cost,
		})
	}
}

func handlePostOpname(o *service.Opname) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			Reason string `json:"reason"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		got, err := o.Post(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.Reason)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"opname":        toOpnameDTO(got.Opname),
			"lines_posted":  got.LinesPosted,
			"surplus_units": got.SurplusUnits,
			"short_units":   got.ShortUnits,
			"cost_delta":    got.CostDelta,
		})
	}
}

// --- opening balances -------------------------------------------------------

func handleCarryInStock(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			AsOfDate string `json:"as_of_date"`
			Note     string `json:"note"`
			Lines    []struct {
				ProductID      string    `json:"product_id"`
				OwnerID        string    `json:"owner_id"`
				Qty            int64     `json:"qty"`
				CostTotalIDR   money.IDR `json:"cost_total_idr"`
				FakturReceived bool      `json:"faktur_received"`
				PPNPaidIDR     money.IDR `json:"ppn_paid_idr"`
				ExpiryDate     string    `json:"expiry_date"`
			} `json:"lines"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		lines := make([]service.OpeningStockLine, 0, len(req.Lines))
		for _, l := range req.Lines {
			lines = append(lines, service.OpeningStockLine{
				ProductID: l.ProductID, OwnerID: l.OwnerID, Qty: l.Qty,
				CostTotalIDR: l.CostTotalIDR, FakturReceived: l.FakturReceived,
				PPNPaidIDR: l.PPNPaidIDR, ExpiryDate: l.ExpiryDate,
			})
		}

		got, err := o.CarryInStock(r.Context(), actorFrom(r), service.OpeningStockInput{
			AsOfDate: req.AsOfDate, Note: req.Note, Lines: lines,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		layers := make([]layerDTO, 0, len(got.Layers))
		for _, l := range got.Layers {
			layers = append(layers, toLayerDTO(l))
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"layers": layers, "total_qty": got.TotalQty, "total_cost_idr": got.TotalCost,
		})
	}
}

type debtRequest struct {
	PartyID    string    `json:"party_id"`
	InvoiceNo  string    `json:"invoice_no"`
	AmountIDR  money.IDR `json:"amount_idr"`
	IncurredOn string    `json:"incurred_on"`
	DueDate    string    `json:"due_date"`
	Note       string    `json:"note"`
}

func (r debtRequest) toInput() service.OpeningDebtInput {
	return service.OpeningDebtInput{
		PartyID: r.PartyID, InvoiceNo: r.InvoiceNo, AmountIDR: r.AmountIDR,
		IncurredOn: r.IncurredOn, DueDate: r.DueDate, Note: r.Note,
	}
}

type debtDTO struct {
	ID             string    `json:"id"`
	PartyID        string    `json:"party_id"`
	Source         string    `json:"source"`
	InvoiceNo      *string   `json:"invoice_no"`
	AmountIDR      money.IDR `json:"amount_idr"`
	PaidIDR        money.IDR `json:"paid_idr"`
	OutstandingIDR money.IDR `json:"outstanding_idr"`
	IncurredOn     string    `json:"incurred_on"`
	DueDate        *string   `json:"due_date"`
	Note           *string   `json:"note"`
}

func toPayableDTO(p gen.PayableBalance) debtDTO {
	return debtDTO{
		ID: p.ID, PartyID: p.SupplierID, Source: p.Source, InvoiceNo: p.InvoiceNo,
		AmountIDR: money.IDR(p.AmountIdr), PaidIDR: money.IDR(p.PaidIdr),
		OutstandingIDR: money.IDR(p.OutstandingIdr), IncurredOn: p.IncurredOn,
		DueDate: p.DueDate, Note: p.Note,
	}
}

func toReceivableDTO(r gen.ReceivableBalance) debtDTO {
	return debtDTO{
		ID: r.ID, PartyID: r.CustomerID, Source: r.Source, InvoiceNo: r.InvoiceNo,
		AmountIDR: money.IDR(r.AmountIdr), PaidIDR: money.IDR(r.PaidIdr),
		OutstandingIDR: money.IDR(r.OutstandingIdr), IncurredOn: r.IncurredOn,
		DueDate: r.DueDate, Note: r.Note,
	}
}

func handleCarryInPayable(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req debtRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		row, err := o.CarryInPayable(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id": row.ID, "amount_idr": money.IDR(row.AmountIdr), "due_date": row.DueDate,
		})
	}
}

func handleCarryInReceivable(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req debtRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		row, err := o.CarryInReceivable(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id": row.ID, "amount_idr": money.IDR(row.AmountIdr), "due_date": row.DueDate,
		})
	}
}

func handleListPayables(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := o.ListPayables(r.Context(), entityID, r.URL.Query().Get("all") != "1")
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]debtDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toPayableDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleListReceivables(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := o.ListReceivables(r.Context(), entityID, r.URL.Query().Get("all") != "1")
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]debtDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toReceivableDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

type paymentRequest struct {
	AmountIDR money.IDR `json:"amount_idr"`
	PaidOn    string    `json:"paid_on"`
	Method    string    `json:"method"`
	Note      string    `json:"note"`
}

func (r paymentRequest) toInput() service.PaymentInput {
	return service.PaymentInput{
		AmountIDR: r.AmountIDR, PaidOn: r.PaidOn, Method: r.Method, Note: r.Note,
	}
}

func handlePayPayable(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req paymentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		row, err := o.PayPayable(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id": row.ID, "amount_idr": money.IDR(row.AmountIdr), "paid_on": row.PaidOn,
		})
	}
}

func handlePayReceivable(o *service.Opening) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req paymentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		row, err := o.PayReceivable(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"id": row.ID, "amount_idr": money.IDR(row.AmountIdr), "paid_on": row.PaidOn,
		})
	}
}
