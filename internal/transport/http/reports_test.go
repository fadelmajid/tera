package http_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

type salesReportBody struct {
	Title   string                    `json:"title"`
	Caveat  string                    `json:"caveat"`
	Period  struct{ From, To string } `json:"period"`
	Summary struct {
		SaleCount      int64 `json:"sale_count"`
		DPPIDR         int64 `json:"dpp_idr"`
		PPNIDR         int64 `json:"ppn_idr"`
		TotalIDR       int64 `json:"total_idr"`
		COGSIDR        int64 `json:"cogs_idr"`
		GrossMarginIDR int64 `json:"gross_margin_idr"`
		VoidCount      int64 `json:"void_count"`
		ReturnCount    int64 `json:"return_count"`
		RefundIDR      int64 `json:"refund_idr"`
	} `json:"summary"`
	ByDay []struct {
		BusinessDate string `json:"business_date"`
		TotalIDR     int64  `json:"total_idr"`
	} `json:"by_day"`
	ByProduct []struct {
		ProductCode string `json:"product_code"`
		Qty         int64  `json:"qty"`
		RevenueIDR  int64  `json:"revenue_idr"`
		MarginIDR   int64  `json:"margin_idr"`
	} `json:"by_product"`
	ByMethod []struct {
		Method    string `json:"method"`
		AmountIDR int64  `json:"amount_idr"`
	} `json:"by_method"`
}

type purchasesReportBody struct {
	Summary struct {
		PurchaseCount    int64 `json:"purchase_count"`
		TotalIDR         int64 `json:"total_idr"`
		WithFakturIDR    int64 `json:"with_faktur_idr"`
		WithoutFakturIDR int64 `json:"without_faktur_idr"`
		PPNIntoCostIDR   int64 `json:"ppn_into_cost_idr"`
	} `json:"summary"`
	BySupplier []struct {
		SupplierName    string `json:"supplier_name"`
		PurchaseCount   int64  `json:"purchase_count"`
		WithFakturCount int64  `json:"with_faktur_count"`
		PPNIntoCostIDR  int64  `json:"ppn_into_cost_idr"`
	} `json:"by_supplier"`
	ByProduct []struct {
		ProductCode  string `json:"product_code"`
		Qty          int64  `json:"qty"`
		CostTotalIDR int64  `json:"cost_total_idr"`
	} `json:"by_product"`
}

type stockReportBody struct {
	Summary struct {
		Lines    int64 `json:"lines"`
		QtyTotal int64 `json:"qty_total"`
		ValueIDR int64 `json:"value_idr"`
	} `json:"summary"`
	OnHand []struct {
		ProductCode         string `json:"product_code"`
		OwnerName           string `json:"owner_name"`
		QtyOnHand           int64  `json:"qty_on_hand"`
		ValueIDR            int64  `json:"value_idr"`
		LayerCount          int64  `json:"layer_count"`
		LayersWithoutFaktur int64  `json:"layers_without_faktur"`
	} `json:"on_hand"`
	OutOfStock []struct {
		ProductCode string `json:"product_code"`
	} `json:"out_of_stock"`
	Movement []struct {
		ProductCode  string `json:"product_code"`
		MovementType string `json:"movement_type"`
		Qty          int64  `json:"qty"`
	} `json:"movement"`
	Intake []struct {
		ProductCode string `json:"product_code"`
		Source      string `json:"source"`
		Qty         int64  `json:"qty"`
	} `json:"intake"`
}

type agingBody struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	AsOf    string   `json:"as_of"`
	Order   []string `json:"bucket_order"`
	Buckets struct {
		NotYetDue  int64 `json:"NOT_YET_DUE"`
		Days1To30  int64 `json:"1_30"`
		Days31To60 int64 `json:"31_60"`
		Days61To90 int64 `json:"61_90"`
		Over90     int64 `json:"OVER_90"`
		NoDueDate  int64 `json:"NO_DUE_DATE"`
		TotalIDR   int64 `json:"total_idr"`
		OverdueIDR int64 `json:"overdue_idr"`
	} `json:"buckets"`
	Counterparties []struct {
		Name           string `json:"name"`
		TotalIDR       int64  `json:"total_idr"`
		OldestDays     int    `json:"oldest_days"`
		WithoutDueDate int    `json:"without_due_date"`
		Items          []struct {
			ID             string `json:"id"`
			DocumentNo     string `json:"document_no"`
			Bucket         string `json:"bucket"`
			DaysOverdue    int    `json:"days_overdue"`
			OutstandingIDR int64  `json:"outstanding_idr"`
			PaidIDR        int64  `json:"paid_idr"`
		} `json:"items"`
	} `json:"counterparties"`
	WithoutDueDate int   `json:"without_due_date"`
	CreditIDR      int64 `json:"credit_idr"`
}

// TestTheSalesReportOverHTTP is TASKS 6.1.
func TestTheSalesReportOverHTTP(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID)

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/sales?from=2026-10-01&to=2026-10-31", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("sales report: %d %s", got.status, got.body)
	}
	body := decode[salesReportBody](t, got.body)

	if body.Title != "Laporan Penjualan" {
		t.Errorf("title = %q", body.Title)
	}
	if body.Period.From != "2026-10-01" || body.Period.To != "2026-10-31" {
		t.Errorf("period = %s..%s", body.Period.From, body.Period.To)
	}

	// Two boxes at Rp 111.000 inclusive: Rp 222.000 taken, Rp 200.000 of
	// revenue and Rp 22.000 of PPN.
	if body.Summary.SaleCount != 1 || body.Summary.TotalIDR != 222_000 {
		t.Errorf("%d sales totalling %d, want 1 at 222000",
			body.Summary.SaleCount, body.Summary.TotalIDR)
	}
	if body.Summary.DPPIDR != 200_000 || body.Summary.PPNIDR != 22_000 {
		t.Errorf("DPP %d + PPN %d, want 200000 + 22000", body.Summary.DPPIDR, body.Summary.PPNIDR)
	}
	// Margin is on the revenue, not on the takings: the PPN was never the
	// shop's money.
	if body.Summary.GrossMarginIDR != body.Summary.DPPIDR-body.Summary.COGSIDR {
		t.Errorf("margin %d does not equal DPP %d less COGS %d",
			body.Summary.GrossMarginIDR, body.Summary.DPPIDR, body.Summary.COGSIDR)
	}
	// And it says so, so nobody reads it as profit.
	if !strings.Contains(body.Caveat, "bukan laba rugi") {
		t.Errorf("the payload does not say this is not a profit figure: %q", body.Caveat)
	}

	if len(body.ByDay) != 1 || body.ByDay[0].BusinessDate != "2026-10-10" {
		t.Errorf("by-day is %+v", body.ByDay)
	}
	if len(body.ByProduct) != 1 || body.ByProduct[0].Qty != 2 {
		t.Errorf("by-product is %+v", body.ByProduct)
	}
	if len(body.ByMethod) != 1 || body.ByMethod[0].AmountIDR != 222_000 {
		t.Errorf("by-method is %+v", body.ByMethod)
	}

	// The parts sum to the whole, on the screen as in the engine.
	var dayTotal int64
	for _, d := range body.ByDay {
		dayTotal += d.TotalIDR
	}
	if dayTotal != body.Summary.TotalIDR {
		t.Errorf("the days total %d against a summary of %d", dayTotal, body.Summary.TotalIDR)
	}
}

// TestThePurchasesReportSeparatesFakturFromNoFaktur is TASKS 6.2, and the
// figure the business cannot see today: PPN paid with no faktur is not
// creditable and went into the cost of the goods instead (INV-9, SPEC §3.2).
func TestThePurchasesReportSeparatesFakturFromNoFaktur(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID) // one purchase with a faktur, one without

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/purchases?from=2026-10-01&to=2026-10-31", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("purchases report: %d %s", got.status, got.body)
	}
	body := decode[purchasesReportBody](t, got.body)

	if body.Summary.PurchaseCount != 2 {
		t.Fatalf("%d purchases, want 2", body.Summary.PurchaseCount)
	}
	if body.Summary.WithFakturIDR != 111_000 || body.Summary.WithoutFakturIDR != 111_000 {
		t.Errorf("faktur split = %d with, %d without, want 111000 each",
			body.Summary.WithFakturIDR, body.Summary.WithoutFakturIDR)
	}
	// The whole point: Rp 11.000 of PPN that will never be credited and is
	// sitting in the cost of the goods.
	if body.Summary.PPNIntoCostIDR != 11_000 {
		t.Errorf("PPN into cost = %d, want 11000", body.Summary.PPNIntoCostIDR)
	}

	if len(body.BySupplier) != 1 {
		t.Fatalf("%d suppliers, want 1", len(body.BySupplier))
	}
	s := body.BySupplier[0]
	if s.PurchaseCount != 2 || s.WithFakturCount != 1 || s.PPNIntoCostIDR != 11_000 {
		t.Errorf("supplier line = %+v", s)
	}
	if len(body.ByProduct) != 1 || body.ByProduct[0].Qty != 20 {
		t.Errorf("by-product = %+v", body.ByProduct)
	}
}

// TestTheStockReportShowsTheShelfPerOwner is TASKS 6.3. Stock is
// owner-attributed, and a total mixing two family members' goods is not a
// figure either of them can use (INV-8).
func TestTheStockReportShowsTheShelfPerOwner(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID) // 20 in, 2 sold

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/stock?from=2026-10-01&to=2026-10-31", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("stock report: %d %s", got.status, got.body)
	}
	body := decode[stockReportBody](t, got.body)

	if len(body.OnHand) != 1 {
		t.Fatalf("%d on-hand lines, want 1", len(body.OnHand))
	}
	row := body.OnHand[0]
	if row.QtyOnHand != 18 {
		t.Errorf("on hand = %d, want 18", row.QtyOnHand)
	}
	if row.OwnerName != "Budi" {
		t.Errorf("owner = %q, want the attribution the stock carries", row.OwnerName)
	}
	// Two layers, one of which arrived without a faktur and therefore cost 11%
	// more (SPEC §3.2). Surfaced so the difference is visible on the shelf.
	if row.LayerCount != 2 || row.LayersWithoutFaktur != 1 {
		t.Errorf("%d layers, %d without a faktur, want 2 and 1",
			row.LayerCount, row.LayersWithoutFaktur)
	}
	if body.Summary.QtyTotal != 18 || body.Summary.ValueIDR != row.ValueIDR {
		t.Errorf("summary %+v does not match the single line", body.Summary)
	}

	var sold int64
	for _, m := range body.Movement {
		if m.MovementType == "SALE" {
			sold += m.Qty
		}
	}
	if sold != 2 {
		t.Errorf("movement reports %d sold, want 2", sold)
	}

	var received int64
	for _, i := range body.Intake {
		if i.Source == "PURCHASE" {
			received += i.Qty
		}
	}
	if received != 20 {
		t.Errorf("intake reports %d received, want 20", received)
	}
}

// debts seeds hutang: one overdue, one not yet due, one with no term at all,
// and one already part-paid.
// create posts a record and hands back its id.
func create(t *testing.T, c *client, entityID, path string, body map[string]any) string {
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
	if out.ID == "" {
		t.Fatalf("POST %s returned no id: %s", path, got.body)
	}
	return out.ID
}

func debts(t *testing.T, c *client, entityID string) {
	t.Helper()

	supplierID := create(t, c, entityID, "/api/v1/suppliers",
		map[string]any{"code": "S-AGE", "name": "PT Terlambat", "issues_faktur": true})

	for _, d := range []struct {
		invoice string
		due     string
		amount  int64
		paid    int64
	}{
		{"INV-LATE", "2026-06-01", 10_000_000, 0},
		{"INV-SOON", "2026-12-31", 4_000_000, 0},
		{"INV-NOTERM", "", 3_000_000, 0},
		{"INV-PART", "2026-10-01", 5_000_000, 2_000_000},
	} {
		body := map[string]any{
			"party_id": supplierID, "invoice_no": d.invoice,
			"amount_idr": d.amount, "incurred_on": "2026-05-01",
		}
		if d.due != "" {
			body["due_date"] = d.due
		}
		id := create(t, c, entityID, "/api/v1/opening/payables", body)

		if d.paid > 0 {
			got := c.send(stdhttp.MethodPost, "/api/v1/payables/"+id+"/payments",
				map[string]any{"amount_idr": d.paid, "paid_on": "2026-09-01", "method": "TRANSFER"},
				hdr(entityID))
			if got.status != stdhttp.StatusCreated {
				t.Fatalf("payment: %d %s", got.status, got.body)
			}
		}
	}
}

// TestTheHutangReportAgesWhatIsOwed is TASKS 6.4 and R5.8: a balance without an
// age is not actionable.
func TestTheHutangReportAgesWhatIsOwed(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	debts(t, c, entityID)

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/payables?as_of=2026-10-21", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("payables: %d %s", got.status, got.body)
	}
	body := decode[agingBody](t, got.body)

	if body.Kind != "HUTANG" || body.AsOf != "2026-10-21" {
		t.Errorf("report is %s as of %s", body.Kind, body.AsOf)
	}
	if len(body.Order) != 6 {
		t.Errorf("%d buckets in the order, want 6", len(body.Order))
	}

	// Rp 10.000.000 due on 1 June is 142 days late; Rp 3.000.000 owed on the
	// invoice part-paid on 1 October is 20 days late.
	if body.Buckets.Over90 != 10_000_000 {
		t.Errorf("over-90 = %d, want 10000000", body.Buckets.Over90)
	}
	if body.Buckets.Days1To30 != 3_000_000 {
		t.Errorf("1-30 = %d, want the 3000000 still owed on the part-paid invoice",
			body.Buckets.Days1To30)
	}
	if body.Buckets.NotYetDue != 4_000_000 {
		t.Errorf("not-yet-due = %d, want 4000000", body.Buckets.NotYetDue)
	}
	// Never folded into "current": nobody recorded a term, so nobody knows.
	if body.Buckets.NoDueDate != 3_000_000 {
		t.Errorf("no-due-date = %d, want 3000000", body.Buckets.NoDueDate)
	}
	if body.WithoutDueDate != 1 {
		t.Errorf("%d documents without a term, want 1", body.WithoutDueDate)
	}
	if body.Buckets.OverdueIDR != 13_000_000 {
		t.Errorf("overdue = %d, want 13000000 — the undated invoice is not claimed as late",
			body.Buckets.OverdueIDR)
	}
	if body.Buckets.TotalIDR != 20_000_000 {
		t.Errorf("total = %d, want 20000000", body.Buckets.TotalIDR)
	}

	if len(body.Counterparties) != 1 {
		t.Fatalf("%d counterparties, want 1", len(body.Counterparties))
	}
	party := body.Counterparties[0]
	if party.OldestDays != 142 {
		t.Errorf("oldest = %d days, want 142", party.OldestDays)
	}
	if len(party.Items) != 4 {
		t.Fatalf("%d documents, want 4", len(party.Items))
	}
	// Worst first, undated last.
	if party.Items[0].DocumentNo != "INV-LATE" {
		t.Errorf("first document is %s, want the latest one", party.Items[0].DocumentNo)
	}
	if party.Items[3].Bucket != "NO_DUE_DATE" {
		t.Errorf("last document is %s, want the undated one", party.Items[3].Bucket)
	}
	// R5.8's partial payment, visible on the row rather than only in a total.
	for _, it := range party.Items {
		if it.DocumentNo == "INV-PART" && (it.PaidIDR != 2_000_000 || it.OutstandingIDR != 3_000_000) {
			t.Errorf("part-paid invoice: paid %d, outstanding %d", it.PaidIDR, it.OutstandingIDR)
		}
	}
}

// TestOnePartPaidInvoiceDecomposesIntoItsPayments is the other half of R5.8.
//
// "Rp 3.000.000 outstanding" answers nothing when the supplier's question is
// which invoice last month's transfer was against.
func TestOnePartPaidInvoiceDecomposesIntoItsPayments(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	debts(t, c, entityID)

	report := decode[agingBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/reports/payables?as_of=2026-10-21", nil, hdr(entityID)).body)

	var id string
	for _, it := range report.Counterparties[0].Items {
		if it.DocumentNo == "INV-PART" {
			id = it.ID
		}
	}
	if id == "" {
		t.Fatal("the part-paid invoice is not on the report")
	}

	got := c.send(stdhttp.MethodGet, "/api/v1/payables/"+id, nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("detail: %d %s", got.status, got.body)
	}
	detail := decode[struct {
		Document struct {
			AmountIDR      int64 `json:"amount_idr"`
			PaidIDR        int64 `json:"paid_idr"`
			OutstandingIDR int64 `json:"outstanding_idr"`
		} `json:"document"`
		Payments []struct {
			AmountIDR int64  `json:"amount_idr"`
			PaidOn    string `json:"paid_on"`
			Method    string `json:"method"`
		} `json:"payments"`
	}](t, got.body)

	if len(detail.Payments) != 1 {
		t.Fatalf("%d payments, want 1", len(detail.Payments))
	}
	if detail.Payments[0].AmountIDR != 2_000_000 || detail.Payments[0].Method != "TRANSFER" {
		t.Errorf("payment = %+v", detail.Payments[0])
	}
	// The outstanding figure is derived from the payment beneath it, not stored
	// beside it.
	if detail.Document.AmountIDR-detail.Payments[0].AmountIDR != detail.Document.OutstandingIDR {
		t.Errorf("%d less %d is not the %d outstanding",
			detail.Document.AmountIDR, detail.Payments[0].AmountIDR, detail.Document.OutstandingIDR)
	}
}

// TestThePiutangReportAgesWhatIsOwedToTheShop is TASKS 6.5.
func TestThePiutangReportAgesWhatIsOwedToTheShop(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")

	customerID := create(t, c, entityID, "/api/v1/customers",
		map[string]any{"code": "C-1", "name": "Klinik Sehat"})
	create(t, c, entityID, "/api/v1/opening/receivables", map[string]any{
		"party_id": customerID, "invoice_no": "PIU-1",
		"amount_idr": 7_000_000, "incurred_on": "2026-05-01", "due_date": "2026-07-01",
	})

	got := c.send(stdhttp.MethodGet,
		"/api/v1/reports/receivables?as_of=2026-08-21", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("receivables: %d %s", got.status, got.body)
	}
	body := decode[agingBody](t, got.body)

	if body.Kind != "PIUTANG" {
		t.Errorf("kind = %q", body.Kind)
	}
	if body.Buckets.Days31To60 != 7_000_000 {
		t.Errorf("31-60 = %d, want the 7000000 owed since 1 July", body.Buckets.Days31To60)
	}
	if body.Counterparties[0].Name != "Klinik Sehat" {
		t.Errorf("counterparty = %q", body.Counterparties[0].Name)
	}
	if body.Counterparties[0].Items[0].DaysOverdue != 51 {
		t.Errorf("days overdue = %d, want 51", body.Counterparties[0].Items[0].DaysOverdue)
	}
}

// TestReportsAreNotForTheCashier. Every one of them carries a cost figure, and
// a cashier needs none of it to ring a sale.
func TestReportsAreNotForTheCashier(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("kasir")

	for _, path := range []string{
		"/api/v1/reports/sales",
		"/api/v1/reports/purchases",
		"/api/v1/reports/stock",
		"/api/v1/reports/payables",
		"/api/v1/reports/receivables",
	} {
		got := c.send(stdhttp.MethodGet, path, nil, hdr(entityID))
		if got.status != stdhttp.StatusForbidden {
			t.Errorf("staff GET %s: %d, want 403", path, got.status)
		}
		if strings.Contains(got.body, "cogs_idr") || strings.Contains(got.body, "value_idr") {
			t.Errorf("the refusal on %s carries report data: %s", path, got.body)
		}
	}

	c.login("manajer")
	for _, path := range []string{"/api/v1/reports/sales", "/api/v1/reports/payables"} {
		if got := c.send(stdhttp.MethodGet, path, nil, hdr(entityID)); got.status != stdhttp.StatusOK {
			t.Errorf("manager GET %s: %d %s, want 200", path, got.status, got.body)
		}
	}
}

// --- the export (TASKS 6.6, R14.3) ------------------------------------------

// TestTheExportIsAWorkingZipOfEverything is the exit route, over the wire.
func TestTheExportIsAWorkingZipOfEverything(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID)

	got := c.send(stdhttp.MethodGet, "/api/v1/export", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("export: %d %s", got.status, got.body)
	}

	body := []byte(got.body)
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the download is not a readable zip: %v", err)
	}

	names := make(map[string]bool, len(r.File))
	for _, f := range r.File {
		names[f.Name] = true
	}
	for _, want := range []string{
		"README.md", "manifest.json",
		"tables/sale.csv", "tables/purchase.csv", "tables/stock_layer.csv",
		"tables/payable.csv", "tables/product.csv", "tables/goose_db_version.csv",
	} {
		if !names[want] {
			t.Errorf("the archive has no %s", want)
		}
	}

	// The download names itself, so the file in somebody's Downloads folder is
	// identifiable a year later.
	if disposition := got.header.Get("Content-Disposition"); !strings.Contains(disposition, "tera-ekspor-") {
		t.Errorf("Content-Disposition = %q", disposition)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	// And says what it holds without being unzipped first.
	if got.header.Get("X-Tera-Schema-Version") == "" || got.header.Get("X-Tera-Total-Rows") == "" {
		t.Error("the response does not state the schema version and row count")
	}
}

// TestTheExportManifestPreviewsWhatWouldBeDownloaded. A list of tables and row
// counts is the only way a person can tell a complete export from a plausible
// one.
func TestTheExportManifestPreviewsWhatWouldBeDownloaded(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	trade(t, c, entityID)

	got := c.send(stdhttp.MethodGet, "/api/v1/export/manifest", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("manifest: %d %s", got.status, got.body)
	}
	body := decode[struct {
		SchemaVersion int64 `json:"schema_version"`
		TotalRows     int64 `json:"total_rows"`
		Objects       []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Rows int64  `json:"rows"`
		} `json:"objects"`
	}](t, got.body)

	if body.SchemaVersion == 0 {
		t.Error("the manifest does not say which schema version it describes")
	}
	if len(body.Objects) < 20 {
		t.Errorf("the manifest lists %d objects; the schema has more", len(body.Objects))
	}
	if body.TotalRows == 0 {
		t.Error("the manifest reports no rows against a database that has traded")
	}
}

// TestTheExportNeedsOwnershipOfEveryCompany draws the boundary R13.4 exists for.
//
// The archive is the whole database and cannot be scoped to one company: the
// products, owners, suppliers and customers are shared by design (D-006). So
// the gate is ownership everywhere — otherwise somebody who owns the non-PKP
// company could download the PKP company's entire history.
func TestTheExportNeedsOwnershipOfEveryCompany(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)

	// A second company that bapak has no role in, created behind the API so no
	// role is granted with it.
	if _, err := c.q.CreateLegalEntity(c.ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "LAIN", Name: "PT Perusahaan Lain", IsPkp: 0,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1,
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("second entity: %v", err)
	}

	c.login("bapak")
	got := c.send(stdhttp.MethodGet, "/api/v1/export", nil, hdr(entityID))
	if got.status != stdhttp.StatusForbidden {
		t.Fatalf("export with a company unowned: %d, want 403", got.status)
	}
	// The refusal names the company, because the fix is a role grant.
	if !strings.Contains(got.body, "PT Perusahaan Lain") {
		t.Errorf("the refusal does not say which company is missing: %s", got.body)
	}

	// A manager never gets it at all.
	c.login("manajer")
	if got := c.send(stdhttp.MethodGet, "/api/v1/export", nil, hdr(entityID)); got.status != stdhttp.StatusForbidden {
		t.Errorf("manager export: %d, want 403", got.status)
	}
}
