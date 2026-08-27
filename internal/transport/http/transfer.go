package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// --- wire shapes ------------------------------------------------------------

type transferDTO struct {
	ID           string `json:"id"`
	FromEntityID string `json:"from_entity_id"`
	ToEntityID   string `json:"to_entity_id"`
	TransferNo   string `json:"transfer_no"`
	BusinessDate string `json:"business_date"`
	OccurredAt   int64  `json:"occurred_at"`

	// Snapshotted at the time of the movement, not looked up now: PKP status
	// can change, and the tax treatment of a transfer is a fact about the day
	// it happened (INV-3).
	FromIsPKP bool `json:"from_is_pkp"`
	ToIsPKP   bool `json:"to_is_pkp"`

	// R4.5. True only on the direction that destroys input credit, and only
	// because a person confirmed it.
	CreditLossAck   bool      `json:"credit_loss_ack"`
	ForfeitedPPNIDR money.IDR `json:"forfeited_ppn_idr"`

	CostTotalIDR money.IDR `json:"cost_total_idr"`
	PPNIDR       money.IDR `json:"ppn_idr"`
	// What the receiving company owes the sending one (D-014).
	AmountIDR    money.IDR `json:"amount_idr"`
	FakturIssued bool      `json:"faktur_issued"`
	FakturNo     *string   `json:"faktur_no"`
	Note         *string   `json:"note"`
}

func toTransferDTO(t gen.Transfer) transferDTO {
	return transferDTO{
		ID: t.ID, FromEntityID: t.FromEntityID, ToEntityID: t.ToEntityID,
		TransferNo: t.TransferNo, BusinessDate: t.BusinessDate, OccurredAt: t.OccurredAt,
		FromIsPKP: t.FromIsPkp == 1, ToIsPKP: t.ToIsPkp == 1,
		CreditLossAck: t.CreditLossAck == 1, ForfeitedPPNIDR: money.IDR(t.ForfeitedPpnIdr),
		CostTotalIDR: money.IDR(t.CostTotalIdr), PPNIDR: money.IDR(t.PpnIdr),
		AmountIDR: money.IDR(t.AmountIdr), FakturIssued: t.FakturIssued == 1,
		FakturNo: t.FakturNo, Note: t.Note,
	}
}

func toTransferListDTO(t gen.ListTransfersRow) map[string]any {
	return map[string]any{
		"id": t.ID, "transfer_no": t.TransferNo, "business_date": t.BusinessDate,
		"occurred_at":       t.OccurredAt,
		"from_entity_id":    t.FromEntityID,
		"from_entity_name":  t.FromEntityName,
		"to_entity_id":      t.ToEntityID,
		"to_entity_name":    t.ToEntityName,
		"from_is_pkp":       t.FromIsPkp == 1,
		"to_is_pkp":         t.ToIsPkp == 1,
		"credit_loss_ack":   t.CreditLossAck == 1,
		"forfeited_ppn_idr": money.IDR(t.ForfeitedPpnIdr),
		"cost_total_idr":    money.IDR(t.CostTotalIdr),
		"ppn_idr":           money.IDR(t.PpnIdr),
		"amount_idr":        money.IDR(t.AmountIdr),
		"faktur_issued":     t.FakturIssued == 1,
	}
}

type transferRequest struct {
	ToEntityID   string `json:"to_entity_id"`
	TransferDate string `json:"transfer_date"`
	FakturIssued bool   `json:"faktur_issued"`
	FakturNo     string `json:"faktur_no"`
	Note         string `json:"note"`
	// R4.5's blocking confirmation, carried as data so the server can refuse
	// without it. A client cannot make this transfer by ignoring a warning.
	AcknowledgeCreditLoss bool `json:"acknowledge_credit_loss"`
	Lines                 []struct {
		ProductID string    `json:"product_id"`
		Qty       int64     `json:"qty"`
		PPNIDR    money.IDR `json:"ppn_idr"`
	} `json:"lines"`
}

func (r transferRequest) toInput() service.TransferInput {
	lines := make([]service.TransferLineInput, 0, len(r.Lines))
	for _, l := range r.Lines {
		lines = append(lines, service.TransferLineInput{
			ProductID: l.ProductID, Qty: l.Qty, PPNIDR: l.PPNIDR,
		})
	}
	return service.TransferInput{
		ToEntityID: r.ToEntityID, TransferDate: r.TransferDate,
		FakturIssued: r.FakturIssued, FakturNo: r.FakturNo, Note: r.Note,
		AcknowledgeCreditLoss: r.AcknowledgeCreditLoss, Lines: lines,
	}
}

// --- handlers ---------------------------------------------------------------

// handleTransferPreview answers "what would this do", so the confirmation R4.5
// requires can quote a figure rather than a warning nobody reads.
//
// A POST that writes nothing: it takes a cart, which does not fit in a query
// string. Exempt from idempotency caching, because it plans against live stock
// and a replayed answer would describe a shelf that has since moved.
func handleTransferPreview(tr *service.Transfers) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req transferRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		got, err := tr.Preview(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}

		lines := make([]map[string]any, 0, len(got.Lines))
		for _, l := range got.Lines {
			layers := make([]map[string]any, 0, len(l.Layers))
			for _, c := range l.Layers {
				layers = append(layers, map[string]any{
					"layer_id": c.LayerID, "qty": c.QtyOut, "cost_idr": c.Cost,
				})
			}
			lines = append(lines, map[string]any{
				"product_id": l.ProductID, "product_code": l.ProductCode,
				"product_name": l.ProductName, "owner_id": l.OwnerID, "owner_name": l.OwnerName,
				"qty": l.Qty, "cost_idr": l.Cost, "ppn_idr": l.PPN,
				"forfeited_ppn_idr": l.ForfeitedPPN, "layers": layers,
			})
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"direction":        got.Direction.String(),
			"taxable_delivery": got.TaxableDelivery,
			// The client must show a blocking confirmation when this is true
			// and send acknowledge_credit_loss, or the write is refused.
			"destroys_input_credit": got.DestroysInputCredit,
			"cost_total_idr":        got.Cost,
			"ppn_idr":               got.PPN,
			"amount_idr":            got.Amount,
			"forfeited_ppn_idr":     got.ForfeitedPPN,
			"lines":                 lines,
		})
	}
}

func handleCreateTransfer(tr *service.Transfers) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req transferRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		got, err := tr.Create(r.Context(), actorFrom(r), req.toInput())
		if err != nil {
			writeServiceError(w, err)
			return
		}

		lines := make([]map[string]any, 0, len(got.Lines))
		for _, l := range got.Lines {
			lines = append(lines, map[string]any{
				"id": l.ID, "product_id": l.ProductID, "owner_id": l.OwnerID,
				"qty": l.Qty, "cost_total_idr": money.IDR(l.CostTotalIdr),
				"ppn_idr": money.IDR(l.PpnIdr), "dest_layer_id": l.DestLayerID,
			})
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"transfer": toTransferDTO(got.Transfer),
			"lines":    lines,
			// Both halves of the movement, so a client can show that the stock
			// actually moved rather than only that a document was written.
			"consumptions":      got.Consumptions,
			"cost_idr":          got.Cost,
			"forfeited_ppn_idr": got.ForfeitedPPN,
		})
	}
}

func handleListTransfers(tr *service.Transfers) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := tr.List(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, toTransferListDTO(row))
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

func handleGetTransfer(tr *service.Transfers) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		transfer, lines, err := tr.Get(r.Context(), entityID, chi.URLParam(r, "id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(lines))
		for _, l := range lines {
			out = append(out, map[string]any{
				"id": l.ID, "product_id": l.ProductID, "product_code": l.ProductCode,
				"product_name": l.ProductName, "product_unit": l.ProductUnit,
				"owner_id": l.OwnerID, "owner_name": l.OwnerName, "qty": l.Qty,
				"cost_total_idr": money.IDR(l.CostTotalIdr), "ppn_idr": money.IDR(l.PpnIdr),
				"forfeited_ppn_idr": money.IDR(l.ForfeitedPpnIdr),
				"dest_layer_id":     l.DestLayerID,
			})
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"transfer": toTransferDTO(transfer), "lines": out,
		})
	}
}

// handleInterCompanyPosition is D-014: what the two companies owe each other,
// netted. Deliberately its own surface rather than a line in the hutang report.
func handleInterCompanyPosition(tr *service.Transfers) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		rows, err := tr.Position(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(rows))
		for _, p := range rows {
			out = append(out, map[string]any{
				"counterparty_id": p.CounterpartyID, "counterparty_name": p.CounterpartyName,
				"out_idr": p.Out, "in_idr": p.In,
				"out_count": p.OutCount, "in_count": p.InCount,
				// Positive means the counterparty owes this company.
				"net_idr": p.Net,
			})
		}
		writeJSON(w, stdhttp.StatusOK, out)
	}
}

// --- routes -----------------------------------------------------------------

// mountTransfers wires the inter-company movement.
//
// Manager or above, for the same reason purchasing is (R10.4): a transfer sets
// the cost basis of the stock it creates and can permanently destroy input PPN
// credit (R4.5). It is a margin-bearing decision, not data entry.
func mountTransfers(r chi.Router, cfg Config) {
	if cfg.Transfers == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanEnterPurchases,
			"hanya pemilik dan manajer yang dapat memindahkan stok antar perusahaan"))

		r.Post("/transfers/preview", handleTransferPreview(cfg.Transfers))
		r.Post("/transfers", handleCreateTransfer(cfg.Transfers))
		r.Get("/transfers", handleListTransfers(cfg.Transfers))
		r.Get("/transfers/{id}", handleGetTransfer(cfg.Transfers))
		r.Get("/reports/inter-company", handleInterCompanyPosition(cfg.Transfers))
	})
}
