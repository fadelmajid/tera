package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	terahttp "github.com/fadelmajid/tera/internal/transport/http"
)

type fakeChecker struct {
	pingErr    error
	version    int64
	versionErr error
}

func (f fakeChecker) PingContext(context.Context) error      { return f.pingErr }
func (f fakeChecker) Version(context.Context) (int64, error) { return f.version, f.versionErr }

// The server serves the shop LAN. Bound to loopback the software runs
// perfectly and nobody but the server machine can reach it (R8.4).
func TestDefaultAddrBindsEveryInterface(t *testing.T) {
	t.Parallel()

	if terahttp.DefaultAddr != "0.0.0.0:8080" {
		t.Fatalf("DefaultAddr = %q, want 0.0.0.0:8080", terahttp.DefaultAddr)
	}

	host, port, err := net.SplitHostPort(terahttp.DefaultAddr)
	if err != nil {
		t.Fatalf("DefaultAddr is not host:port: %v", err)
	}
	if port == "" {
		t.Error("DefaultAddr carries no port")
	}

	ip := net.ParseIP(host)
	if ip == nil {
		t.Fatalf("DefaultAddr host %q is not an IP — a hostname could resolve to loopback", host)
	}
	if ip.IsLoopback() {
		t.Error("DefaultAddr is loopback; clients are browsers on the shop LAN, not this machine")
	}
	if !ip.IsUnspecified() {
		t.Errorf("DefaultAddr host %q is not the unspecified address", host)
	}
}

func TestNewDefaultsToTheLANAddr(t *testing.T) {
	t.Parallel()

	s := terahttp.New(terahttp.Config{DB: fakeChecker{}})
	if s.Addr() != terahttp.DefaultAddr {
		t.Errorf("Addr() = %q, want %q", s.Addr(), terahttp.DefaultAddr)
	}
}

// response is what a test actually wants to assert on, with the body already
// read and the connection already closed.
type response struct {
	status int
	header stdhttp.Header
	body   string
}

func do(t *testing.T, cfg terahttp.Config, method, path string) response {
	t.Helper()

	srv := httptest.NewServer(terahttp.Handler(cfg))
	t.Cleanup(srv.Close)

	req, err := stdhttp.NewRequestWithContext(context.Background(), method, srv.URL+path, stdhttp.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// Liveness touches nothing else, so a database problem cannot make the process
// look dead and get it restarted.
func TestHealthzIsIndependentOfTheDatabase(t *testing.T) {
	t.Parallel()

	cfg := terahttp.Config{DB: fakeChecker{pingErr: errors.New("database is down")}}
	got := do(t, cfg, stdhttp.MethodGet, "/healthz")

	if got.status != stdhttp.StatusOK {
		t.Errorf("status = %d, want 200 even with the database down", got.status)
	}
	if !strings.Contains(got.body, `"ok"`) {
		t.Errorf("body = %q, want an ok status", got.body)
	}
}

func TestReadyz(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		db         terahttp.Checker
		wantStatus int
	}{
		{"ready", fakeChecker{version: 1}, stdhttp.StatusOK},
		{"database down", fakeChecker{pingErr: errors.New("no")}, stdhttp.StatusServiceUnavailable},
		{"schema unreadable", fakeChecker{versionErr: errors.New("no")}, stdhttp.StatusServiceUnavailable},
		{"no database configured", nil, stdhttp.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := do(t, terahttp.Config{DB: tc.db}, stdhttp.MethodGet, "/readyz")
			if got.status != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", got.status, tc.wantStatus, got.body)
			}

			if tc.wantStatus == stdhttp.StatusOK {
				var decoded struct {
					Status        string `json:"status"`
					SchemaVersion int64  `json:"schema_version"`
				}
				if err := json.Unmarshal([]byte(got.body), &decoded); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if decoded.SchemaVersion != 1 {
					t.Errorf("schema_version = %d, want 1", decoded.SchemaVersion)
				}
			}
		})
	}
}

func TestUnknownAPIEndpointReturnsJSON(t *testing.T) {
	t.Parallel()

	got := do(t, terahttp.Config{DB: fakeChecker{}}, stdhttp.MethodGet, "/api/v1/tidak-ada")

	if got.status != stdhttp.StatusNotFound {
		t.Errorf("status = %d, want 404", got.status)
	}
	if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want JSON", ct)
	}
}

// UI copy is Bahasa Indonesia from the start, not English translated later
// (R9.12). That applies to the placeholder too.
func TestRootPlaceholderIsInIndonesian(t *testing.T) {
	t.Parallel()

	got := do(t, terahttp.Config{DB: fakeChecker{}}, stdhttp.MethodGet, "/")

	if !strings.Contains(got.body, "berjalan") {
		t.Errorf("root page is not in Indonesian: %q", got.body)
	}
}
