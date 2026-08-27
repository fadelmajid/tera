package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// taxCaveat travels with every payload that carries a tax figure.
//
// A guardrail from CLAUDE.md, not decoration: this system computes an estimate
// from the data it was given, and the business's actual position depends on
// paperwork and rules it cannot see. It rides in the payload rather than being
// pasted into the screen so no client can show the number without it.
const taxCaveat = "Estimasi berdasarkan data di sistem ini. " +
	"Konfirmasikan dengan konsultan pajak Anda."

// --- wire shapes ------------------------------------------------------------

type taxRuleDTO struct {
	ID       string `json:"id"`
	EntityID string `json:"entity_id"`
	TaxType  string `json:"tax_type"`

	// RateBP is basis points and DPPNum/DPPDen are the DPP nilai lain as an
	// exact fraction — 1200 and 11/12 for non-luxury PPN. Sent as the three
	// integers the rule actually holds, never as a computed decimal: 11/12 has
	// no finite decimal form and 0.916666… is a number no regulation contains
	// (SPEC §2.1, INV-1).
	RateBP int64 `json:"rate_bp"`
	DPPNum int64 `json:"dpp_factor_num"`
	DPPDen int64 `json:"dpp_factor_den"`
	// EffectiveRateBP is the two combined, in basis points: 1100 for the
	// August 2026 rule. A convenience for the screen, and exact here only
	// because 12% of 11/12 happens to land on a whole basis point. The three
	// fields above remain the truth.
	EffectiveRateBP int64 `json:"effective_rate_bp"`

	Inclusive        bool   `json:"is_inclusive"`
	CalculationLevel string `json:"calculation_level"`
	RoundingMode     string `json:"rounding_mode"`
	RoundingUnit     int64  `json:"rounding_unit"`

	ValidFrom string  `json:"valid_from"`
	ValidTo   *string `json:"valid_to"`
	// InForce is whether this rule covers today in the company's own timezone.
	InForce bool `json:"in_force"`
	// Staged is a rule that has not started yet — the correct state for a
	// company that has registered for PKP with effect from a future tax period
	// (PMK 164/2023 Pasal 18).
	Staged bool `json:"staged"`

	// LegalRef is why the row says what it says. Shown on the screen so the
	// owner's konsultan pajak can check the configuration without reading code.
	LegalRef string  `json:"legal_ref"`
	Note     *string `json:"note"`

	CreatedAt int64  `json:"created_at"`
	ClosedAt  *int64 `json:"closed_at"`
}

func toTaxRuleDTO(r gen.TaxRule, today string) taxRuleDTO {
	out := taxRuleDTO{
		ID: r.ID, EntityID: r.EntityID, TaxType: r.TaxType,
		RateBP: r.RateBp, DPPNum: r.DppFactorNum, DPPDen: r.DppFactorDen,
		// rate_bp x dpp_num / dpp_den, in basis points.
		EffectiveRateBP:  r.RateBp * r.DppFactorNum / r.DppFactorDen,
		Inclusive:        r.IsInclusive == 1,
		CalculationLevel: r.CalculationLevel,
		RoundingMode:     r.RoundingMode, RoundingUnit: r.RoundingUnit,
		ValidFrom: r.ValidFrom, ValidTo: r.ValidTo,
		LegalRef: r.LegalRef, Note: r.Note,
		CreatedAt: r.CreatedAt, ClosedAt: r.ClosedAt,
	}
	out.Staged = r.ValidFrom > today
	out.InForce = !out.Staged && (r.ValidTo == nil || *r.ValidTo >= today)
	return out
}

type taxRuleRequest struct {
	TaxType          string `json:"tax_type"`
	RateBP           int64  `json:"rate_bp"`
	DPPNum           int64  `json:"dpp_factor_num"`
	DPPDen           int64  `json:"dpp_factor_den"`
	Inclusive        bool   `json:"is_inclusive"`
	CalculationLevel string `json:"calculation_level"`
	RoundingMode     string `json:"rounding_mode"`
	RoundingUnit     int64  `json:"rounding_unit"`
	ValidFrom        string `json:"valid_from"`
	ValidTo          string `json:"valid_to"`
	LegalRef         string `json:"legal_ref"`
	Note             string `json:"note"`
}

type closeRuleRequest struct {
	ValidTo string `json:"valid_to"`
	Reason  string `json:"reason"`
}

type deleteRuleRequest struct {
	Reason string `json:"reason"`
}

// --- handlers ---------------------------------------------------------------

func handleListTaxRules(t *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		rows, err := t.ListRules(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		today, err := t.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		rules := make([]taxRuleDTO, 0, len(rows))
		for _, row := range rows {
			rules = append(rules, toTaxRuleDTO(row, today))
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"today":  today,
			"rules":  rules,
			"caveat": taxCaveat,
		})
	}
}

func handleCreateTaxRule(t *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req taxRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := t.CreateRule(r.Context(), actorFrom(r), service.TaxRuleInput{
			Type: req.TaxType, RateBP: req.RateBP,
			DPPNum: req.DPPNum, DPPDen: req.DPPDen,
			Inclusive: req.Inclusive, CalculationLevel: req.CalculationLevel,
			RoundingMode: req.RoundingMode, RoundingUnit: req.RoundingUnit,
			ValidFrom: req.ValidFrom, ValidTo: req.ValidTo,
			LegalRef: req.LegalRef, Note: req.Note,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		entityID, _ := EntityFrom(r.Context())
		today, err := t.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"rule": toTaxRuleDTO(row, today), "caveat": taxCaveat,
		})
	}
}

// handleCloseTaxRule ends a rule's window. The only edit a rule ever gets
// (INV-4): a rate is changed by closing one row and opening another.
func handleCloseTaxRule(t *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req closeRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := t.CloseRule(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.ValidTo, req.Reason)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		entityID, _ := EntityFrom(r.Context())
		today, err := t.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"rule": toTaxRuleDTO(row, today), "caveat": taxCaveat,
		})
	}
}

func handleDeleteTaxRule(t *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req deleteRuleRequest
		if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
			return
		}

		if err := t.DeleteRule(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.Reason); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{"deleted": true})
	}
}

// handlePPNPosition is SPEC §2.4 over the wire, with the documents behind it.
func handlePPNPosition(t *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		got, err := t.Position(r.Context(), entityID, r.URL.Query().Get("masa"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		p := got.Position

		sales := make([]map[string]any, 0, len(got.Sales))
		for _, s := range got.Sales {
			sales = append(sales, map[string]any{
				"sale_id": s.ID, "invoice_no": s.InvoiceNo, "business_date": s.BusinessDate,
				"faktur_issued": s.FakturIssued == 1, "faktur_no": s.FakturNo,
				"customer_name": s.CustomerName,
				"dpp_idr":       money.IDR(s.DppIdr), "ppn_idr": money.IDR(s.PpnIdr),
				"total_idr": money.IDR(s.TotalIdr),
			})
		}
		purchases := make([]map[string]any, 0, len(got.Purchases))
		for _, pr := range got.Purchases {
			purchases = append(purchases, map[string]any{
				"purchase_id": pr.ID, "invoice_no": pr.InvoiceNo, "business_date": pr.BusinessDate,
				"faktur_received": pr.FakturReceived == 1, "faktur_no": pr.FakturNo,
				"supplier_name": pr.SupplierName,
				"ppn_idr":       money.IDR(pr.PpnIdr),
				// Zero whenever no faktur arrived, whatever was paid (INV-9).
				"creditable_ppn_idr": money.IDR(pr.CreditablePpnIdr),
				"total_idr":          money.IDR(pr.TotalIdr),
			})
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"title": "Posisi PPN per Masa Pajak",
			"masa":  got.Masa,
			"period": map[string]any{
				"from": got.From, "to": got.To,
			},
			"output": map[string]any{
				"with_faktur_idr": p.OutputWithFaktur,
				// TASKS 5.4 on the screen. A PKP owes output PPN on the
				// delivery whether or not the buyer took a faktur, and this is
				// the half of the liability nothing else in the business will
				// ever mention.
				"without_faktur_idr": p.OutputWithoutFaktur,
				"reversed_idr":       p.OutputReversed,
				"net_idr":            p.Output(),
			},
			"input": map[string]any{
				// The filter that makes purchase tracking worth building
				// (SPEC §2.4): only a faktur makes input PPN creditable.
				"creditable_idr": p.InputCreditable,
				"reversed_idr":   p.InputReversed,
				// Reported, never netted. This PPN is already in the cost of
				// the goods (SPEC §3.2); crediting it here as well would claim
				// the same rupiah twice.
				"non_creditable_idr": p.InputNonCreditable,
				"net_idr":            p.Input(),
			},
			// Negative is a lebih bayar, carried to the next masa or claimed at
			// the end of the book year (UU PPN Pasal 9 ayat (4)). Not clamped.
			"payable_idr": p.Payable(),
			"is_overpaid": p.IsOverpaid(),
			"sales":       sales,
			"purchases":   purchases,
			"caveat":      taxCaveat,
		})
	}
}

func mountTax(r chi.Router, cfg Config) {
	if cfg.Tax == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanSeeTaxPosition,
			"hanya pemilik dan manajer yang dapat melihat posisi PPN"))

		r.Get("/tax/rules", handleListTaxRules(cfg.Tax))
		r.Get("/reports/ppn", handlePPNPosition(cfg.Tax))
	})

	// Owner only. A tax rule decides what every subsequent sale charges, and it
	// is the configuration a konsultan pajak is shown.
	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanManageTaxRules,
			"hanya pemilik yang dapat mengubah aturan pajak"))

		r.Post("/tax/rules", handleCreateTaxRule(cfg.Tax))
		r.Post("/tax/rules/{id}/close", handleCloseTaxRule(cfg.Tax))
		r.Delete("/tax/rules/{id}", handleDeleteTaxRule(cfg.Tax))
	})
}
