package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
	terahttp "github.com/fadelmajid/tera/internal/transport/http"
)

// client keeps a cookie jar so a login carries into later requests, the way a
// browser on the shop LAN does.
type client struct {
	t    *testing.T
	hc   *stdhttp.Client
	ur   string
	auth *service.Auth
	q    *gen.Queries
	ctx  context.Context
}

func (c *client) deps() (*client, *service.Auth, *gen.Queries, context.Context) {
	return c, c.auth, c.q, c.ctx
}

func newClient(t *testing.T) (c *client, auth *service.Auth, q *gen.Queries, ctx context.Context) {
	t.Helper()

	// Real bcrypt cost here: this test exercises the production path end to
	// end, and a handful of hashes is a second well spent.
	ctx = context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	auth = service.NewAuth(db, time.Now)
	srv := httptest.NewServer(terahttp.Handler(terahttp.Config{
		DB:     db,
		Auth:   auth,
		Idem:   service.NewIdempotency(db, time.Now),
		Master: service.NewMasterData(db, service.NewAuditor(time.Now), time.Now),
	}))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}

	c = &client{t: t, hc: &stdhttp.Client{Jar: jar}, ur: srv.URL, auth: auth, q: gen.New(db), ctx: ctx}
	return c, auth, c.q, ctx
}

// setupEntity gets a caller to the state every entity-scoped test needs: a
// logged-in owner of one company.
func setupEntity(t *testing.T) (c *client, entityID string) {
	t.Helper()

	c, auth, _, ctx := newClient(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("user: %v", err)
	}
	if got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "budi", "password": "rahasia-panjang"}, nil); got.status != stdhttp.StatusOK {
		t.Fatalf("login: %d %s", got.status, got.body)
	}

	got := c.send(stdhttp.MethodPost, "/api/v1/setup/entity", map[string]any{
		"code": "PKP", "name": "PT Sehat Sentosa", "is_pkp": true,
		"timezone": "Asia/Jakarta", "book_year_start_month": 1,
	}, nil)
	if got.status != stdhttp.StatusCreated {
		t.Fatalf("setup: %d %s", got.status, got.body)
	}

	var entity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(got.body), &entity); err != nil {
		t.Fatalf("decode entity: %v", err)
	}
	return c, entity.ID
}

func (c *client) send(method, path string, body any, headers map[string]string) response {
	c.t.Helper()

	var raw string
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode: %v", err)
		}
		raw = string(encoded)
	}
	return c.sendRaw(method, path, raw, headers)
}

// sendRaw posts a body verbatim, so a test can send JSON Go would never
// produce — a fractional rupiah, for instance (INV-1).
func (c *client) sendRaw(method, path, body string, headers map[string]string) response {
	c.t.Helper()

	req, err := stdhttp.NewRequestWithContext(context.Background(), method, c.ur+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if method != stdhttp.MethodGet {
		req.Header.Set(terahttp.ClientRequestHeader, store.NewID())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	out, err := readAll(resp)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return out
}

func TestLoginMeLogoutFlow(t *testing.T) {
	t.Parallel()

	c, auth, q, ctx := newClient(t)

	e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: "PKP", Name: "PT Sehat", IsPkp: 1,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1, CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	u, err := auth.CreateUser(ctx, "budi", "Budi Santoso", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := auth.GrantRole(ctx, u.ID, e.ID, service.RoleManager); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// Anonymous.
	if got := c.send(stdhttp.MethodGet, "/api/v1/auth/me", nil, nil); got.status != stdhttp.StatusUnauthorized {
		t.Fatalf("anonymous /me status = %d, want 401", got.status)
	}

	// Wrong password.
	bad := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "budi", "password": "salah"}, nil)
	if bad.status != stdhttp.StatusUnauthorized {
		t.Fatalf("bad login status = %d, want 401", bad.status)
	}

	// Login.
	ok := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "budi", "password": "rahasia-panjang"}, nil)
	if ok.status != stdhttp.StatusOK {
		t.Fatalf("login status = %d, want 200 (%s)", ok.status, ok.body)
	}

	var cookieSeen bool
	for _, ck := range ok.header.Values("Set-Cookie") {
		if len(ck) > len(terahttp.SessionCookie) && ck[:len(terahttp.SessionCookie)] == terahttp.SessionCookie {
			cookieSeen = true
			if !containsFold(ck, "HttpOnly") {
				t.Error("session cookie is not HttpOnly")
			}
			if !containsFold(ck, "SameSite=Lax") {
				t.Error("session cookie is not SameSite=Lax")
			}
		}
	}
	if !cookieSeen {
		t.Fatal("login set no session cookie")
	}

	// The cookie carries.
	me := c.send(stdhttp.MethodGet, "/api/v1/auth/me", nil, nil)
	if me.status != stdhttp.StatusOK {
		t.Fatalf("/me status = %d, want 200 (%s)", me.status, me.body)
	}

	var decoded struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Roles map[string]string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(me.body), &decoded); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if decoded.User.Username != "budi" {
		t.Errorf("username = %q, want budi", decoded.User.Username)
	}
	if decoded.Roles[e.ID] != "manager" {
		t.Errorf("role for the entity = %q, want manager", decoded.Roles[e.ID])
	}

	// Logout ends it.
	if got := c.send(stdhttp.MethodPost, "/api/v1/auth/logout", nil, nil); got.status != stdhttp.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", got.status)
	}
	if got := c.send(stdhttp.MethodGet, "/api/v1/auth/me", nil, nil); got.status != stdhttp.StatusUnauthorized {
		t.Errorf("/me after logout = %d, want 401", got.status)
	}
}

// A password must never reach the logs or a response body.
func TestLoginResponseCarriesNoSecret(t *testing.T) {
	t.Parallel()

	c, auth, _, ctx := newClient(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("user: %v", err)
	}

	got := c.send(stdhttp.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "budi", "password": "rahasia-panjang"}, nil)

	if containsFold(got.body, "rahasia-panjang") {
		t.Error("the login response echoes the password")
	}
	if containsFold(got.body, "$2a$") || containsFold(got.body, "password_hash") {
		t.Error("the login response leaks the password hash")
	}
}

func containsFold(haystack, needle string) bool {
	return needle != "" && bytes.Contains(
		bytes.ToLower([]byte(haystack)), bytes.ToLower([]byte(needle)))
}
