package http_test

import (
	stdhttp "net/http"
	"strings"
	"testing"
)

type omzetBody struct {
	BookYear  int                       `json:"book_year"`
	AsOf      string                    `json:"as_of"`
	CountedTo string                    `json:"counted_to"`
	Window    struct{ From, To string } `json:"window"`
	Threshold struct {
		AmountIDR int64  `json:"amount_idr"`
		WatchBP   int64  `json:"watch_bp"`
		WarnBP    int64  `json:"warn_bp"`
		LegalRef  string `json:"legal_ref"`
	} `json:"threshold"`
	Cumulative struct {
		Label        string `json:"label"`
		AmountIDR    int64  `json:"amount_idr"`
		PercentBP    int64  `json:"percent_bp"`
		RemainingIDR int64  `json:"remaining_idr"`
		Entries      int    `json:"entries"`
	} `json:"cumulative"`
	Trailing12M struct {
		Label      string                    `json:"label"`
		AmountIDR  int64                     `json:"amount_idr"`
		IsEstimate bool                      `json:"is_estimate"`
		Window     struct{ From, To string } `json:"window"`
	} `json:"trailing_12m"`
	State   string `json:"state"`
	IsPKP   bool   `json:"is_pkp"`
	Base    string `json:"base"`
	Caveat  string `json:"caveat"`
	Crossed *struct {
		On         string `json:"on"`
		RegisterBy string `json:"register_by"`
		VATStarts  string `json:"vat_starts"`
		PeakIDR    int64  `json:"peak_idr"`
		PeakOn     string `json:"peak_on"`
	} `json:"crossed"`
	BookYears []int `json:"book_years"`
}

// sellFor rings a sale of the given inclusive value on a business date.
func sellFor(t *testing.T, c *client, entityID, productID, day string, unit int64, qty int) {
	t.Helper()

	got := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": day,
		"lines":     []map[string]any{{"product_id": productID, "qty": qty, "unit_price_idr": unit}},
	}, hdr(entityID))
	if got.status != stdhttp.StatusCreated {
		t.Fatalf("sale on %s: %d %s", day, got.status, got.body)
	}
}

// stockedShop is a company with plenty on the shelf and an open till.
func stockedShop(t *testing.T, c *client, entityID string) string {
	t.Helper()

	productID, supplierID := catalogue(t, c, entityID)
	if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-01-02", "faktur_received": true,
		"faktur_no": "010.000-26.00000001",
		"lines": []map[string]any{
			{"product_id": productID, "qty": 5000, "unit_price_idr": 10_000, "ppn_idr": 5_500_000},
		},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 0, "business_date": "2026-01-02"},
		hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("till: %d %s", got.status, got.body)
	}
	return productID
}

// TestTheOmzetClockLabelsTheLegalFigureApartFromTheEstimate is TASKS 7.10 and
// the whole of SPEC §5.1.
//
// The threshold is measured per book year, cumulative, reset annually. Most
// guidance online quotes a rolling twelve months and is wrong, so the two
// figures leave the server under different names, each carrying its own label,
// and the estimate says so in its own payload. A client cannot relabel one as
// the other without deleting text that says what it is.
func TestTheOmzetClockLabelsTheLegalFigureApartFromTheEstimate(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID := stockedShop(t, c, entityID)

	// Rp 111.000.000 last book year, Rp 55.500.000 in this one.
	sellFor(t, c, entityID, productID, "2026-11-20", 111_000, 1000)
	sellFor(t, c, entityID, productID, "2027-02-10", 111_000, 500)

	got := c.send(stdhttp.MethodGet,
		"/api/v1/omzet?book_year=2027&as_of=2027-06-30", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("omzet: %d %s", got.status, got.body)
	}
	body := decode[omzetBody](t, got.body)

	// The legal figure counts this book year only.
	if body.Cumulative.AmountIDR != 50_000_000 {
		t.Errorf("cumulative = %d, want only 2027's Rp 50.000.000", body.Cumulative.AmountIDR)
	}
	// The estimate spans the boundary the legal figure resets on. That is the
	// entire difference between them.
	if body.Trailing12M.AmountIDR != 150_000_000 {
		t.Errorf("trailing twelve = %d, want Rp 150.000.000 across both years",
			body.Trailing12M.AmountIDR)
	}
	if !body.Trailing12M.IsEstimate {
		t.Error("the trailing figure does not declare itself an estimate")
	}

	// Each says which it is, in the payload, in Indonesian.
	if !strings.Contains(body.Cumulative.Label, "mengikat") {
		t.Errorf("the cumulative figure is not labelled as the binding one: %q", body.Cumulative.Label)
	}
	if !strings.Contains(body.Trailing12M.Label, "estimasi") {
		t.Errorf("the trailing figure is not labelled an estimate: %q", body.Trailing12M.Label)
	}
	if body.Window.From != "2027-01-01" || body.Window.To != "2027-12-31" {
		t.Errorf("book year window = %s..%s", body.Window.From, body.Window.To)
	}
	if body.CountedTo != "2027-06-30" {
		t.Errorf("counted to %q, want the day it was read on", body.CountedTo)
	}
	if body.State != "OK" {
		t.Errorf("state = %s", body.State)
	}
	// The threshold cites its regulation rather than asserting a number.
	if body.Threshold.AmountIDR != 4_800_000_000 || body.Threshold.LegalRef == "" {
		t.Errorf("threshold = %d (%q)", body.Threshold.AmountIDR, body.Threshold.LegalRef)
	}
	if !strings.Contains(body.Caveat, "konsultan pajak") {
		t.Errorf("no compliance caveat: %q", body.Caveat)
	}
}

// TestCrossingEmitsBothDatesOverHTTP is TASKS 7.9 and 7.11 on the wire.
//
// The gap between the two dates is most of this feature's value. A business
// told only the registration date starts charging PPN it does not yet owe; told
// only the obligation date, it misses the registration.
func TestCrossingEmitsBothDatesOverHTTP(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID := stockedShop(t, c, entityID)

	// Rp 777.000.000 taken a month, which is Rp 700.000.000 of turnover net of
	// PPN. Six months is Rp 4,2 miliar — 87,5% of the line and short of it. The
	// seventh takes it to Rp 4,9 miliar and over.
	for month := 1; month <= 6; month++ {
		sellFor(t, c, entityID, productID, monthDay(month), 777_000_000, 1)
	}
	before := decode[omzetBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/omzet?book_year=2026&as_of=2026-06-30", nil, hdr(entityID)).body)
	if before.State == "CROSSED" {
		t.Fatalf("crossed after six months at %d", before.Cumulative.AmountIDR)
	}
	if before.Crossed != nil {
		t.Error("a year that has not crossed carries crossing dates")
	}

	sellFor(t, c, entityID, productID, monthDay(7), 777_000_000, 1)

	body := decode[omzetBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/omzet?book_year=2026&as_of=2026-12-31", nil, hdr(entityID)).body)

	if body.State != "CROSSED" {
		t.Fatalf("state = %s at %d, want CROSSED", body.State, body.Cumulative.AmountIDR)
	}
	if body.Crossed == nil {
		t.Fatal("a crossed year carries no dates")
	}
	if body.Crossed.On != "2026-07-20" {
		t.Errorf("crossed on %s, want the July sale", body.Crossed.On)
	}
	// SPEC §5.2's reading, which is what is seeded.
	if body.Crossed.RegisterBy != "2026-12-31" {
		t.Errorf("register by %s, want the end of the book year", body.Crossed.RegisterBy)
	}
	if body.Crossed.VATStarts != "2027-01-01" {
		t.Errorf("VAT starts %s, want the first day of the next book year", body.Crossed.VATStarts)
	}
	// Two distinct dates, in that order. A payload that collapsed them would
	// lose the point of the feature.
	if body.Crossed.RegisterBy >= body.Crossed.VATStarts {
		t.Errorf("the dates leave no gap: %s and %s", body.Crossed.RegisterBy, body.Crossed.VATStarts)
	}
}

func monthDay(month int) string {
	return "2026-" + string(rune('0'+month/10)) + string(rune('0'+month%10)) + "-20"
}

// TestCrossingSurvivesAReturnThatDropsItBackUnder is SPEC §5.2's stickiness,
// end to end.
//
// The legal event happened. Goods coming back later do not unhappen it, so the
// state stays CROSSED while the cumulative honestly falls below the line — and
// the peak is what makes that pair explicable rather than looking like a bug.
func TestCrossingSurvivesAReturnThatDropsItBackUnder(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID := stockedShop(t, c, entityID)

	sellFor(t, c, entityID, productID, "2026-03-10", 444_000_000, 1) // Rp 400.000.000 net
	// Rp 4.884.000.000 taken is Rp 4.400.000.000 of turnover, which takes the
	// year to Rp 4,8 miliar exactly and over the line.
	got := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"sale_date": "2026-09-15",
		"lines":     []map[string]any{{"product_id": productID, "qty": 1, "unit_price_idr": 4_884_000_000}},
	}, hdr(entityID))
	if got.status != stdhttp.StatusCreated {
		t.Fatalf("crossing sale: %d %s", got.status, got.body)
	}
	rung := decode[struct {
		Sale  struct{ ID string } `json:"sale"`
		Lines []struct {
			ID string `json:"id"`
		} `json:"lines"`
	}](t, got.body)

	crossed := decode[omzetBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/omzet?book_year=2026&as_of=2026-09-30", nil, hdr(entityID)).body)
	if crossed.State != "CROSSED" {
		t.Fatalf("state = %s at %d, want CROSSED", crossed.State, crossed.Cumulative.AmountIDR)
	}

	// The whole of the big sale comes back in November.
	if got := c.send(stdhttp.MethodPost, "/api/v1/sales/"+rung.Sale.ID+"/returns", map[string]any{
		"return_date": "2026-11-02", "reason": "Batal proyek",
		"lines": []map[string]any{{"sale_line_id": rung.Lines[0].ID, "qty": 1}},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("return: %d %s", got.status, got.body)
	}

	after := decode[omzetBody](t, c.send(stdhttp.MethodGet,
		"/api/v1/omzet?book_year=2026&as_of=2026-12-31", nil, hdr(entityID)).body)

	if after.State != "CROSSED" {
		t.Fatalf("state = %s after the return, want CROSSED — a refund does not undo a crossing",
			after.State)
	}
	if after.Cumulative.AmountIDR >= 4_800_000_000 {
		t.Errorf("cumulative = %d, want an honest figure below the threshold",
			after.Cumulative.AmountIDR)
	}
	if after.Crossed == nil || after.Crossed.On != "2026-09-15" {
		t.Errorf("crossing date moved: %+v", after.Crossed)
	}
	// Without the peak, a CROSSED banner over a figure under the line reads as
	// a bug rather than as the rule.
	if after.Crossed.PeakIDR < 4_800_000_000 || after.Crossed.PeakOn != "2026-09-15" {
		t.Errorf("peak = %d on %s", after.Crossed.PeakIDR, after.Crossed.PeakOn)
	}
}

// TestTheOmzetLedgerDecomposes: every figure on the clock expands into the
// documents that produced it.
func TestTheOmzetLedgerDecomposes(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)
	c.login("bapak")
	productID := stockedShop(t, c, entityID)
	sellFor(t, c, entityID, productID, "2026-05-05", 111_000, 10)

	got := c.send(stdhttp.MethodGet, "/api/v1/omzet/ledger?book_year=2026", nil, hdr(entityID))
	if got.status != stdhttp.StatusOK {
		t.Fatalf("ledger: %d %s", got.status, got.body)
	}
	body := decode[struct {
		BookYear int `json:"book_year"`
		Entries  []struct {
			EventType     string `json:"event_type"`
			EffectiveDate string `json:"effective_date"`
			AmountIDR     int64  `json:"amount_idr"`
			InvoiceNo     string `json:"invoice_no"`
		} `json:"entries"`
	}](t, got.body)

	if len(body.Entries) != 1 {
		t.Fatalf("%d entries, want 1", len(body.Entries))
	}
	e := body.Entries[0]
	if e.EventType != "SALE" || e.EffectiveDate != "2026-05-05" || e.AmountIDR != 1_000_000 {
		t.Errorf("entry = %+v", e)
	}
	// The document behind it, so the figure is checkable against a receipt.
	if e.InvoiceNo == "" {
		t.Error("the row does not name the invoice behind it")
	}
}

// TestOnlyTheOwnerChangesTheThreshold. It decides when this business is told it
// must register, and it is the configuration a konsultan pajak is shown.
func TestOnlyTheOwnerChangesTheThreshold(t *testing.T) {
	t.Parallel()

	c, entityID := cast(t)

	newThreshold := map[string]any{
		"amount_idr": 5_000_000_000, "watch_bp": 7000, "warn_bp": 9000,
		"register_by_policy": "END_OF_FOLLOWING_MONTH",
		"vat_starts_policy":  "MONTH_AFTER_REGISTRATION",
		"valid_from":         "2028-01-01", "legal_ref": "PMK contoh",
	}

	c.login("manajer")
	if got := c.send(stdhttp.MethodPost, "/api/v1/omzet/thresholds", newThreshold, hdr(entityID)); got.status != stdhttp.StatusForbidden {
		t.Errorf("manager POST threshold: %d, want 403", got.status)
	}
	// A manager still reads the clock: they run the shop and need to know how
	// close it is to the line.
	if got := c.send(stdhttp.MethodGet, "/api/v1/omzet", nil, hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Errorf("manager GET omzet: %d %s, want 200", got.status, got.body)
	}

	c.login("kasir")
	for _, path := range []string{"/api/v1/omzet", "/api/v1/omzet/thresholds"} {
		got := c.send(stdhttp.MethodGet, path, nil, hdr(entityID))
		if got.status != stdhttp.StatusForbidden {
			t.Errorf("staff GET %s: %d, want 403", path, got.status)
		}
		if strings.Contains(got.body, "amount_idr") {
			t.Errorf("the refusal on %s carries figures: %s", path, got.body)
		}
	}

	// The owner changes it by opening a new row, never by editing one (INV-4).
	c.login("bapak")
	list := decode[struct {
		Thresholds []struct {
			ID      string `json:"id"`
			InForce bool   `json:"in_force"`
		} `json:"thresholds"`
	}](t, c.send(stdhttp.MethodGet, "/api/v1/omzet/thresholds", nil, hdr(entityID)).body)
	if len(list.Thresholds) != 1 || !list.Thresholds[0].InForce {
		t.Fatalf("seeded thresholds = %+v", list.Thresholds)
	}

	// Overlapping is refused; closing first is the way.
	if got := c.send(stdhttp.MethodPost, "/api/v1/omzet/thresholds", newThreshold, hdr(entityID)); got.status != stdhttp.StatusBadRequest {
		t.Fatalf("overlapping threshold: %d %s, want 400", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost,
		"/api/v1/omzet/thresholds/"+list.Thresholds[0].ID+"/close",
		map[string]any{"valid_to": "2027-12-31", "reason": "batas berubah"},
		hdr(entityID)); got.status != stdhttp.StatusOK {
		t.Fatalf("close: %d %s", got.status, got.body)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/omzet/thresholds", newThreshold, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("create: %d %s", got.status, got.body)
	}
}
