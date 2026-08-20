package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// Session lifetime. Long enough to cover a shop day without a mid-shift
// re-login at the till, short enough that an unattended browser does not stay
// open indefinitely. Sliding: every authenticated request extends it.
const (
	SessionTTL  = 12 * time.Hour
	tokenBytes  = 32
	minPassword = 8
)

// defaultBcryptCost. At cost 12 a hash takes roughly a third of a second by
// design: right for a login screen, wrong for a test suite that logs in dozens
// of times. The cost therefore lives on the Auth instance rather than in a
// package variable — a package variable would be shared mutable state that
// parallel tests race on, which is a bug the knob itself would have introduced.
const defaultBcryptCost = 12

var (
	// ErrInvalidCredentials covers both a wrong password and an unknown user.
	// Deliberately one error: telling a caller which of the two it was hands
	// them a username oracle.
	ErrInvalidCredentials = errors.New("service: nama pengguna atau kata sandi salah")
	// ErrSessionInvalid covers an unknown, expired, or revoked session.
	ErrSessionInvalid = errors.New("service: sesi tidak berlaku")
	// ErrUserInactive is returned when a deactivated user tries to log in.
	ErrUserInactive = errors.New("service: pengguna tidak aktif")
	// ErrPasswordTooShort is returned when a new password is below the minimum.
	ErrPasswordTooShort = fmt.Errorf("service: kata sandi minimal %d karakter", minPassword)
	// ErrNoAccessToEntity is returned when a user holds no role in an entity.
	ErrNoAccessToEntity = errors.New("service: tidak punya akses ke perusahaan ini")
)

// Principal is an authenticated user together with the roles they hold, keyed
// by entity. A user may hold a different role in each company (R13.4), so
// authorization questions are always asked per entity.
type Principal struct {
	UserID   string
	Username string
	FullName string
	Roles    map[string]Role
}

// RoleIn returns the user's role in one entity.
func (p Principal) RoleIn(entityID string) (Role, bool) {
	r, ok := p.Roles[entityID]
	return r, ok
}

// Can applies a capability predicate within one entity.
//
// Every authorization decision goes through here, so none of them can forget
// which company they are asking about — the mistake that would let a manager
// of the non-PKP entity edit the PKP entity's history.
func (p Principal) Can(entityID string, capability func(Role) bool) bool {
	r, ok := p.RoleIn(entityID)
	return ok && capability(r)
}

// Auth issues and verifies sessions.
type Auth struct {
	db   *store.DB
	q    *gen.Queries
	now  func() time.Time
	cost int
}

// NewAuth builds the authenticator. now is injectable so session expiry is
// testable without sleeping.
func NewAuth(db *store.DB, now func() time.Time) *Auth {
	if now == nil {
		now = time.Now
	}
	return &Auth{db: db, q: gen.New(db), now: now, cost: defaultBcryptCost}
}

// CreateUser adds a user with a bcrypt-hashed password.
func (a *Auth) CreateUser(ctx context.Context, username, fullName, password string) (gen.AppUser, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return gen.AppUser{}, errors.New("service: nama pengguna kosong")
	}
	if len([]rune(password)) < minPassword {
		return gen.AppUser{}, ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.cost)
	if err != nil {
		return gen.AppUser{}, fmt.Errorf("service: hash password: %w", err)
	}

	now := a.now().Unix()
	u, err := a.q.CreateUser(ctx, gen.CreateUserParams{
		ID: store.NewID(), Username: username, FullName: fullName,
		PasswordHash: string(hash), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return gen.AppUser{}, fmt.Errorf("service: create user: %w", err)
	}
	return u, nil
}

// GrantRole gives a user a role in one entity, replacing any role they held there.
func (a *Auth) GrantRole(ctx context.Context, userID, entityID string, role Role) error {
	if !role.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	err := a.q.GrantRole(ctx, gen.GrantRoleParams{
		UserID: userID, EntityID: entityID,
		Role: string(role), CreatedAt: a.now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("service: grant role: %w", err)
	}
	return nil
}

// Login verifies credentials and issues a session token.
//
// The token is returned once, to be set as a cookie. Only its SHA-256 is
// stored, so a copy of the database file — and R14 says copies live on
// removable media — does not hand anyone a working session.
func (a *Auth) Login(ctx context.Context, username, password string) (string, Principal, error) {
	username = strings.ToLower(strings.TrimSpace(username))

	u, err := a.q.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Compare against a throwaway hash anyway. Returning early on an
			// unknown username makes login measurably faster for names that do
			// not exist, which is a username oracle.
			equaliseTiming(password)
			return "", Principal{}, ErrInvalidCredentials
		}
		return "", Principal{}, fmt.Errorf("service: lookup user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", Principal{}, ErrInvalidCredentials
	}
	if u.IsActive != 1 {
		return "", Principal{}, ErrUserInactive
	}

	token, hash, err := newToken()
	if err != nil {
		return "", Principal{}, err
	}

	now := a.now()
	err = a.q.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: hash, UserID: u.ID,
		CreatedAt: now.Unix(), LastSeenAt: now.Unix(),
		ExpiresAt: now.Add(SessionTTL).Unix(),
	})
	if err != nil {
		return "", Principal{}, fmt.Errorf("service: create session: %w", err)
	}

	p, err := a.principal(ctx, &u)
	if err != nil {
		return "", Principal{}, err
	}
	return token, p, nil
}

// Authenticate resolves a session token to a principal, sliding its expiry.
func (a *Auth) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrSessionInvalid
	}

	s, err := a.q.GetSession(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Principal{}, ErrSessionInvalid
		}
		return Principal{}, fmt.Errorf("service: lookup session: %w", err)
	}

	now := a.now()
	if now.Unix() >= s.ExpiresAt {
		// Expired sessions are removed on sight rather than swept on a timer.
		_ = a.q.DeleteSession(ctx, s.TokenHash)
		return Principal{}, ErrSessionInvalid
	}

	u, err := a.q.GetUser(ctx, s.UserID)
	if err != nil {
		return Principal{}, fmt.Errorf("service: lookup user: %w", err)
	}
	if u.IsActive != 1 {
		_ = a.q.DeleteSessionsForUser(ctx, u.ID)
		return Principal{}, ErrUserInactive
	}

	if err := a.q.TouchSession(ctx, gen.TouchSessionParams{
		TokenHash: s.TokenHash, LastSeenAt: now.Unix(),
	}); err != nil {
		return Principal{}, fmt.Errorf("service: touch session: %w", err)
	}

	return a.principal(ctx, &u)
}

// Logout revokes one session.
func (a *Auth) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := a.q.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("service: delete session: %w", err)
	}
	return nil
}

// PurgeExpiredSessions removes sessions that have lapsed.
func (a *Auth) PurgeExpiredSessions(ctx context.Context) error {
	if err := a.q.DeleteExpiredSessions(ctx, a.now().Unix()); err != nil {
		return fmt.Errorf("service: purge sessions: %w", err)
	}
	return nil
}

// CountUsers reports how many users exist, for first-run bootstrap.
func (a *Auth) CountUsers(ctx context.Context) (int64, error) {
	n, err := a.q.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("service: count users: %w", err)
	}
	return n, nil
}

func (a *Auth) principal(ctx context.Context, u *gen.AppUser) (Principal, error) {
	rows, err := a.q.ListRolesForUser(ctx, u.ID)
	if err != nil {
		return Principal{}, fmt.Errorf("service: list roles: %w", err)
	}

	roles := make(map[string]Role, len(rows))
	for _, row := range rows {
		r, perr := ParseRole(row.Role)
		if perr != nil {
			return Principal{}, perr
		}
		roles[row.EntityID] = r
	}

	return Principal{
		UserID: u.ID, Username: u.Username, FullName: u.FullName, Roles: roles,
	}, nil
}

// newToken mints a session token and returns it alongside the hash to store.
func newToken() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("service: generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// equaliseTiming burns roughly one bcrypt comparison so that an unknown
// username costs the same as a known one.
func equaliseTiming(password string) {
	// A fixed hash of an arbitrary value; the comparison always fails.
	const dummy = "$2a$12$C6UzMDM.H6dfI/f/IKcEe.jJqL0/rEYGZFrLPBNiEEyGVdEjr5.Kq"
	_ = bcrypt.CompareHashAndPassword([]byte(dummy), []byte(password))
}

// BootstrapAdmin creates the first user when the database has none, returning
// the generated password so the caller can display it once.
//
// The install story is "copy one binary, run it, everyone opens a URL"
// (ARCHITECTURE §1). That needs a way in on first run that does not involve a
// terminal or a default password shipped in the source — a fresh, random
// credential printed once is the smallest thing that works.
func (a *Auth) BootstrapAdmin(ctx context.Context) (username, password string, created bool, err error) {
	n, err := a.CountUsers(ctx)
	if err != nil {
		return "", "", false, err
	}
	if n > 0 {
		return "", "", false, nil
	}

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", "", false, fmt.Errorf("service: generate password: %w", err)
	}
	password = base64.RawURLEncoding.EncodeToString(buf)

	if _, err := a.CreateUser(ctx, "admin", "Administrator", password); err != nil {
		return "", "", false, err
	}
	return "admin", password, true, nil
}
