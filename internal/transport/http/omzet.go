package http

import (
	stdhttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/omzet"
	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// The two figures cross the wire under names that cannot be mistaken for each
// other, and each carries its own label saying which it is.
//
// SPEC §5.1: the book-year cumulative is the legally binding number and the
// trailing twelve months is a momentum estimate. Most guidance online quotes
// the rolling figure, which is why a payload that shipped them as two
// interchangeable integers would be one relabelling away from telling an owner
// they have crossed a threshold they are nowhere near — or that they are safe
// when they are not.
const (
	cumulativeLabel = "Kumulatif tahun buku — angka yang mengikat"
	trailingLabel   = "12 bulan terakhir — estimasi laju, bukan angka resmi"

	omzetCaveat = "Estimasi berdasarkan data di sistem ini. " +
		"Konfirmasikan dengan konsultan pajak Anda."
)

func omzetPositionBody(c *service.Clock) map[string]any {
	p := c.Position

	body := map[string]any{
		"book_year": p.BookYear,
		"as_of":     p.AsOf,
		// How far into the year the figures run. A clock read on 30 June shows
		// turnover to 30 June, and saying so stops the figure being read as a
		// full year that came in low.
		"counted_to": p.CountedTo,
		"window":     map[string]any{"from": p.Window.From, "to": p.Window.To},

		"threshold": map[string]any{
			"amount_idr": p.Threshold.AmountIDR,
			"watch_bp":   p.Threshold.WatchBP,
			"warn_bp":    p.Threshold.WarnBP,
			// Shown so the owner's konsultan pajak can check the figure against
			// the regulation rather than take it on trust.
			"legal_ref": p.Threshold.LegalRef,
		},

		// The legally binding figure (SPEC §5.1). Cumulative per book year,
		// reset annually.
		"cumulative": map[string]any{
			"label":         cumulativeLabel,
			"amount_idr":    p.Cumulative,
			"percent_bp":    p.PercentBP,
			"remaining_idr": p.Remaining,
			"entries":       p.Entries,
		},
		// A pace estimate and nothing more. Never what the alarm reads.
		"trailing_12m": map[string]any{
			"label":      trailingLabel,
			"amount_idr": p.Trailing12,
			"window": map[string]any{
				"from": p.Trailing12Window.From, "to": p.Trailing12Window.To,
			},
			"is_estimate": true,
		},

		"state": string(p.State),
		// Both companies are tracked but only a non-PKP one can still cross
		// (SPEC §5.3): a CROSSED banner on a company registered years ago is
		// noise, and the screen needs to know which it is looking at.
		"is_pkp": c.IsPKP,
		"base":   string(c.Base),

		"book_years": c.BookYears,
		"caveat":     omzetCaveat,
	}

	if p.State == omzet.Crossed {
		body["crossed"] = map[string]any{
			"on": p.CrossedOn,
			// Both dates, always. The gap between them is the most
			// misunderstood part of this rule and most of the feature's value:
			// a business told only the first registers and starts charging PPN
			// it does not yet owe, and told only the second misses the
			// registration entirely.
			"register_by": p.RegisterBy,
			"vat_starts":  p.VATStarts,
			// The peak explains a state that outlives the figure. A year that
			// crossed in September and took returns in November reports a
			// cumulative below the threshold and a state of CROSSED, and
			// without this that reads like a bug rather than the rule.
			"peak_idr": p.Peak,
			"peak_on":  p.PeakOn,
		}
	}
	return body
}

func handleOmzetPosition(o *service.Omzet) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		q := r.URL.Query()

		got, err := o.Position(r.Context(), entityID, q.Get("book_year"), q.Get("as_of"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, omzetPositionBody(&got))
	}
}

// handleOmzetLedger is the drill-down: every figure on the clock decomposes
// into the documents that produced it.
func handleOmzetLedger(o *service.Omzet) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		year, err := strconv.Atoi(r.URL.Query().Get("book_year"))
		if err != nil {
			writeJSON(w, stdhttp.StatusBadRequest,
				map[string]any{"error": "tahun buku harus angka"})
			return
		}

		rows, err := o.Ledger(r.Context(), entityID, year)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		out := make([]map[string]any, 0, len(rows))
		for _, l := range rows {
			out = append(out, map[string]any{
				"id": l.ID, "book_year": l.BookYear,
				// Not always the day the row was written: a void carries the
				// original sale's date (SPEC §5.4).
				"effective_date": l.EffectiveDate,
				"event_type":     l.EventType,
				"amount_idr":     money.IDR(l.SignedAmountIdr),
				"source_txn_id":  l.SourceTxnID,
				"invoice_no":     l.SaleInvoiceNo,
				"note":           l.Note,
				"created_at":     l.CreatedAt,
			})
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"book_year": year, "entries": out, "caveat": omzetCaveat,
		})
	}
}

// --- the threshold, effective-dated (INV-4) ---------------------------------

func omzetThresholdBody(t gen.OmzetThreshold, today string) map[string]any {
	staged := t.ValidFrom > today
	inForce := !staged && (t.ValidTo == nil || *t.ValidTo >= today)

	return map[string]any{
		"id": t.ID, "amount_idr": money.IDR(t.AmountIdr),
		"watch_bp": t.WatchBp, "warn_bp": t.WarnBp,
		// Two readings each, both implemented, because which regulation governs
		// PKP registration is not settled — SPEC §5.2 cites a final-PPh
		// regulation for a PPN deadline. Answering it is this row.
		"register_by_policy": t.RegisterByPolicy,
		"vat_starts_policy":  t.VatStartsPolicy,
		"valid_from":         t.ValidFrom, "valid_to": t.ValidTo,
		"in_force": inForce, "staged": staged,
		"legal_ref": t.LegalRef, "note": t.Note,
		"created_at": t.CreatedAt, "closed_at": t.ClosedAt,
	}
}

type thresholdRequest struct {
	AmountIDR        money.IDR `json:"amount_idr"`
	WatchBP          int64     `json:"watch_bp"`
	WarnBP           int64     `json:"warn_bp"`
	RegisterByPolicy string    `json:"register_by_policy"`
	VATStartsPolicy  string    `json:"vat_starts_policy"`
	ValidFrom        string    `json:"valid_from"`
	ValidTo          string    `json:"valid_to"`
	LegalRef         string    `json:"legal_ref"`
	Note             string    `json:"note"`
}

func handleListThresholds(o *service.Omzet, tax *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		rows, err := o.ListThresholds(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		// The company's own date, so a laptop with a wrong clock does not get
		// to decide which threshold the screen calls current (INV-5).
		today, err := tax.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		out := make([]map[string]any, 0, len(rows))
		for _, t := range rows {
			out = append(out, omzetThresholdBody(t, today))
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"today": today, "thresholds": out, "caveat": omzetCaveat,
		})
	}
}

func handleCreateThreshold(o *service.Omzet, tax *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req thresholdRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := o.CreateThreshold(r.Context(), actorFrom(r), service.ThresholdInput{
			AmountIDR: req.AmountIDR, WatchBP: req.WatchBP, WarnBP: req.WarnBP,
			RegisterByPolicy: req.RegisterByPolicy, VATStartsPolicy: req.VATStartsPolicy,
			ValidFrom: req.ValidFrom, ValidTo: req.ValidTo,
			LegalRef: req.LegalRef, Note: req.Note,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		entityID, _ := EntityFrom(r.Context())
		today, err := tax.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusCreated, map[string]any{
			"threshold": omzetThresholdBody(row, today), "caveat": omzetCaveat,
		})
	}
}

func handleCloseThreshold(o *service.Omzet, tax *service.Tax) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req closeRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		row, err := o.CloseThreshold(r.Context(), actorFrom(r),
			chi.URLParam(r, "id"), req.ValidTo, req.Reason)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		entityID, _ := EntityFrom(r.Context())
		today, err := tax.Today(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"threshold": omzetThresholdBody(row, today), "caveat": omzetCaveat,
		})
	}
}

type omzetBaseRequest struct {
	Base string `json:"base"`
	Note string `json:"note"`
}

func handleSetOmzetBase(o *service.Omzet) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req omzetBaseRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		base, err := o.SetBase(r.Context(), actorFrom(r), req.Base, req.Note)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, map[string]any{"base": string(base)})
	}
}

func mountOmzet(r chi.Router, cfg Config) {
	if cfg.Omzet == nil || cfg.Tax == nil {
		return
	}

	r.Group(func(r chi.Router) {
		// The same gate as the PPN position: a figure the business plans
		// around, not something a cashier needs at the till.
		r.Use(requireEntityRole(service.Role.CanSeeTaxPosition,
			"hanya pemilik dan manajer yang dapat melihat omzet terhadap batas PKP"))

		r.Get("/omzet", handleOmzetPosition(cfg.Omzet))
		r.Get("/omzet/ledger", handleOmzetLedger(cfg.Omzet))
		r.Get("/omzet/thresholds", handleListThresholds(cfg.Omzet, cfg.Tax))
	})

	r.Group(func(r chi.Router) {
		// Owner only, like a tax rule: the threshold decides when this business
		// is told it must register, and it is the configuration a konsultan
		// pajak is shown.
		r.Use(requireEntityRole(service.Role.CanManageTaxRules,
			"hanya pemilik yang dapat mengubah batas omzet"))

		r.Post("/omzet/thresholds", handleCreateThreshold(cfg.Omzet, cfg.Tax))
		r.Post("/omzet/thresholds/{id}/close", handleCloseThreshold(cfg.Omzet, cfg.Tax))
		r.Put("/omzet/base", handleSetOmzetBase(cfg.Omzet))
	})
}
