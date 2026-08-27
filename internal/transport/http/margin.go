package http

import (
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/domain/margin"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/service"
)

// The report's own disclaimer, which is not the compliance notice and must not
// be confused with it. This is a scope statement rather than a tax caveat:
// gross margin has not paid rent, and a family planning around it as if it had
// would be planning around a number that does not exist (REQUIREMENTS §5).
const (
	reportTitle = "Laporan Margin per Owner"
	reportNote  = "Margin kotor: pendapatan dikurangi HPP dari lapisan stok yang benar-benar terpakai. " +
		"Biaya bersama seperti listrik, gaji, dan sewa TIDAK termasuk dan diselesaikan di luar aplikasi."
	reportCaveat = "Angka ini bukan laba bersih dan bukan Laba Rugi."
)

// --- wire shapes ------------------------------------------------------------
//
// Every level of the tree is on the wire, because the drill-down is a hard
// requirement rather than a screen feature (SPEC §4.2). One request returns the
// owner totals, the sales behind them, the products in each sale, and the
// individual FIFO layers each drew — so no figure on screen can exist without
// the rows that produced it being one click away.

type layerDrawDTO struct {
	ConsumptionID string    `json:"consumption_id"`
	LayerID       string    `json:"layer_id"`
	Qty           int64     `json:"qty"`
	CostIDR       money.IDR `json:"cost_idr"`
	IsReversal    bool      `json:"is_reversal"`
	AcquiredAt    int64     `json:"acquired_at"`
	Source        string    `json:"source"`
	LayerQtyIn    int64     `json:"layer_qty_in"`
	LayerCostIDR  money.IDR `json:"layer_cost_total_idr"`
	// INV-9 on the wire. Without the faktur the PPN paid on this layer was
	// never creditable and became cost, so the same purchase price yields a
	// layer roughly 11% dearer — which is often the entire answer to "why is
	// my margin lower this month".
	FakturReceived bool `json:"faktur_received"`
}

type productLineDTO struct {
	ProductID   string         `json:"product_id"`
	ProductCode string         `json:"product_code"`
	ProductName string         `json:"product_name"`
	Qty         int64          `json:"qty"`
	RevenueIDR  money.IDR      `json:"revenue_idr"`
	PPNIDR      money.IDR      `json:"ppn_idr"`
	COGSIDR     money.IDR      `json:"cogs_idr"`
	MarginIDR   money.IDR      `json:"margin_idr"`
	Layers      []layerDrawDTO `json:"layers"`
}

type marginSaleDTO struct {
	SaleID       string           `json:"sale_id"`
	InvoiceNo    string           `json:"invoice_no"`
	BusinessDate string           `json:"business_date"`
	OccurredAt   int64            `json:"occurred_at"`
	CustomerName string           `json:"customer_name"`
	RevenueIDR   money.IDR        `json:"revenue_idr"`
	PPNIDR       money.IDR        `json:"ppn_idr"`
	TenderedIDR  money.IDR        `json:"tendered_idr"`
	COGSIDR      money.IDR        `json:"cogs_idr"`
	MarginIDR    money.IDR        `json:"margin_idr"`
	Products     []productLineDTO `json:"products"`
}

type marginReturnDTO struct {
	ReturnID      string `json:"return_id"`
	SaleID        string `json:"sale_id"`
	SaleInvoiceNo string `json:"sale_invoice_no"`
	// All three dates travel: when the goods came back, when they were sold,
	// and which of the two the rule in force actually counted. Nobody reading
	// the report should have to reconstruct which rule ran (SPEC §4.4).
	BusinessDate     string `json:"business_date"`
	SaleBusinessDate string `json:"sale_business_date"`
	EffectiveDate    string `json:"effective_date"`
	CrossesPeriod    bool   `json:"crosses_period"`
	Reason           string `json:"reason"`
	// RefundIDR is the whole sum handed back, PPN included -- what the customer
	// received. PPNReversedIDR is the tax inside it, and RevenueReversedIDR is
	// the difference, which is what actually comes off the margin.
	RefundIDR          money.IDR `json:"refund_idr"`
	PPNReversedIDR     money.IDR `json:"ppn_reversed_idr"`
	RevenueReversedIDR money.IDR `json:"revenue_reversed_idr"`
	COGSReversedIDR    money.IDR `json:"cogs_reversed_idr"`
	// MarginIDR is the margin the return took back out, positive when margin
	// was removed.
	MarginIDR money.IDR        `json:"margin_idr"`
	Products  []productLineDTO `json:"products"`
}

type ownerMarginDTO struct {
	// OwnerID is empty for the company bucket (R2.2) — the same null the
	// database carries, not a sentinel string.
	OwnerID   string `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	IsCompany bool   `json:"is_company"`

	// RevenueIDR is net of PPN, because COGS is net of creditable PPN
	// (SPEC §3.2) and the two have to compare. PPNIDR is the tax that was
	// collected alongside it and TenderedIDR is their sum -- what the customer
	// actually handed over, so a margin line can be reconciled against a day's
	// takings without anyone doing the subtraction by hand.
	RevenueIDR       money.IDR `json:"revenue_idr"`
	PPNIDR           money.IDR `json:"ppn_idr"`
	TenderedIDR      money.IDR `json:"tendered_idr"`
	COGSIDR          money.IDR `json:"cogs_idr"`
	GrossMarginIDR   money.IDR `json:"gross_margin_idr"`
	ReturnRefundIDR  money.IDR `json:"return_refund_idr"`
	ReturnPPNIDR     money.IDR `json:"return_ppn_idr"`
	ReturnRevenueIDR money.IDR `json:"return_revenue_idr"`
	ReturnCOGSIDR    money.IDR `json:"return_cogs_idr"`
	ReturnMarginIDR  money.IDR `json:"return_margin_idr"`
	NetRevenueIDR    money.IDR `json:"net_revenue_idr"`
	NetCOGSIDR       money.IDR `json:"net_cogs_idr"`
	MarginIDR        money.IDR `json:"margin_idr"`

	Sales   []marginSaleDTO   `json:"sales"`
	Returns []marginReturnDTO `json:"returns"`
	// LaterReturns are goods sold in this period that came back in another and
	// were counted there. Context only — no figure above includes them.
	// See DECISIONS D-012.
	LaterReturns []marginReturnDTO `json:"later_returns"`
}

func toLayerDrawDTO(l margin.LayerDraw) layerDrawDTO {
	return layerDrawDTO{
		ConsumptionID: l.ConsumptionID, LayerID: l.LayerID, Qty: l.Qty, CostIDR: l.Cost,
		IsReversal: l.IsReversal, AcquiredAt: l.AcquiredAt.Unix(), Source: l.Source,
		LayerQtyIn: l.LayerQtyIn, LayerCostIDR: l.LayerCostTotal,
		FakturReceived: l.FakturReceived,
	}
}

func toProductLineDTO(p margin.ProductLine) productLineDTO {
	layers := make([]layerDrawDTO, 0, len(p.Layers))
	for _, l := range p.Layers {
		layers = append(layers, toLayerDrawDTO(l))
	}
	return productLineDTO{
		ProductID: p.ProductID, ProductCode: p.ProductCode, ProductName: p.ProductName,
		Qty: p.Qty, RevenueIDR: p.Revenue, PPNIDR: p.PPN,
		COGSIDR: p.COGS, MarginIDR: p.Margin(),
		Layers: layers,
	}
}

func toOwnerMarginDTO(o margin.OwnerReport) ownerMarginDTO {
	sales := make([]marginSaleDTO, 0, len(o.Sales))
	for _, s := range o.Sales {
		products := make([]productLineDTO, 0, len(s.Products))
		for _, p := range s.Products {
			products = append(products, toProductLineDTO(p))
		}
		sales = append(sales, marginSaleDTO{
			SaleID: s.SaleID, InvoiceNo: s.InvoiceNo, BusinessDate: s.BusinessDate,
			OccurredAt: s.OccurredAt.Unix(), CustomerName: s.CustomerName,
			RevenueIDR: s.Revenue, PPNIDR: s.PPN, TenderedIDR: s.Tendered(),
			COGSIDR: s.COGS, MarginIDR: s.Margin(),
			Products: products,
		})
	}

	returns, laterReturns := toReturnDTOs(o.Returns), toReturnDTOs(o.LaterReturns)

	return ownerMarginDTO{
		OwnerID: string(o.OwnerID), OwnerName: o.OwnerName, IsCompany: o.IsCompany,
		RevenueIDR: o.Revenue, PPNIDR: o.PPN, TenderedIDR: o.Tendered(),
		COGSIDR: o.COGS, GrossMarginIDR: o.GrossMargin(),
		ReturnRefundIDR: o.ReturnRefund, ReturnPPNIDR: o.ReturnPPN,
		ReturnRevenueIDR: o.ReturnRevenue(), ReturnCOGSIDR: o.ReturnCOGS,
		ReturnMarginIDR: o.ReturnMargin(), NetRevenueIDR: o.NetRevenue(),
		NetCOGSIDR: o.NetCOGS(), MarginIDR: o.Margin(),
		Sales: sales, Returns: returns, LaterReturns: laterReturns,
	}
}

func toReturnDTOs(rows []margin.ReturnReport) []marginReturnDTO {
	out := make([]marginReturnDTO, 0, len(rows))
	for _, r := range rows {
		products := make([]productLineDTO, 0, len(r.Products))
		for _, p := range r.Products {
			products = append(products, toProductLineDTO(p))
		}
		out = append(out, marginReturnDTO{
			ReturnID: r.ReturnID, SaleID: r.SaleID, SaleInvoiceNo: r.SaleInvoiceNo,
			BusinessDate: r.BusinessDate, SaleBusinessDate: r.SaleBusinessDate,
			EffectiveDate: r.EffectiveDate, CrossesPeriod: r.CrossesPeriod,
			Reason: r.Reason, RefundIDR: r.Refund, PPNReversedIDR: r.PPNReversed,
			RevenueReversedIDR: r.RevenueReversed(), COGSReversedIDR: r.COGSReversed,
			MarginIDR: r.Margin(), Products: products,
		})
	}
	return out
}

func returnRuleBody(r service.ReturnRule) map[string]any {
	return map[string]any{
		"rule": string(r.Rule),
		// False means nobody has touched it and the company is running on the
		// default (D-012). A settings detail, not a warning: the report states
		// the rule it ran under either way.
		"chosen":     r.Chosen,
		"note":       r.Note,
		"updated_at": r.UpdatedAt.Unix(),
	}
}

// --- handlers ---------------------------------------------------------------

func handleMarginReport(m *service.Margin) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())

		report, err := m.Report(r.Context(), entityID,
			r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		if err != nil {
			writeServiceError(w, err)
			return
		}

		owners := make([]ownerMarginDTO, 0, len(report.Owners))
		for _, o := range report.Owners {
			owners = append(owners, toOwnerMarginDTO(o))
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			// The title travels with the payload so no client can relabel it.
			// R2.4 money moves on this figure and it is gross margin, so it is
			// never Laba Rugi (REQUIREMENTS §5).
			"title":  reportTitle,
			"note":   reportNote,
			"caveat": reportCaveat,
			"period": map[string]any{"from": report.Period.From, "to": report.Period.To},
			// The rule the figures were produced under, so no client can show
			// a settlement figure without being able to say which rule placed
			// the returns (SPEC §4.4, D-012).
			"return_rule": string(report.ReturnPeriod),
			"owners":      owners,
			"totals": map[string]any{
				"revenue_idr":        report.Totals.Revenue,
				"ppn_idr":            report.Totals.PPN,
				"tendered_idr":       report.Totals.Tendered(),
				"cogs_idr":           report.Totals.COGS,
				"gross_margin_idr":   report.Totals.GrossMargin(),
				"return_refund_idr":  report.Totals.ReturnRefund,
				"return_ppn_idr":     report.Totals.ReturnPPN,
				"return_revenue_idr": report.Totals.ReturnRevenue(),
				"return_cogs_idr":    report.Totals.ReturnCOGS,
				"return_margin_idr":  report.Totals.ReturnMargin(),
				"net_revenue_idr":    report.Totals.NetRevenue(),
				"net_cogs_idr":       report.Totals.NetCOGS(),
				"margin_idr":         report.Totals.Margin(),
			},
		})
	}
}

func handleGetReturnRule(m *service.Margin) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entityID, _ := EntityFrom(r.Context())
		got, err := m.ReturnRuleFor(r.Context(), entityID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, returnRuleBody(got))
	}
}

func handleSetReturnRule(m *service.Margin) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req struct {
			Rule string `json:"rule"`
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		got, err := m.SetReturnRule(r.Context(), actorFrom(r), req.Rule, req.Note)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, returnRuleBody(got))
	}
}

// --- routes -----------------------------------------------------------------

// mountMargin wires Laporan Margin per Owner.
//
// D-009: owners and managers see owner margin figures; regular staff see none,
// and not a reduced version either — a cashier is not one of the family members
// products are attributed to, so "other owners' figures" means all of them.
// Enforced here rather than on the screen, because a margin figure that reaches
// the browser has already left the building.
//
// Choosing the returns-period rule is an owner's decision, not a manager's: it
// changes what every past report says and therefore what has already been
// settled (SPEC §4.4, R7.3).
func mountMargin(r chi.Router, cfg Config) {
	if cfg.Margin == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanSeeAllOwnerMargin,
			"hanya pemilik dan manajer yang dapat melihat laporan margin per owner"))

		r.Get("/reports/margin", handleMarginReport(cfg.Margin))
		r.Get("/reports/margin/return-rule", handleGetReturnRule(cfg.Margin))
	})

	r.Group(func(r chi.Router) {
		r.Use(requireEntityRole(service.Role.CanManageUsers,
			"hanya pemilik yang dapat menetapkan aturan periode retur"))

		r.Put("/reports/margin/return-rule", handleSetReturnRule(cfg.Margin))
	})
}
