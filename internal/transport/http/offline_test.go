package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	terahttp "github.com/fadelmajid/tera/internal/transport/http"
)

// TestNothingReachesTheInternet is the half of TASKS 2.11 a test can prove.
//
// INV-11: the system must function fully with no internet, on the shop LAN
// alone. The other half -- unplugging the router, ringing a sale from a second
// device, watching the receipt come out -- is physical and is written up in
// docs/OFFLINE-DRILL.md. This covers what a machine can check: that no code
// path would have tried.
//
// It reads the source rather than mocking the network, because the failure this
// guards against is someone adding a font CDN, an analytics beacon, or a
// licence check months from now. A mock would not see that; a grep does.
func TestNothingReachesTheInternet(t *testing.T) {
	t.Parallel()

	// Package paths that would pull the process onto the internet at runtime.
	// net/http itself is fine -- it is the server, and the ESC/POS printer is
	// a TCP socket on the LAN.
	banned := []struct{ pattern, why string }{
		{"golang.org/x/net/http2/h2c", "no external transport is needed on a shop LAN"},
		{"cloud.google.com/", "no cloud dependency (INV-11)"},
		{"github.com/aws/", "no cloud dependency (INV-11)"},
		{"go.opentelemetry.io/", "telemetry would phone home from a shop with no internet"},
	}

	// Markup that would make the browser fetch from outside the shop.
	webBanned := []struct{ pattern, why string }{
		{"https://fonts.googleapis.com", "a web font would block the UI when the internet is down"},
		{"https://cdn.", "a CDN dependency breaks the shop when the line drops"},
		{"http://cdn.", "a CDN dependency breaks the shop when the line drops"},
		{"unpkg.com", "a CDN dependency breaks the shop when the line drops"},
		{"jsdelivr.net", "a CDN dependency breaks the shop when the line drops"},
		{"googletagmanager", "analytics has no business in a shop till"},
	}

	// Collect the files first, then read them. Doing both inside the walk
	// callback means returning nil on an error to keep walking, which is the
	// documented idiom but reads as swallowing a failure.
	var files []string
	root := filepath.Join("..", "..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, path := range files {
		ext := filepath.Ext(path)
		isGo := ext == ".go" && !strings.HasSuffix(path, "_test.go")
		isWeb := ext == ".ts" || ext == ".tsx" || ext == ".html" || ext == ".css"
		if !isGo && !isWeb {
			continue
		}

		body, readErr := os.ReadFile(path) //nolint:gosec // walking the repo's own tree
		if readErr != nil {
			t.Errorf("could not read %s: %v", path, readErr)
			continue
		}
		text := string(body)

		rules := webBanned
		if isGo {
			rules = banned
		}
		for _, rule := range rules {
			if strings.Contains(text, rule.pattern) {
				t.Errorf("%s references %q: %s", path, rule.pattern, rule.why)
			}
		}
	}
}

// The server binds every interface, not loopback. Bound to localhost the
// software runs perfectly and nobody but the server machine can reach it --
// which is indistinguishable from "the network is down" to a cashier on a
// second device (R8.4).
func TestServerBindsTheLAN(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(defaultAddr(), "0.0.0.0:") {
		t.Errorf("default bind address is %q, want 0.0.0.0 so other devices on the shop LAN can reach it",
			defaultAddr())
	}
}

// TestSaleCompletesWithoutDNSOrOutboundNetwork rings a sale end to end with
// outbound name resolution replaced by a resolver that fails.
//
// If any part of the sale path tried to reach a host, this would surface it.
// The shop's router works; their internet does not, and the difference is the
// whole deployment model (REQUIREMENTS §10).
func TestSaleCompletesWithoutDNSOrOutboundNetwork(t *testing.T) {
	c, entityID := setupEntity(t)
	productID, supplierID := catalogue(t, c, entityID)

	// Stock to sell.
	if got := c.send(stdhttp.MethodPost, "/api/v1/purchases", map[string]any{
		"supplier_id": supplierID, "purchase_date": "2026-10-01",
		"lines": []map[string]any{{"product_id": productID, "qty": 10, "unit_price_idr": 10_000}},
	}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("purchase: %d %s", got.status, got.body)
	}

	// Every outbound name lookup now fails, as it would with the line unplugged.
	// Not parallel: the resolver is process-wide.
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errNoInternet
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })

	if got := c.send(stdhttp.MethodPost, "/api/v1/cash-sessions",
		map[string]any{"opening_float_idr": 500_000}, hdr(entityID)); got.status != stdhttp.StatusCreated {
		t.Fatalf("open till with no internet: %d %s", got.status, got.body)
	}

	sale := c.send(stdhttp.MethodPost, "/api/v1/sales", map[string]any{
		"lines": []map[string]any{{"product_id": productID, "qty": 3}},
	}, hdr(entityID))
	if sale.status != stdhttp.StatusCreated {
		t.Fatalf("ring a sale with no internet: %d %s", sale.status, sale.body)
	}

	var out struct {
		Sale struct {
			ID        string `json:"id"`
			InvoiceNo string `json:"invoice_no"`
		} `json:"sale"`
		COGS int64 `json:"cogs_idr"`
	}
	if err := json.Unmarshal([]byte(sale.body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.COGS != 30_000 {
		t.Errorf("COGS = %d, want 30000", out.COGS)
	}

	// And the receipt renders, which is what actually reaches the paper.
	receipt := c.send(stdhttp.MethodGet, "/api/v1/sales/"+out.Sale.ID+"/receipt", nil, hdr(entityID))
	if receipt.status != stdhttp.StatusOK {
		t.Fatalf("render receipt with no internet: %d %s", receipt.status, receipt.body)
	}
	var preview struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(receipt.body), &preview); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if !strings.Contains(preview.Text, out.Sale.InvoiceNo) {
		t.Errorf("receipt does not carry the invoice number:\n%s", preview.Text)
	}
	if !strings.Contains(preview.Text, "PT Sehat Sentosa") {
		t.Errorf("receipt does not carry the shop name:\n%s", preview.Text)
	}
}

// errNoInternet stands in for an unplugged line.
var errNoInternet = errors.New("no internet, as designed")

// defaultAddr is the bind address the server uses when none is configured.
func defaultAddr() string { return terahttp.DefaultAddr }
