package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// twoCompanies is the user's real setup over HTTP: one PKP entity, one not,
// sharing a catalogue, with an owner who is a manager in both.
func twoCompanies(t *testing.T) (c *client, pkp, nonPKP, productID, supplierID string) {
	t.Helper()

	c, auth, q, ctx := newClient(t)

	entity := func(code, name string, isPKP int64) string {
		t.Helper()
		e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
			ID: store.NewID(), Code: code, Name: name, IsPkp: isPKP,
			Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
		})
		if err != nil {
			t.Fatalf("entity %s: %v", code, err)
		}
		return e.ID
	}
	pkp = entity("PKP", "PT Sehat Sentosa", 1)
	nonPKP = entity("NONPKP", "PT Medika Jaya", 0)

	u, err := auth.CreateUser(ctx, "bapak", "Bapak", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	for _, e := range []string{pkp, nonPKP} {
		if err := auth.GrantRole(ctx, u.ID, e, service.RoleOwner); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	c.login("bapak")

	create := func(path string, body map[string]any, entityID string) string {
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

	ownerID := create("/api/v1/owners", map[string]any{"code": "BUDI", "name": "Budi"}, pkp)
	productID = create("/api/v1/products", map[string]any{
		"code": "P-GLOVE", "name": "Sarung Tangan", "unit": "box",
		"owner_id": ownerID, "sale_price_idr": 20_000,
	}, pkp)
	supplierID = create("/api/v1/suppliers", map[string]any{
		"code": "S1", "name": "PT Medika Farma", "issues_faktur": true,
	}, pkp)
	return c, pkp, nonPKP, productID, supplierID
}

func (c *client) buyInto(t *testing.T, entityID, supplierID, productID string, qty, unit, ppn int64, faktur bool) {
	t.Helper()

	body := map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01", "faktur_received": faktur,
		"lines": []map[string]any{
			{"product_id": productID, "qty": qty, "unit_price_idr": unit, "ppn_idr": ppn},
		},
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", body, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", got.status, got.body)
	}
}

// TASKS 4.4 and R4.5 over the wire.
//
// The confirmation is blocking because the server refuses the write without it,
// not because a screen shows a dialog. A client that ignores the warning and
// posts anyway gets a 409, and the stock has not moved.
func TestNonPKPToPKPTransferIsBlockedUntilConfirmed(t *testing.T) {
	t.Parallel()

	c, pkp, nonPKP, productID, supplierID := twoCompanies(t)
	// The non-PKP company paid Rp 11.000 of PPN it could never credit.
	c.buyInto(t, nonPKP, supplierID, productID, 10, 10_000, 11_000, true)

	cart := []map[string]any{{"product_id": productID, "qty": 4}}

	// The preview is what lets the dialog quote a figure instead of a warning.
	preview := c.send(stdhttp.MethodPost, "/api/v1/transfers/preview", map[string]any{
		"to_entity_id": pkp, "transfer_date": "2026-10-05", "lines": cart,
	}, hdr(nonPKP))
	if preview.status != stdhttp.StatusOK {
		t.Fatalf("preview: %d %s", preview.status, preview.body)
	}
	var plan struct {
		Direction           string `json:"direction"`
		DestroysInputCredit bool   `json:"destroys_input_credit"`
		TaxableDelivery     bool   `json:"taxable_delivery"`
		CostTotalIDR        int64  `json:"cost_total_idr"`
		ForfeitedPPNIDR     int64  `json:"forfeited_ppn_idr"`
	}
	if err := json.Unmarshal([]byte(preview.body), &plan); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !plan.DestroysInputCredit || plan.TaxableDelivery {
		t.Errorf("preview = %+v, want a non-taxable delivery that destroys credit", plan)
	}
	if plan.Direction != "non-PKP → PKP" {
		t.Errorf("direction = %q", plan.Direction)
	}
	// Four of ten units: Rp 44.400 of cost, Rp 4.400 of input PPN destroyed.
	if plan.CostTotalIDR != 44_400 || plan.ForfeitedPPNIDR != 4_400 {
		t.Errorf("preview cost/forfeited = %d/%d, want 44400/4400", plan.CostTotalIDR, plan.ForfeitedPPNIDR)
	}

	// Posting without the acknowledgement is refused, and nothing moves.
	unconfirmed := c.send(stdhttp.MethodPost, "/api/v1/transfers", map[string]any{
		"to_entity_id": pkp, "transfer_date": "2026-10-05", "lines": cart,
	}, hdr(nonPKP))
	if unconfirmed.status != stdhttp.StatusConflict {
		t.Fatalf("unconfirmed transfer = %d %s, want 409", unconfirmed.status, unconfirmed.body)
	}
	if !strings.Contains(unconfirmed.body, "kredit PPN") {
		t.Errorf("the refusal does not say what is at stake: %s", unconfirmed.body)
	}
	if listed := c.send(stdhttp.MethodGet, "/api/v1/transfers", nil, hdr(nonPKP)); strings.TrimSpace(listed.body) != "[]" {
		t.Errorf("a refused transfer left something behind: %s", listed.body)
	}

	// Confirmed, it goes through and both halves of the movement are reported.
	confirmed := c.send(stdhttp.MethodPost, "/api/v1/transfers", map[string]any{
		"to_entity_id": pkp, "transfer_date": "2026-10-05",
		"acknowledge_credit_loss": true, "lines": cart,
	}, hdr(nonPKP))
	if confirmed.status != stdhttp.StatusCreated {
		t.Fatalf("confirmed transfer: %d %s", confirmed.status, confirmed.body)
	}
	var wrote struct {
		Transfer struct {
			CreditLossAck   bool  `json:"credit_loss_ack"`
			ForfeitedPPNIDR int64 `json:"forfeited_ppn_idr"`
			AmountIDR       int64 `json:"amount_idr"`
		} `json:"transfer"`
		Consumptions int `json:"consumptions"`
	}
	if err := json.Unmarshal([]byte(confirmed.body), &wrote); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !wrote.Transfer.CreditLossAck || wrote.Transfer.ForfeitedPPNIDR != 4_400 {
		t.Errorf("transfer = %+v", wrote.Transfer)
	}
	if wrote.Consumptions != 1 {
		t.Errorf("consumptions = %d, want 1 — the source side must have moved too", wrote.Consumptions)
	}
}

// The flow Olsera gets wrong, checked from the outside: after one call, the
// stock is gone from one company and present in the other.
func TestTransferMovesStockOnBothSidesOverHTTP(t *testing.T) {
	t.Parallel()

	c, pkp, nonPKP, productID, supplierID := twoCompanies(t)
	c.buyInto(t, pkp, supplierID, productID, 10, 10_000, 11_000, true)

	onHand := func(entityID string) int64 {
		t.Helper()
		got := c.send(stdhttp.MethodGet, "/api/v1/opname/count-sheet", nil, hdr(entityID))
		if got.status != stdhttp.StatusOK {
			t.Fatalf("count sheet: %d %s", got.status, got.body)
		}
		var rows []struct {
			ProductID string `json:"product_id"`
			QtyOnHand int64  `json:"qty_on_hand"`
		}
		if err := json.Unmarshal([]byte(got.body), &rows); err != nil {
			t.Fatalf("decode sheet: %v", err)
		}
		for _, r := range rows {
			if r.ProductID == productID {
				return r.QtyOnHand
			}
		}
		return 0
	}

	if before, after := onHand(pkp), onHand(nonPKP); before != 10 || after != 0 {
		t.Fatalf("before the transfer: %d and %d, want 10 and 0", before, after)
	}

	got := c.send(stdhttp.MethodPost, "/api/v1/transfers", map[string]any{
		"to_entity_id": nonPKP, "transfer_date": "2026-10-05",
		"faktur_issued": true, "faktur_no": "010.000-26.00000009",
		"lines": []map[string]any{{"product_id": productID, "qty": 4, "ppn_idr": 4_400}},
	}, hdr(pkp))
	if got.status != stdhttp.StatusCreated {
		t.Fatalf("transfer: %d %s", got.status, got.body)
	}

	if src, dst := onHand(pkp), onHand(nonPKP); src != 6 || dst != 4 {
		t.Errorf("after the transfer: %d and %d, want 6 and 4 — the stock did not move on both sides", src, dst)
	}

	// D-014: the two companies net off against each other, and neither appears
	// in the other's hutang or piutang.
	position := c.send(stdhttp.MethodGet, "/api/v1/reports/inter-company", nil, hdr(pkp))
	if position.status != stdhttp.StatusOK {
		t.Fatalf("position: %d %s", position.status, position.body)
	}
	var rows []struct {
		CounterpartyName string `json:"counterparty_name"`
		NetIDR           int64  `json:"net_idr"`
	}
	if err := json.Unmarshal([]byte(position.body), &rows); err != nil {
		t.Fatalf("decode position: %v", err)
	}
	// Rp 40.000 of cost plus Rp 4.400 of PPN on the delivery.
	if len(rows) != 1 || rows[0].NetIDR != 44_400 || rows[0].CounterpartyName != "PT Medika Jaya" {
		t.Errorf("position = %+v, want PT Medika Jaya owing 44400", rows)
	}
	hutang := c.send(stdhttp.MethodGet, "/api/v1/payables", nil, hdr(nonPKP))
	if hutang.status == stdhttp.StatusOK && strings.Contains(hutang.body, "PT Sehat Sentosa") {
		t.Error("the sending company turned up in the receiving company's hutang report (D-014)")
	}
}

// R10.4: a cashier does not move stock between companies. The transfer sets a
// cost basis and can destroy input PPN credit — margin-bearing, not data entry.
func TestStaffCannotTransferStock(t *testing.T) {
	t.Parallel()

	c, auth, q, ctx := newClient(t)
	pkp, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "PKP", Name: "PT Sehat", IsPkp: 1,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	other, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "NON", Name: "PT Medika", IsPkp: 0,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
	})
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	u, err := auth.CreateUser(ctx, "kasir", "Kasir", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := auth.GrantRole(ctx, u.ID, pkp.ID, service.RoleStaff); err != nil {
		t.Fatalf("grant: %v", err)
	}
	c.login("kasir")

	for _, path := range []string{"/api/v1/transfers", "/api/v1/transfers/preview"} {
		got := c.send(stdhttp.MethodPost, path, map[string]any{
			"to_entity_id": other.ID, "lines": []map[string]any{{"product_id": "x", "qty": 1}},
		}, hdr(pkp.ID))
		if got.status != stdhttp.StatusForbidden {
			t.Errorf("staff POST %s = %d %s, want 403", path, got.status, got.body)
		}
	}
}

// A second company can be created at all.
//
// Found missing while building Phase 4: the setup route refuses once one
// company exists and told the user to ask an owner to add another, which was a
// route that did not exist. Every inter-company feature needs two companies
// (R1.1, R1.2), so without this the transfer screen has nowhere to send
// anything.
func TestASecondCompanyCanBeAddedByAnOwner(t *testing.T) {
	t.Parallel()

	c, first := setupEntity(t)

	// The setup route is for the first company only, and says so.
	again := c.send(stdhttp.MethodPost, "/api/v1/setup/entity", map[string]any{
		"code": "NONPKP", "name": "PT Medika Jaya", "is_pkp": false,
		"timezone": "Asia/Jakarta", "book_year_start_month": 1,
	}, nil)
	if again.status != stdhttp.StatusConflict {
		t.Fatalf("second setup = %d %s, want 409", again.status, again.body)
	}

	added := c.send(stdhttp.MethodPost, "/api/v1/entities", map[string]any{
		"code": "NONPKP", "name": "PT Medika Jaya", "is_pkp": false,
		"timezone": "Asia/Jakarta", "book_year_start_month": 1,
	}, hdr(first))
	if added.status != stdhttp.StatusCreated {
		t.Fatalf("add company: %d %s", added.status, added.body)
	}
	var entity struct {
		ID    string `json:"id"`
		IsPKP bool   `json:"is_pkp"`
	}
	if err := json.Unmarshal([]byte(added.body), &entity); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entity.IsPKP {
		t.Error("the second company was created PKP")
	}

	// The creator can actually reach it — a company nobody can open is not one.
	me := c.send(stdhttp.MethodGet, "/api/v1/auth/me", nil, nil)
	if !strings.Contains(me.body, entity.ID) {
		t.Errorf("the creating owner holds no role in the new company: %s", me.body)
	}
	listed := c.send(stdhttp.MethodGet, "/api/v1/entities", nil, nil)
	if !strings.Contains(listed.body, "PT Medika Jaya") {
		t.Errorf("the new company is not listed: %s", listed.body)
	}
}
