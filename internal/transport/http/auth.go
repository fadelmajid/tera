package http

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/service"
)

// SessionCookie is the cookie carrying the session token.
const SessionCookie = "tera_session"

// EntityHeader names the company a request applies to.
//
// Authorization is always per entity (R13.4), so a mutating request that does
// not say which company it means is ambiguous rather than defaultable — the
// same reasoning as INV-8 for stock: guessing moves consequences onto the
// wrong books.
const EntityHeader = "X-Entity-Id"

type ctxKey int

const (
	ctxPrincipal ctxKey = iota
	ctxEntityID
)

// PrincipalFrom returns the authenticated user on a request, if any.
func PrincipalFrom(ctx context.Context) (service.Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(service.Principal)
	return p, ok
}

// EntityFrom returns the entity a request applies to, if it named one.
func EntityFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxEntityID).(string)
	return id, ok && id != ""
}

// authenticate resolves the session cookie into a principal when one is
// present. It never rejects — requireAuth does that — so public routes and
// authenticated routes can share the chain.
func authenticate(auth *service.Auth) func(stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			if auth == nil {
				next.ServeHTTP(w, r)
				return
			}

			c, err := r.Cookie(SessionCookie)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}

			p, err := auth.Authenticate(r.Context(), c.Value)
			if err != nil {
				// An invalid or expired session gets its cookie cleared, so the
				// browser stops sending a token that will never work again.
				clearSessionCookie(w, false)
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), ctxPrincipal, p)
			if id := strings.TrimSpace(r.Header.Get(EntityHeader)); id != "" {
				ctx = context.WithValue(ctx, ctxEntityID, id)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireAuth rejects anonymous requests.
func requireAuth(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": "silakan masuk terlebih dahulu"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireEntityRole checks a capability within the entity the request names.
//
// It refuses a request that names no entity rather than picking one. A user
// may be an owner in one company and staff in the other (R13.4); defaulting
// would silently apply the wrong company's authority.
func requireEntityRole(capability func(service.Role) bool, reason string) func(stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": "silakan masuk terlebih dahulu"})
				return
			}

			entityID, ok := EntityFrom(r.Context())
			if !ok {
				writeJSON(w, stdhttp.StatusBadRequest, map[string]any{
					"error": "perusahaan tidak ditentukan pada permintaan ini",
				})
				return
			}

			if _, has := p.RoleIn(entityID); !has {
				writeJSON(w, stdhttp.StatusForbidden, map[string]any{
					"error": service.ErrNoAccessToEntity.Error(),
				})
				return
			}
			if !p.Can(entityID, capability) {
				writeJSON(w, stdhttp.StatusForbidden, map[string]any{"error": reason})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func handleLogin(auth *service.Auth, secureCookie bool) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req loginRequest
		if err := json.NewDecoder(stdhttp.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeJSON(w, stdhttp.StatusBadRequest, map[string]any{"error": "permintaan tidak valid"})
			return
		}

		token, p, err := auth.Login(r.Context(), req.Username, req.Password)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidCredentials):
				writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": err.Error()})
			case errors.Is(err, service.ErrUserInactive):
				writeJSON(w, stdhttp.StatusForbidden, map[string]any{"error": err.Error()})
			default:
				writeJSON(w, stdhttp.StatusInternalServerError, map[string]any{"error": "gagal masuk"})
			}
			return
		}

		// gosec flags Secure being a variable rather than a literal true. It is
		// deliberate: the shop LAN is plain HTTP with no certificate authority,
		// and a Secure cookie would simply never be sent, locking everyone out.
		// HttpOnly and SameSite=Lax are unconditional. See DECISIONS D-008.
		stdhttp.SetCookie(w, &stdhttp.Cookie{ //nolint:gosec // G124: see D-008
			Name:     SessionCookie,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: stdhttp.SameSiteLaxMode,
			Secure:   secureCookie,
			Expires:  time.Now().Add(service.SessionTTL),
		})
		writeJSON(w, stdhttp.StatusOK, principalBody(p))
	}
}

func handleLogout(auth *service.Auth, secureCookie bool) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil {
			if lerr := auth.Logout(r.Context(), c.Value); lerr != nil {
				writeJSON(w, stdhttp.StatusInternalServerError, map[string]any{"error": "gagal keluar"})
				return
			}
		}
		clearSessionCookie(w, secureCookie)
		w.WriteHeader(stdhttp.StatusNoContent)
	}
}

func handleMe() stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeJSON(w, stdhttp.StatusUnauthorized, map[string]any{"error": "silakan masuk terlebih dahulu"})
			return
		}
		writeJSON(w, stdhttp.StatusOK, principalBody(p))
	}
}

func principalBody(p service.Principal) map[string]any {
	roles := make(map[string]string, len(p.Roles))
	for entityID, role := range p.Roles {
		roles[entityID] = string(role)
	}
	return map[string]any{
		"user": map[string]any{
			"id":        p.UserID,
			"username":  p.Username,
			"full_name": p.FullName,
		},
		"roles": roles,
	}
}

func clearSessionCookie(w stdhttp.ResponseWriter, secure bool) {
	stdhttp.SetCookie(w, &stdhttp.Cookie{ //nolint:gosec // G124: see D-008
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: stdhttp.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
	})
}
