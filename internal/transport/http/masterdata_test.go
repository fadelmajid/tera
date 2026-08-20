package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store/gen"
)

func hdr(entityID string) map[string]string {
	return map[string]string{"X-Entity-Id": entityID}
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out
}

type idBody struct {
	ID      string  `json:"id"`
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	OwnerID *string `json:"owner_id"`
	Price   int64   `json:"sale_price_idr"`
}

// The Phase 0 exit criterion, end to end over HTTP: log in, set the business
// up, and create a product attributed to a family member.
func TestPhaseZeroExitCriterion(t *testing.T) {
	t.Parallel()

	c, auth, q, ctx := newClient(t)

	if _, err := auth.CreateUser(ctx, "budi", "Budi Santoso", "rahasia-panjang"); err != nil {
		t.Fatalf("user: %v", err)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "budi", "password": "rahasia-panjang"}, nil); got.status != stdhttp.StatusOK {
		t.Fatalf("login: %d %s", got.status, got.body)
	}

	// No company exists yet, so the account holds no role anywhere and every
	// entity-scoped route must refuse it.
	if got := c.send(stdhttp.MethodPost, "/api/v1/owners",
		map[string]any{"code": "BUDI", "name": "Budi"}, hdr("tidak-ada")); got.status != stdhttp.StatusForbidden {
		t.Fatalf("expected 403 before any company exists, got %d %s", got.status, got.body)
	}

	// Setup creates the first company and makes the caller its owner.
	setup := c.send(stdhttp.MethodPost, "/api/v1/setup/entity", map[string]any{
		"code": "PKP", "name": "PT Sehat Sentosa", "is_pkp": true,
		"timezone": "Asia/Jakarta", "book_year_start_month": 1,
	}, nil)
	if setup.status != stdhttp.StatusCreated {
		t.Fatalf("setup: %d %s", setup.status, setup.body)
	}
	entity := decode[idBody](t, setup.body)

	// It closes behind itself.
	again := c.send(stdhttp.MethodPost, "/api/v1/setup/entity", map[string]any{
		"code": "X", "name": "X", "is_pkp": false,
	}, nil)
	if again.status != stdhttp.StatusConflict {
		t.Errorf("second setup: %d, want 409", again.status)
	}

	// An owner — a family member products are attributed to.
	ownerResp := c.send(stdhttp.MethodPost, "/api/v1/owners",
		map[string]any{"code": "BUDI", "name": "Budi"}, hdr(entity.ID))
	if ownerResp.status != stdhttp.StatusCreated {
		t.Fatalf("create owner: %d %s", ownerResp.status, ownerResp.body)
	}
	owner := decode[idBody](t, ownerResp.body)

	// A product attributed to that owner.
	productResp := c.send(stdhttp.MethodPost, "/api/v1/products", map[string]any{
		"code": "MSK-01", "name": "Masker Bedah 3 Ply", "unit": "box",
		"owner_id": owner.ID, "sale_price_idr": 27500,
	}, hdr(entity.ID))
	if productResp.status != stdhttp.StatusCreated {
		t.Fatalf("create product: %d %s", productResp.status, productResp.body)
	}

	product := decode[idBody](t, productResp.body)
	if product.OwnerID == nil || *product.OwnerID != owner.ID {
		t.Fatalf("product owner = %v, want %s", product.OwnerID, owner.ID)
	}
	if product.Price != 27_500 {
		t.Errorf("price = %d, want 27500", product.Price)
	}

	// INV-10: every mutation left a trail naming who made it.
	rows, err := q.ListAuditForRecord(ctx, gen.ListAuditForRecordParams{
		RecordType: "product", RecordID: product.ID,
	})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows for the product = %d, want 1", len(rows))
	}
	if rows[0].ActorUserID == nil {
		t.Error("the audit row names no actor")
	}
	if rows[0].Action != "CREATE" || rows[0].AfterJson == nil {
		t.Errorf("audit row = %s with after=%v", rows[0].Action, rows[0].AfterJson)
	}
	if rows[0].ClientRequestID == nil {
		t.Error("the audit row is not tied back to the request that made it")
	}
}

// R2.2: a product with no owner is the company bucket, and null must survive
// the wire so the margin report can show it as its own line.
func TestCompanyBucketProductRoundTrips(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)

	got := c.send(stdhttp.MethodPost, "/api/v1/products", map[string]any{
		"code": "ALK-70", "name": "Alkohol 70%", "unit": "botol", "sale_price_idr": 18000,
	}, hdr(entityID))
	if got.status != stdhttp.StatusCreated {
		t.Fatalf("create: %d %s", got.status, got.body)
	}

	product := decode[idBody](t, got.body)
	if product.OwnerID != nil {
		t.Errorf("owner_id = %v, want null for the company bucket", *product.OwnerID)
	}
	if !strings.Contains(got.body, `"owner_id":null`) {
		t.Errorf("owner_id was omitted rather than explicitly null: %s", got.body)
	}
}

// INV-1 at the edge of the system: a fractional rupiah is refused, not rounded.
func TestFractionalPriceIsRefused(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)

	for _, body := range []string{
		`{"code":"A1","name":"A","unit":"pcs","sale_price_idr":27500.5}`,
		`{"code":"A2","name":"A","unit":"pcs","sale_price_idr":2.75e4}`,
	} {
		got := c.sendRaw(stdhttp.MethodPost, "/api/v1/products", body, hdr(entityID))
		if got.status != stdhttp.StatusBadRequest {
			t.Errorf("body %s got %d, want 400 (%s)", body, got.status, got.body)
		}
	}
}

// R13.3 / D-009 and R7.1 in one: staff may read the catalogue they sell from,
// and may not change it.
func TestStaffCanReadMasterDataButNotChangeIt(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)

	// A second user, staff in the same company.
	_, auth, _, ctx := c.deps()
	kasir, err := auth.CreateUser(ctx, "kasir", "Kasir", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := auth.GrantRole(ctx, kasir.ID, entityID, service.RoleStaff); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "kasir", "password": "rahasia-panjang"}, nil); got.status != stdhttp.StatusOK {
		t.Fatalf("login: %d %s", got.status, got.body)
	}

	if got := c.send(stdhttp.MethodGet, "/api/v1/products", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Errorf("staff cannot read the catalogue: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/products", map[string]any{
		"code": "X", "name": "X", "unit": "pcs", "sale_price_idr": 1000,
	}, hdr(entityID)); got.status != stdhttp.StatusForbidden {
		t.Errorf("staff changed master data: %d %s", got.status, got.body)
	}
}

func TestDuplicateCodeIsRejected(t *testing.T) {
	t.Parallel()

	c, entityID := setupEntity(t)

	body := map[string]any{"code": "DUP", "name": "Satu", "unit": "pcs", "sale_price_idr": 1000}
	if got := c.send(stdhttp.MethodPost, "/api/v1/products", body, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("first: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/products", body, hdr(entityID)); got.status != stdhttp.StatusConflict {
		t.Errorf("duplicate code: %d, want 409 (%s)", got.status, got.body)
	}
}
