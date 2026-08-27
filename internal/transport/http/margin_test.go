package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
	"github.com/fadelmajid/tera/internal/store/seed"
)

type marginReportBody struct {
	Title  string `json:"title"`
	Note   string `json:"note"`
	Caveat string `json:"caveat"`
	Period struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"period"`
	ReturnRule string `json:"return_rule"`
	Owners     []struct {
		OwnerID     string `json:"owner_id"`
		OwnerName   string `json:"owner_name"`
		IsCompany   bool   `json:"is_company"`
		RevenueIDR  int64  `json:"revenue_idr"`
		PPNIDR      int64  `json:"ppn_idr"`
		TenderedIDR int64  `json:"tendered_idr"`
		COGSIDR     int64  `json:"cogs_idr"`
		MarginIDR   int64  `json:"margin_idr"`
		Sales       []struct {
			InvoiceNo  string `json:"invoice_no"`
			RevenueIDR int64  `json:"revenue_idr"`
			COGSIDR    int64  `json:"cogs_idr"`
			Products   []struct {
				ProductCode string `json:"product_code"`
				COGSIDR     int64  `json:"cogs_idr"`
				Layers      []struct {
					LayerID        string `json:"layer_id"`
					Qty            int64  `json:"qty"`
					CostIDR        int64  `json:"cost_idr"`
					FakturReceived bool   `json:"faktur_received"`
				} `json:"layers"`
			} `json:"products"`
		} `json:"sales"`
		Returns []struct {
			EffectiveDate string `json:"effective_date"`
			CrossesPeriod bool   `json:"crosses_period"`
			RefundIDR     int64  `json:"refund_idr"`
		} `json:"returns"`
		LaterReturns []struct {
			EffectiveDate    string `json:"effective_date"`
			SaleBusinessDate string `json:"sale_business_date"`
			RefundIDR        int64  `json:"refund_idr"`
		} `json:"later_returns"`
	} `json:"owners"`
	Totals struct {
		MarginIDR int64 `json:"margin_idr"`
	} `json:"totals"`
}

// cast builds one company with all three roles in it, so the authority rules
// can be exercised against the same data rather than three different worlds.
func cast(t *testing.T) (c *client, entityID string) {
	t.Helper()

	c, auth, q, ctx := newClient(t)

	entity, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "PKP", Name: "PT Sehat Sentosa", IsPkp: 1,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("entity: %v", err)
	}

	// A PKP company owes output PPN on every taxable delivery, so it cannot
	// ring a sale with no rule in force (TASKS 5.4). Real companies are seeded
	// when they are created; this one is built row by row, so it is seeded
	// here -- from the same source, so the default moving moves this with it.
	th := seed.OmzetThresholdFor()
	if _, err := q.CreateOmzetThreshold(ctx, gen.CreateOmzetThresholdParams{
		ID: store.NewID(), EntityID: entity.ID, AmountIdr: th.AmountIDR,
		WatchBp: th.WatchBP, WarnBp: th.WarnBP,
		RegisterByPolicy: th.RegisterByPolicy, VatStartsPolicy: th.VATStartsPolicy,
		ValidFrom: th.ValidFrom, LegalRef: th.LegalRef,
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("seed omzet threshold: %v", err)
	}

	for _, r := range seed.TaxRulesFor(true) {
		if _, err := q.CreateTaxRule(ctx, gen.CreateTaxRuleParams{
			ID: store.NewID(), EntityID: entity.ID, TaxType: r.Type,
			RateBp: r.RateBP, DppFactorNum: r.DPPNum, DppFactorDen: r.DPPDen,
			IsInclusive: 1, CalculationLevel: r.Level,
			RoundingMode: r.Rounding, RoundingUnit: r.RoundingUnit,
			ValidFrom: r.ValidFrom, LegalRef: r.LegalRef,
			CreatedAt: time.Now().Unix(),
		}); err != nil {
			t.Fatalf("seed tax rule: %v", err)
		}
	}

	for _, who := range []struct {
		username string
		role     service.Role
	}{
		{"bapak", service.RoleOwner},
		{"manajer", service.RoleManager},
		{"kasir", service.RoleStaff},
	} {
		u, err := auth.CreateUser(ctx, who.username, who.username, "rahasia-panjang")
		if err != nil {
			t.Fatalf("user %s: %v", who.username, err)
		}
		if err := auth.GrantRole(ctx, u.ID, entity.ID, who.role); err != nil {
			t.Fatalf("grant %s: %v", who.username, err)
		}
	}
	return c, entity.ID
}

func (c *client) login(username string) {
	c.t.Helper()
	got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": username, "password": "rahasia-panjang"}, nil)
	if got.status != stdhttp.StatusOK {
		c.t.Fatalf("login %s: %d %s", username, got.status, got.body)
	}
}

// TestOnlyOwnersAndManagersSeeOwnerMargin is D-009 enforced where it has to be.
//
// A cashier is not one of the family members products are attributed to, so
// "other owners' figures" means all of them — staff see no margin, not a
// redacted version. The refusal is the endpoint's, not the screen's: a margin
// figure that reaches the browser has already left the building.
func TestOnlyOwnersAndManagersSeeOwnerMargin(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)

	c.login("kasir")
	got := c.send(stdhttp.MethodGet, "/api/v1/reports/margin", nil, hdr(entityID))
	if got.status != stdhttp.StatusForbidden {
		t.Fatalf("staff GET margin: %d %s, want 403", got.status, got.body)
	}
	// And nothing leaks in the refusal itself.
	if strings.Contains(got.body, "revenue_idr") || strings.Contains(got.body, "owners") {
		t.Errorf("the refusal carries report data: %s", got.body)
	}

	c.login("manajer")
	if got := c.send(stdhttp.MethodGet, "/api/v1/reports/margin", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Fatalf("manager GET margin: %d %s, want 200", got.status, got.body)
	}

	c.login("bapak")
	if got := c.send(stdhttp.MethodGet, "/api/v1/reports/margin", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Fatalf("owner GET margin: %d %s, want 200", got.status, got.body)
	}
}

// Changing the returns-period rule changes what every past report says, and
// therefore what has already been settled (SPEC §4.4, R7.3). That is an
// owner's decision, not a manager's.
func TestOnlyAnOwnerSetsTheReturnsPeriodRule(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)

	c.login("manajer")
	got := c.send(stdhttp.MethodPut, "/api/v1/reports/margin/return-rule",
		map[string]any{"rule": "SALE_DATE"}, hdr(entityID))
	if got.status != stdhttp.StatusForbidden {
		t.Fatalf("manager PUT rule: %d %s, want 403", got.status, got.body)
	}

	c.login("bapak")
	got = c.send(stdhttp.MethodPut, "/api/v1/reports/margin/return-rule",
		map[string]any{"rule": "SALE_DATE", "note": "Disepakati keluarga"}, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("owner PUT rule: %d %s, want 200", got.status, got.body)
	}

	var rule struct {
		Rule   string `json:"rule"`
		Chosen bool   `json:"chosen"`
		Note   string `json:"note"`
	}
	if err := json.Unmarshal([]byte(got.body), &rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.Rule != "SALE_DATE" || !rule.Chosen || rule.Note != "Disepakati keluarga" {
		t.Errorf("rule = %+v", rule)
	}

	// A rule nobody recognises is refused rather than defaulted.
	if got := c.send(stdhttp.MethodPut, "/api/v1/reports/margin/return-rule",
		map[string]any{"rule": "SEMAUNYA"}, hdr(entityID)); got.status != stdhttp.StatusBadRequest {
		t.Errorf("unknown rule: %d %s, want 400", got.status, got.body)
	}
}

// The report reaches the browser labelled correctly and decomposed all the way
// to layer costs, in one request (SPEC §4.2, REQUIREMENTS §5).
func TestMarginReportOverHTTPIsLabelledAndDrillsDown(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")

	productID, supplierID := catalogue(t, c, entityID)

	// Two layers of the same goods at the same price, one with a faktur and
	// one without, so the drill-down has something to explain (INV-9).
	for _, faktur := range []bool{true, false} {
		date := "2026-10-01"
		if !faktur {
			date = "2026-10-05"
		}
		body := map[string]any{
			"supplier_id": supplierID, "purchase_date": date, "faktur_received": faktur,
			"lines": []map[string]any{
				{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
			},
		}
		if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", body, hdr(entityID)); got.status != stdhttp.StatusCreated {
			t.Fatalf("purchase: %d %s", got.status, got.body)
		}
	}

	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 0, "business_date": "2026-10-10"}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("open till: %d %s", got.status, got.body)
	}

	// Twelve boxes: all ten of the faktur layer, then two of the dearer one.
	sale := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-10-10",
		"lines":     []map[string]any{{"product_id": productID, "qty": 12, "unit_price_idr": 20_000}},
	}, hdr(entityID))
	if sale.status != stdhttp.StatusCreated {
		t.Fatalf("sale: %d %s", sale.status, sale.body)
	}

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/margin?from=2026-10-01&to=2026-10-31", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("report: %d %s", got.status, got.body)
	}
	report := decode[marginReportBody](t, got.body)

	// R2.4 and REQUIREMENTS §5: this is gross margin and it has not paid rent.
	// The label travels with the payload so no client can relabel it.
	if report.Title != "Laporan Margin per Owner" {
		t.Errorf("title = %q", report.Title)
	}
	if strings.Contains(strings.ToLower(got.body), "laba rugi") &&
		!strings.Contains(report.Caveat, "bukan Laba Rugi") {
		t.Error("the payload says Laba Rugi somewhere other than the caveat that forbids it")
	}
	for _, want := range []string{"listrik", "gaji", "sewa"} {
		if !strings.Contains(report.Note, want) {
			t.Errorf("the note does not mention %s being excluded", want)
		}
	}

	// The rule the figures were produced under travels with them, so no client
	// can show a settlement figure without being able to say which rule placed
	// the returns (SPEC §4.4, D-012).
	if report.ReturnRule != "RETURN_DATE" {
		t.Errorf("return rule = %q, want the RETURN_DATE default", report.ReturnRule)
	}

	if len(report.Owners) != 1 {
		t.Fatalf("got %d owner lines, want 1", len(report.Owners))
	}
	owner := report.Owners[0]
	// The customer paid Rp 240.000 at an inclusive 11%, which is Rp 216.216 of
	// revenue and Rp 23.784 of PPN (TASKS 5.3). Margin is on the revenue: the
	// PPN is owed to the state and was never anybody's to divide.
	if owner.RevenueIDR != 216_216 || owner.COGSIDR != 122_200 || owner.MarginIDR != 94_016 {
		t.Errorf("owner line = %d/%d/%d, want 216216/122200/94016",
			owner.RevenueIDR, owner.COGSIDR, owner.MarginIDR)
	}
	// And the report says so, rather than leaving somebody to wonder why the
	// figure is not the Rp 240.000 on the invoice.
	if owner.PPNIDR != 23_784 || owner.TenderedIDR != 240_000 {
		t.Errorf("PPN/tendered = %d/%d, want 23784/240000", owner.PPNIDR, owner.TenderedIDR)
	}

	// The drill-down, in the same response: owner -> sale -> product -> layers.
	if len(owner.Sales) != 1 || len(owner.Sales[0].Products) != 1 {
		t.Fatalf("the report does not decompose to sales and products")
	}
	layers := owner.Sales[0].Products[0].Layers
	if len(layers) != 2 {
		t.Fatalf("got %d layers, want the 2 the sale drew", len(layers))
	}
	if !layers[0].FakturReceived || layers[1].FakturReceived {
		t.Errorf("faktur flags = %v/%v, want true then false",
			layers[0].FakturReceived, layers[1].FakturReceived)
	}
	if layers[0].CostIDR != 100_000 || layers[1].CostIDR != 22_200 {
		t.Errorf("layer costs = %d/%d, want 100000/22200", layers[0].CostIDR, layers[1].CostIDR)
	}
	var layered int64
	for _, l := range layers {
		layered += l.CostIDR
	}
	if layered != owner.Sales[0].Products[0].COGSIDR || layered != owner.COGSIDR {
		t.Errorf("layers total %d against a product of %d and an owner of %d",
			layered, owner.Sales[0].Products[0].COGSIDR, owner.COGSIDR)
	}
}

// D-012 over the wire. October keeps its settled figures and still says what
// came back in November, in the same response, decomposed like everything else.
func TestOctobersReportSaysWhatCameBackLater(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID, supplierID := catalogue(t, c, entityID)

	if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01", "faktur_received": true,
		"lines": []map[string]any{
			{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
		},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 0, "business_date": "2026-10-10"}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("open till: %d %s", got.status, got.body)
	}

	sale := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-10-10",
		"lines":     []map[string]any{{"product_id": productID, "qty": 10, "unit_price_idr": 20_000}},
	}, hdr(entityID))
	if sale.status != stdhttp.StatusCreated {
		t.Fatalf("sale: %d %s", sale.status, sale.body)
	}
	rung := decode[struct {
		Sale  struct{ ID string } `json:"sale"`
		Lines []struct {
			ID string `json:"id"`
		} `json:"lines"`
	}](t, sale.body)

	// Two boxes come back in November, against an October sale.
	if got := c.send(stdhttp.MethodPost, "/api/v1/sales/"+rung.Sale.ID+"/returns", map[string]any{
		"return_date": "2026-11-03", "reason": "Ukuran salah",
		"lines": []map[string]any{{"sale_line_id": rung.Lines[0].ID, "qty": 2}},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("return: %d %s", got.status, got.body)
	}

	october := decode[marginReportBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/reports/margin?from=2026-10-01&to=2026-10-31", nil, hdr(entityID)).body)

	owner := october.Owners[0]
	// October is untouched — that is the point of the rule.
	// Rp 200.000 taken at an inclusive 11% is Rp 180.180 of revenue.
	if owner.RevenueIDR != 180_180 || owner.COGSIDR != 100_000 || owner.MarginIDR != 80_180 {
		t.Errorf("October = %d/%d/%d, want 180180/100000/80180 — a settled month moved",
			owner.RevenueIDR, owner.COGSIDR, owner.MarginIDR)
	}
	if len(owner.Returns) != 0 {
		t.Errorf("October counted %d returns under RETURN_DATE", len(owner.Returns))
	}
	// And it still says what came back.
	if len(owner.LaterReturns) != 1 {
		t.Fatalf("October lists %d later returns, want 1", len(owner.LaterReturns))
	}
	later := owner.LaterReturns[0]
	if later.SaleBusinessDate != "2026-10-10" || later.EffectiveDate != "2026-11-03" || later.RefundIDR != 40_000 {
		t.Errorf("later return = %+v", later)
	}

	// November is where it actually counts.
	november := decode[marginReportBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/reports/margin?from=2026-11-01&to=2026-11-30", nil, hdr(entityID)).body)
	if len(november.Owners) != 1 || len(november.Owners[0].Returns) != 1 {
		t.Fatalf("November does not carry the return")
	}
	if !november.Owners[0].Returns[0].CrossesPeriod {
		t.Error("the November return is not flagged as crossing the period")
	}
	// The customer got Rp 40.000 back, Rp 3.964 of it PPN, so Rp 36.036 of
	// revenue reversed against Rp 20.000 of cost put back.
	if november.Totals.MarginIDR != -16_036 {
		t.Errorf("November margin = %d, want -16036", november.Totals.MarginIDR)
	}
}

// R12.3 over the wire, and the reason these errors carry Indonesian text at
// all: the cashier has a customer in front of them and needs to be told what to
// do instead, not "kesalahan internal".
func TestTillAndVoidStateErrorsReachTheCashier(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID, supplierID := catalogue(t, c, entityID)

	if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01", "faktur_received": true,
		"lines": []map[string]any{
			{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
		},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", got.status, got.body)
	}

	// No till open yet: a refusal the cashier can act on, not a 500.
	noTill := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-10-10",
		"lines":     []map[string]any{{"product_id": productID, "qty": 1, "unit_price_idr": 20_000}},
	}, hdr(entityID))
	if noTill.status != stdhttp.StatusConflict {
		t.Errorf("sale with no till = %d %s, want 409", noTill.status, noTill.body)
	}
	if !strings.Contains(noTill.body, "sesi kas") {
		t.Errorf("the refusal does not mention the till: %s", noTill.body)
	}

	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 0, "business_date": "2026-10-10"}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("open till: %d %s", got.status, got.body)
	}

	sale := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-10-10",
		"lines":     []map[string]any{{"product_id": productID, "qty": 4, "unit_price_idr": 20_000}},
	}, hdr(entityID))
	if sale.status != stdhttp.StatusCreated {
		t.Fatalf("sale: %d %s", sale.status, sale.body)
	}
	rung := decode[struct {
		Sale  struct{ ID string } `json:"sale"`
		Lines []struct {
			ID string `json:"id"`
		} `json:"lines"`
	}](t, sale.body)

	if got := c.send(stdhttp.MethodPost, "/api/v1/sales/"+rung.Sale.ID+"/returns", map[string]any{
		"return_date": "2026-10-10", "reason": "Kemasan rusak",
		"lines": []map[string]any{{"sale_line_id": rung.Lines[0].ID, "qty": 1}},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("return: %d %s", got.status, got.body)
	}

	// The till is still open, so only the return itself stands in the way.
	voided := c.send(stdhttp.MethodPost, "/api/v1/sales/"+rung.Sale.ID+"/void",
		map[string]any{"reason": "salah input"}, hdr(entityID))
	if voided.status != stdhttp.StatusConflict {
		t.Fatalf("void after a return = %d %s, want 409", voided.status, voided.body)
	}
	if !strings.Contains(voided.body, "retur") {
		t.Errorf("the refusal does not point at a return: %s", voided.body)
	}
	if strings.Contains(voided.body, "kesalahan internal") {
		t.Error("the cashier is shown a generic server error instead of what to do")
	}
}
