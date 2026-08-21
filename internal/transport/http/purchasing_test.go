package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"testing"

	"github.com/fadelmajid/tera/internal/service"
)

type purchaseResponse struct {
	Purchase struct {
		ID             string `json:"id"`
		FakturReceived bool   `json:"faktur_received"`
		SubtotalIDR    int64  `json:"subtotal_idr"`
		PPNIDR         int64  `json:"ppn_idr"`
		TotalIDR       int64  `json:"total_idr"`
	} `json:"purchase"`
	Lines []struct {
		ID               string `json:"id"`
		CostTotalIDR     int64  `json:"cost_total_idr"`
		CreditablePPNIDR int64  `json:"creditable_ppn_idr"`
	} `json:"lines"`
	Layers []struct {
		ID             string `json:"id"`
		CostTotalIDR   int64  `json:"cost_total_idr"`
		FakturReceived bool   `json:"faktur_received"`
	} `json:"layers"`
}

// catalogue gets a company to the point where it can buy something: one owner,
// one product attributed to them, one supplier.
func catalogue(t *testing.T, c *client, entityID string) (productID, supplierID string) {
	t.Helper()

	create := func(path string, body map[string]any) string {
		t.Helper()
		got := c.send(stdhttp.MethodPost, path, body, hdr(entityID))
		if got.status != stdhttp.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, got.status, got.body)
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(got.body), &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out.ID
	}

	ownerID := create("/api/v1/owners", map[string]any{"code": "BUDI", "name": "Budi"})
	productID = create("/api/v1/products", map[string]any{
		"code": "P-GLOVE", "name": "Sarung Tangan", "unit": "box",
		"owner_id": ownerID, "sale_price_idr": 150_000,
	})
	supplierID = create("/api/v1/suppliers", map[string]any{
		"code": "S1", "name": "PT Medika Jaya", "issues_faktur": true,
	})
	return productID, supplierID
}

// The thesis over HTTP: two identical invoices, one with a faktur and one
// without, come back with different layer costs (TASKS 1.7, SPEC §3.2).
func TestPurchaseOverHTTPCarriesTheFakturCostBasis(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)
	productID, supplierID := catalogue(t, c, entityID)

	buy := func(faktur bool) purchaseResponse {
		t.Helper()
		got := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
			"supplier_id": supplierID, "purchase_date": "2026-10-01",
			"faktur_received": faktur,
			"lines": []map[string]any{
				{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
			},
		}, hdr(entityID))
		if got.status != stdhttp.StatusCreated {
			t.Fatalf("purchase: %d %s", got.status, got.body)
		}
		return decode[purchaseResponse](t, got.body)
	}

	withFaktur, without := buy(true), buy(false)

	if withFaktur.Layers[0].CostTotalIDR != 100_000 {
		t.Errorf("with faktur: layer cost %d, want 100000", withFaktur.Layers[0].CostTotalIDR)
	}
	if without.Layers[0].CostTotalIDR != 111_000 {
		t.Errorf("without faktur: layer cost %d, want 111000", without.Layers[0].CostTotalIDR)
	}
	if withFaktur.Layers[0].CostTotalIDR == without.Layers[0].CostTotalIDR {
		t.Fatal("the faktur made no difference to the cost that reached the client")
	}
	if withFaktur.Lines[0].CreditablePPNIDR != 11_000 || without.Lines[0].CreditablePPNIDR != 0 {
		t.Errorf("creditable PPN = %d and %d, want 11000 and 0",
			withFaktur.Lines[0].CreditablePPNIDR, without.Lines[0].CreditablePPNIDR)
	}

	// The PPN position surface carries the compliance disclaimer. We are not
	// tax advisors (CLAUDE.md).
	pos := c.send(stdhttp.MethodGet, "/api/v1/ppn/input?from=2026-10-01&to=2026-10-31", nil, hdr(entityID))
	if pos.status != stdhttp.StatusOK {
		t.Fatalf("ppn position: %d %s", pos.status, pos.body)
	}
	var position struct {
		CreditableIDR    int64  `json:"creditable_idr"`
		ComplianceNotice string `json:"compliance_notice"`
	}
	if err := json.Unmarshal([]byte(pos.body), &position); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if position.CreditableIDR != 11_000 {
		t.Errorf("creditable input PPN = %d, want 11000", position.CreditableIDR)
	}
	if position.ComplianceNotice == "" {
		t.Error("a tax figure was served without the compliance disclaimer")
	}
}

// R10.4: purchases are entered by admin or manager, not cashier staff. These
// screens set the faktur status that decides a layer's cost basis and post
// stock adjustments -- margin-bearing, not data entry.
func TestStaffCannotEnterPurchasesOrAdjustStock(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)
	productID, supplierID := catalogue(t, c, entityID)

	// A cashier account, staff in this company.
	if _, err := c.auth.CreateUser(c.ctx, "kasir", "Kasir", "rahasia-panjang"); err != nil {
		t.Fatalf("user: %v", err)
	}
	users, err := c.q.ListUsers(c.ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var staffID string
	for _, u := range users {
		if u.Username == "kasir" {
			staffID = u.ID
		}
	}
	if err := c.auth.GrantRole(c.ctx, staffID, entityID, service.RoleStaff); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "kasir", "password": "rahasia-panjang"}, nil); got.status != stdhttp.StatusOK {
		t.Fatalf("login: %d %s", got.status, got.body)
	}

	refused := []struct {
		name, method, path string
		body               any
	}{
		{"record a purchase", stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
			"supplier_id": supplierID, "faktur_received": true,
			"lines": []map[string]any{{"product_id": productID, "qty": 1, "unit_price_idr": 1_000}},
		}},
		{"list purchases", stdhttp.MethodGet, "/api/v1/purchases", nil},
		{"start a stock count", stdhttp.MethodPost, "/api/v1/opname", map[string]any{}},
		{"carry in opening stock", stdhttp.MethodPost, "/api/v1/opening/stock", map[string]any{
			"lines": []map[string]any{{"product_id": productID, "qty": 1, "cost_total_idr": 1_000}},
		}},
		{"see hutang", stdhttp.MethodGet, "/api/v1/payables", nil},
		{"see the PPN position", stdhttp.MethodGet, "/api/v1/ppn/input?from=2026-10-01&to=2026-10-31", nil},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			got := c.send(tc.method, tc.path, tc.body, hdr(entityID))
			if got.status != stdhttp.StatusForbidden {
				t.Errorf("staff could %s: %d %s", tc.name, got.status, got.body)
			}
		})
	}

	// A cashier still sees the catalogue -- they have to sell from it.
	if got := c.send(stdhttp.MethodGet, "/api/v1/products", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Errorf("staff cannot see products: %d %s", got.status, got.body)
	}
}

// INV-6 over the wire: a stuttering shop WiFi must not ring the same purchase
// twice. Two identical layers would be stock the business never bought.
func TestPurchaseIsIdempotentOnClientRequestID(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)
	productID, supplierID := catalogue(t, c, entityID)

	body := map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01", "faktur_received": true,
		"lines": []map[string]any{
			{"product_id": productID, "qty": 10, "unit_price_idr": 10_000, "ppn_idr": 11_000},
		},
	}
	headers := hdr(entityID)
	headers["X-Client-Request-Id"] = "11111111-1111-7111-8111-111111111111"

	first := c.send(stdhttp.MethodPost, "/api/v1/purchases", body, headers)
	if first.status != stdhttp.StatusCreated {
		t.Fatalf("first: %d %s", first.status, first.body)
	}
	second := c.send(stdhttp.MethodPost, "/api/v1/purchases", body, headers)
	if second.status != stdhttp.StatusCreated {
		t.Fatalf("retry: %d %s", second.status, second.body)
	}

	a := decode[purchaseResponse](t, first.body)
	b := decode[purchaseResponse](t, second.body)
	if a.Purchase.ID != b.Purchase.ID {
		t.Errorf("the retry created a second purchase: %s then %s", a.Purchase.ID, b.Purchase.ID)
	}

	list := c.send(stdhttp.MethodGet, "/api/v1/purchases", nil, hdr(entityID))
	rows := decode[[]struct {
		ID string `json:"id"`
	}](t, list.body)
	if len(rows) != 1 {
		t.Errorf("got %d purchases after one retried request, want 1", len(rows))
	}
}

// A full opname over HTTP: count, see the variance, post it (R12.4-5).
func TestOpnameOverHTTP(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)
	productID, supplierID := catalogue(t, c, entityID)

	buy := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01",
		"lines": []map[string]any{{"product_id": productID, "qty": 10, "unit_price_idr": 10_000}},
	}, hdr(entityID))
	if buy.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", buy.status, buy.body)
	}

	sheet := c.send(stdhttp.MethodGet, "/api/v1/opname/count-sheet", nil, hdr(entityID))
	if sheet.status != stdhttp.StatusOK {
		t.Fatalf("count sheet: %d %s", sheet.status, sheet.body)
	}
	onHand := decode[[]struct {
		ProductID string  `json:"product_id"`
		OwnerID   *string `json:"owner_id"`
		QtyOnHand int64   `json:"qty_on_hand"`
	}](t, sheet.body)
	if len(onHand) != 1 || onHand[0].QtyOnHand != 10 {
		t.Fatalf("count sheet = %+v", onHand)
	}

	start := c.send(stdhttp.MethodPost, "/api/v1/opname",
		map[string]any{"count_date": "2026-10-15"}, hdr(entityID))
	if start.status != stdhttp.StatusCreated {
		t.Fatalf("start: %d %s", start.status, start.body)
	}
	opname := decode[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}](t, start.body)

	// An unexplained variance is refused (R12.5).
	bad := c.send(stdhttp.MethodPost, "/api/v1/opname/"+opname.ID+"/lines", map[string]any{
		"product_id": productID, "owner_id": derefOrEmpty(onHand[0].OwnerID), "counted_qty": 8,
	}, hdr(entityID))
	if bad.status != stdhttp.StatusBadRequest {
		t.Errorf("accepted an unexplained variance: %d %s", bad.status, bad.body)
	}

	line := c.send(stdhttp.MethodPost, "/api/v1/opname/"+opname.ID+"/lines", map[string]any{
		"product_id": productID, "owner_id": derefOrEmpty(onHand[0].OwnerID),
		"counted_qty": 8, "reason_code": "RUSAK", "reason_note": "Kemasan sobek",
	}, hdr(entityID))
	if line.status != stdhttp.StatusOK {
		t.Fatalf("save line: %d %s", line.status, line.body)
	}
	saved := decode[struct {
		SystemQty  int64 `json:"system_qty"`
		CountedQty int64 `json:"counted_qty"`
		Variance   int64 `json:"variance"`
	}](t, line.body)
	if saved.SystemQty != 10 || saved.Variance != -2 {
		t.Errorf("variance = %+v, want system 10 and variance -2", saved)
	}

	post := c.send(stdhttp.MethodPost, "/api/v1/opname/"+opname.ID+"/post",
		map[string]any{"reason": "Opname bulanan Oktober"}, hdr(entityID))
	if post.status != stdhttp.StatusOK {
		t.Fatalf("post: %d %s", post.status, post.body)
	}
	result := decode[struct {
		Opname struct {
			Status string `json:"status"`
		} `json:"opname"`
		ShortUnits int64 `json:"short_units"`
	}](t, post.body)
	if result.Opname.Status != "POSTED" || result.ShortUnits != 2 {
		t.Errorf("post result = %+v", result)
	}

	// Stock actually moved.
	after := c.send(stdhttp.MethodGet, "/api/v1/opname/count-sheet", nil, hdr(entityID))
	remaining := decode[[]struct {
		QtyOnHand int64 `json:"qty_on_hand"`
	}](t, after.body)
	if len(remaining) != 1 || remaining[0].QtyOnHand != 8 {
		t.Errorf("on hand after posting = %+v, want 8", remaining)
	}
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
