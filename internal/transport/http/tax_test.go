package http_test

import (
	stdhttp "net/http"
	"strings"
	"testing"
)

type ppnPositionBody struct {
	Title  string `json:"title"`
	Masa   string `json:"masa"`
	Caveat string `json:"caveat"`
	Period struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"period"`
	Output struct {
		WithFakturIDR    int64 `json:"with_faktur_idr"`
		WithoutFakturIDR int64 `json:"without_faktur_idr"`
		ReversedIDR      int64 `json:"reversed_idr"`
		NetIDR           int64 `json:"net_idr"`
	} `json:"output"`
	Input struct {
		CreditableIDR    int64 `json:"creditable_idr"`
		ReversedIDR      int64 `json:"reversed_idr"`
		NonCreditableIDR int64 `json:"non_creditable_idr"`
		NetIDR           int64 `json:"net_idr"`
	} `json:"input"`
	PayableIDR int64 `json:"payable_idr"`
	IsOverpaid bool  `json:"is_overpaid"`
	Sales      []struct {
		InvoiceNo    string `json:"invoice_no"`
		FakturIssued bool   `json:"faktur_issued"`
		DPPIDR       int64  `json:"dpp_idr"`
		PPNIDR       int64  `json:"ppn_idr"`
	} `json:"sales"`
	Purchases []struct {
		FakturReceived   bool  `json:"faktur_received"`
		PPNIDR           int64 `json:"ppn_idr"`
		CreditablePPNIDR int64 `json:"creditable_ppn_idr"`
	} `json:"purchases"`
}

type taxRulesBody struct {
	Today  string `json:"today"`
	Caveat string `json:"caveat"`
	Rules  []struct {
		ID              string  `json:"id"`
		TaxType         string  `json:"tax_type"`
		RateBP          int64   `json:"rate_bp"`
		DPPNum          int64   `json:"dpp_factor_num"`
		DPPDen          int64   `json:"dpp_factor_den"`
		EffectiveRateBP int64   `json:"effective_rate_bp"`
		Inclusive       bool    `json:"is_inclusive"`
		ValidFrom       string  `json:"valid_from"`
		ValidTo         *string `json:"valid_to"`
		InForce         bool    `json:"in_force"`
		Staged          bool    `json:"staged"`
		LegalRef        string  `json:"legal_ref"`
	} `json:"rules"`
}

// trade rings one purchase with a faktur, one without, and one sale, so the
// PPN position has something on both sides.
func trade(t *testing.T, c *client, entityID string) {
	t.Helper()

	productID, supplierID := catalogue(t, c, entityID)

	for _, faktur := range []bool{true, false} {
		body := map[string]any{
			"supplier_id": supplierID, "purchase_date": "2026-10-01", "faktur_received": faktur,
			"lines": []map[string]any{
				{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
			},
		}
		if faktur {
			body["faktur_no"] = "010.000-26.00000001"
		}
		if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", body, hdr(entityID)); got.status != stdhttp.StatusCreated {
			t.Fatalf("purchase faktur=%v: %d %s", faktur, got.status, got.body)
		}
	}

	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 0, "business_date": "2026-10-10"}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("open till: %d %s", got.status, got.body)
	}
	// A walk-in: no faktur issued, and PPN owed all the same (SPEC §2.3).
	if got := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-10-10",
		"lines":     []map[string]any{{"product_id": productID, "qty": 2, "unit_price_idr": 111_000}},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("sale: %d %s", got.status, got.body)
	}
}

// TestThePPNPositionOverHTTP is SPEC §2.4 and TASKS 5.9 on the wire.
func TestThePPNPositionOverHTTP(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID)

	got := c.send(stdhttp.MethodGet, "/api/v1/reports/ppn?masa=2026-10", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("position: %d %s", got.status, got.body)
	}
	body := decode[ppnPositionBody](t, got.body)

	if body.Masa != "2026-10" || body.Period.From != "2026-10-01" || body.Period.To != "2026-10-31" {
		t.Errorf("masa %q covers %s..%s", body.Masa, body.Period.From, body.Period.To)
	}

	// Rp 222.000 taken inclusive of an effective 11% is Rp 200.000 + Rp 22.000,
	// and the buyer took no faktur.
	if body.Output.WithoutFakturIDR != 22_000 || body.Output.WithFakturIDR != 0 {
		t.Errorf("output split = %d with a faktur, %d without, want 0 and 22000",
			body.Output.WithFakturIDR, body.Output.WithoutFakturIDR)
	}
	// TASKS 5.4 on the screen: the liability exists and is on its own line.
	if body.Output.NetIDR != 22_000 {
		t.Errorf("output PPN = %d, want 22000", body.Output.NetIDR)
	}

	// Only the purchase with a faktur is creditable (INV-9). The other one's
	// PPN is reported, and never netted: it is already in the cost of the goods.
	if body.Input.CreditableIDR != 11_000 || body.Input.NonCreditableIDR != 11_000 {
		t.Errorf("input = %d creditable, %d not, want 11000 each",
			body.Input.CreditableIDR, body.Input.NonCreditableIDR)
	}
	if body.PayableIDR != 11_000 || body.IsOverpaid {
		t.Errorf("payable = %d (overpaid=%v), want 11000", body.PayableIDR, body.IsOverpaid)
	}

	// Every figure decomposes into the documents behind it — this report goes
	// to a konsultan pajak, and a number they cannot check is a number they
	// will not sign off.
	if len(body.Sales) != 1 || body.Sales[0].PPNIDR != 22_000 || body.Sales[0].FakturIssued {
		t.Errorf("the sales behind the output figure: %+v", body.Sales)
	}
	if len(body.Purchases) != 2 {
		t.Fatalf("the position lists %d purchases, want both", len(body.Purchases))
	}
	for _, p := range body.Purchases {
		want := int64(0)
		if p.FakturReceived {
			want = 11_000
		}
		if p.CreditablePPNIDR != want {
			t.Errorf("purchase with faktur=%v credits %d, want %d",
				p.FakturReceived, p.CreditablePPNIDR, want)
		}
	}

	// The compliance caveat travels with the payload, so no client can show a
	// tax figure without it (CLAUDE.md).
	if !strings.Contains(body.Caveat, "konsultan pajak") {
		t.Errorf("the payload carries no compliance caveat: %q", body.Caveat)
	}
}

// TestTheTaxRuleScreenShowsWhatIsInForceAndWhy is TASKS 5.10.
func TestTheTaxRuleScreenShowsWhatIsInForceAndWhy(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")

	got := c.send(stdhttp.MethodGet, "/api/v1/tax/rules", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("rules: %d %s", got.status, got.body)
	}
	body := decode[taxRulesBody](t, got.body)

	if len(body.Rules) != 1 {
		t.Fatalf("got %d rules, want the one seeded", len(body.Rules))
	}
	r := body.Rules[0]

	// The fraction crosses the wire as the two integers the rule holds, never
	// as a decimal: 11/12 has no finite decimal form (SPEC §2.1, INV-1).
	if r.RateBP != 1200 || r.DPPNum != 11 || r.DPPDen != 12 {
		t.Errorf("rule = %d bp on %d/%d, want 1200 on 11/12", r.RateBP, r.DPPNum, r.DPPDen)
	}
	if r.EffectiveRateBP != 1100 {
		t.Errorf("effective rate = %d bp, want 1100", r.EffectiveRateBP)
	}
	if strings.Contains(got.body, "0.9166") || strings.Contains(got.body, "0.91") {
		t.Errorf("the payload carries a decimal approximation of 11/12: %s", got.body)
	}
	// The citation is the whole point of the screen: the owner's konsultan
	// pajak checks the configuration against the regulation, not against code.
	if r.LegalRef == "" {
		t.Error("the rule reaches the screen with no legal reference")
	}
	if !r.InForce || r.Staged {
		t.Errorf("the seeded rule is in_force=%v staged=%v", r.InForce, r.Staged)
	}
	if !strings.Contains(body.Caveat, "konsultan pajak") {
		t.Errorf("the payload carries no compliance caveat: %q", body.Caveat)
	}
}

// TestARateIsChangedByClosingOneRowAndOpeningAnother is INV-4 over the wire.
func TestARateIsChangedByClosingOneRowAndOpeningAnother(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")

	rules := decode[taxRulesBody](t, c.send(stdhttp.MethodGet, "/api/v1/tax/rules", nil, hdr(entityID)).body)
	seeded := rules.Rules[0].ID

	// Opening a second rule while the first is still open is refused: two rates
	// claiming the same day is a rate change that applies to some sales and not
	// others.
	newRule := map[string]any{
		"tax_type": "PPN", "rate_bp": 1200, "dpp_factor_num": 1, "dpp_factor_den": 1,
		"is_inclusive": true, "calculation_level": "INVOICE",
		"rounding_mode": "HALF_UP", "rounding_unit": 1,
		"valid_from": "2027-01-01", "legal_ref": "UU 7/2021 (HPP) Pasal 7",
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/tax/rules", newRule, hdr(entityID)); got.status != stdhttp.StatusBadRequest {
		t.Fatalf("overlapping rule: %d %s, want 400", got.status, got.body)
	}

	// Close the old one, then open the new one from the following day.
	if got := c.send(stdhttp.MethodPost, "/api/v1/tax/rules/"+seeded+"/close",
		map[string]any{"valid_to": "2026-12-31", "reason": "tarif berubah"}, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Fatalf("close: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/tax/rules", newRule, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("create: %d %s", got.status, got.body)
	}

	after := decode[taxRulesBody](t, c.send(stdhttp.MethodGet, "/api/v1/tax/rules", nil, hdr(entityID)).body)
	if len(after.Rules) != 2 {
		t.Fatalf("got %d rules, want both the old and the new", len(after.Rules))
	}
	// The superseded rule is still there. It is the rate past sales were priced
	// under, and the report explaining them reads its citation.
	var closed, staged int
	for _, r := range after.Rules {
		if r.ValidTo != nil {
			closed++
		}
		if r.Staged {
			staged++
		}
	}
	if closed != 1 || staged != 1 {
		t.Errorf("%d closed and %d staged rules, want one of each", closed, staged)
	}
}

// TestOnlyTheOwnerChangesTaxRules is the tightest gate in the system after user
// management.
//
// A tax rule decides what every subsequent sale charges, and it is the
// configuration a konsultan pajak is shown. A manager runs the shop; the person
// who answers for the rate is the person who sets it.
func TestOnlyTheOwnerChangesTaxRules(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)

	newRule := map[string]any{
		"tax_type": "PPN", "rate_bp": 1200, "dpp_factor_num": 1, "dpp_factor_den": 1,
		"is_inclusive": true, "calculation_level": "INVOICE",
		"rounding_mode": "HALF_UP", "rounding_unit": 1,
		"valid_from": "2027-01-01", "legal_ref": "UU 7/2021 (HPP) Pasal 7",
	}

	c.login("manajer")
	if got := c.send(stdhttp.MethodPost, "/api/v1/tax/rules", newRule, hdr(entityID)); got.status != stdhttp.StatusForbidden {
		t.Errorf("manager POST tax rule: %d %s, want 403", got.status, got.body)
	}
	// A manager may still read the configuration and the position: they run the
	// shop and need to know what it is charging.
	if got := c.send(stdhttp.MethodGet, "/api/v1/tax/rules", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Errorf("manager GET tax rules: %d %s, want 200", got.status, got.body)
	}

	c.login("kasir")
	for _, path := range []string{"/api/v1/tax/rules", "/api/v1/reports/ppn"} {
		got := c.send(stdhttp.MethodGet, path, nil, hdr(entityID))
		if got.status != stdhttp.StatusForbidden {
			t.Errorf("staff GET %s: %d %s, want 403", path, got.status, got.body)
		}
		if strings.Contains(got.body, "rate_bp") || strings.Contains(got.body, "payable_idr") {
			t.Errorf("the refusal carries tax data: %s", got.body)
		}
	}
}
