package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newAuth(t *testing.T) (*service.Auth, *gen.Queries, *clock, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	c := &clock{t: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)}
	return service.NewAuthWithCost(db, c.now, 4), gen.New(db), c, ctx
}

func makeEntity(ctx context.Context, t *testing.T, q *gen.Queries, code string, isPKP int64) string {
	t.Helper()

	e, err := q.CreateLegalEntity(ctx, gen.CreateLegalEntityParams{
		ID: store.NewID(), Code: code, Name: code, IsPkp: isPKP,
		Timezone: "Asia/Jakarta", BookYearStartMonth: 1, CreatedAt: 0, UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("create entity: %v", err)
	}
	return e.ID
}

func TestLoginAndAuthenticate(t *testing.T) {
	t.Parallel()

	auth, q, _, ctx := newAuth(t)
	entityID := makeEntity(ctx, t, q, "PKP", 1)

	u, err := auth.CreateUser(ctx, "Budi", "Budi Santoso", "rahasia-panjang")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.Username != "budi" {
		t.Errorf("username = %q, want it normalised to lowercase", u.Username)
	}
	if u.PasswordHash == "rahasia-panjang" {
		t.Fatal("password stored in plaintext")
	}

	if err := auth.GrantRole(ctx, u.ID, entityID, service.RoleOwner); err != nil {
		t.Fatalf("grant: %v", err)
	}

	token, p, err := auth.Login(ctx, "BUDI", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" {
		t.Fatal("login returned an empty token")
	}
	if got, ok := p.RoleIn(entityID); !ok || got != service.RoleOwner {
		t.Errorf("role = %q (present %v), want owner", got, ok)
	}

	got, err := auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("authenticated as %q, want %q", got.UserID, u.ID)
	}
}

// The token itself is never stored — only its SHA-256 — so a copy of the
// database file does not hand anyone a working session. R14 puts copies of
// that file on removable media, which is exactly why this matters.
func TestSessionTokenIsNotStored(t *testing.T) {
	t.Parallel()

	auth, q, _, ctx := newAuth(t)

	u, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if _, err := q.GetSession(ctx, token); err == nil {
		t.Fatal("the raw token is a key in the session table; it must be hashed")
	}

	// And the hash that is stored must still resolve the session.
	if _, err := auth.Authenticate(ctx, token); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	_ = u
}

func TestLoginFailures(t *testing.T) {
	t.Parallel()

	auth, _, _, ctx := newAuth(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tests := []struct {
		name     string
		user     string
		password string
		want     error
	}{
		{"wrong password", "budi", "salah-sekali", service.ErrInvalidCredentials},
		{"unknown user", "tidak-ada", "rahasia-panjang", service.ErrInvalidCredentials},
		{"empty password", "budi", "", service.ErrInvalidCredentials},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := auth.Login(ctx, tc.user, tc.password); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPasswordMinimumLength(t *testing.T) {
	t.Parallel()

	auth, _, _, ctx := newAuth(t)

	if _, err := auth.CreateUser(ctx, "budi", "Budi", "pendek"); !errors.Is(err, service.ErrPasswordTooShort) {
		t.Errorf("got %v, want ErrPasswordTooShort", err)
	}
}

func TestSessionExpires(t *testing.T) {
	t.Parallel()

	auth, _, clk, ctx := newAuth(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	clk.add(service.SessionTTL - time.Minute)
	if _, err := auth.Authenticate(ctx, token); err != nil {
		t.Fatalf("session rejected before its expiry: %v", err)
	}

	clk.add(2 * time.Minute)
	if _, err := auth.Authenticate(ctx, token); !errors.Is(err, service.ErrSessionInvalid) {
		t.Errorf("got %v, want ErrSessionInvalid after the TTL", err)
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	t.Parallel()

	auth, _, _, ctx := newAuth(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := auth.Logout(ctx, token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := auth.Authenticate(ctx, token); !errors.Is(err, service.ErrSessionInvalid) {
		t.Errorf("got %v, want the session to be gone", err)
	}
}

func TestDeactivatedUserCannotAuthenticate(t *testing.T) {
	t.Parallel()

	auth, q, _, ctx := newAuth(t)
	u, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := q.SetUserActive(ctx, gen.SetUserActiveParams{ID: u.ID, IsActive: 0, UpdatedAt: 0}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// An existing session must stop working immediately, not at its next expiry.
	if _, err := auth.Authenticate(ctx, token); !errors.Is(err, service.ErrUserInactive) {
		t.Errorf("got %v, want ErrUserInactive", err)
	}
	if _, _, err := auth.Login(ctx, "budi", "rahasia-panjang"); !errors.Is(err, service.ErrUserInactive) {
		t.Errorf("login got %v, want ErrUserInactive", err)
	}
}

// R13.4 through the whole stack: one user, two companies, two different roles.
func TestRolesAreIndependentPerEntity(t *testing.T) {
	t.Parallel()

	auth, q, _, ctx := newAuth(t)
	pkp := makeEntity(ctx, t, q, "PKP", 1)
	nonPKP := makeEntity(ctx, t, q, "NONPKP", 0)

	u, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := auth.GrantRole(ctx, u.ID, pkp, service.RoleManager); err != nil {
		t.Fatalf("grant pkp: %v", err)
	}
	if err := auth.GrantRole(ctx, u.ID, nonPKP, service.RoleStaff); err != nil {
		t.Fatalf("grant non-pkp: %v", err)
	}

	_, p, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if !p.Can(pkp, service.Role.CanEditHistory) {
		t.Error("manager of the PKP entity cannot edit its history")
	}
	if p.Can(nonPKP, service.Role.CanEditHistory) {
		t.Error("staff of the non-PKP entity was allowed to edit its history")
	}
}

func TestBootstrapAdminOnlyOnce(t *testing.T) {
	t.Parallel()

	auth, _, _, ctx := newAuth(t)

	username, password, created, err := auth.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created || username != "admin" || password == "" {
		t.Fatalf("bootstrap returned (%q, %q, %v)", username, password, created)
	}

	if _, _, err := auth.Login(ctx, "admin", password); err != nil {
		t.Fatalf("cannot log in with the bootstrap password: %v", err)
	}

	_, _, created, err = auth.BootstrapAdmin(ctx)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if created {
		t.Error("bootstrap created a second admin on a populated database")
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	t.Parallel()

	auth, _, clk, ctx := newAuth(t)
	if _, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := auth.Login(ctx, "budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	clk.add(SessionTTLPlus())
	if err := auth.PurgeExpiredSessions(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := auth.Authenticate(ctx, token); !errors.Is(err, service.ErrSessionInvalid) {
		t.Errorf("got %v, want the purged session to be gone", err)
	}
}

// SessionTTLPlus is a hair past the TTL, so a test can step over the boundary
// without restating the constant.
func SessionTTLPlus() time.Duration { return service.SessionTTL + time.Second }
