package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/fadelmajid/tera/internal/service"
)

// requireEntityRole is not mounted on a production route until the master-data
// screens (TASKS 0.10), but it is the gate every mutating handler will sit
// behind, so it is tested now rather than trusted later.
func TestRequireEntityRole(t *testing.T) {
	t.Parallel()

	const pkp, nonPKP, unknown = "entity-pkp", "entity-non-pkp", "entity-other"

	principal := service.Principal{
		UserID: "u1", Username: "budi",
		Roles: map[string]service.Role{
			pkp:    service.RoleManager,
			nonPKP: service.RoleStaff,
		},
	}

	tests := []struct {
		name          string
		authenticated bool
		entityHeader  string
		wantStatus    int
	}{
		{"anonymous", false, pkp, stdhttp.StatusUnauthorized},
		{"manager in the named entity", true, pkp, stdhttp.StatusOK},
		{"staff in the named entity", true, nonPKP, stdhttp.StatusForbidden},
		{"no role in the named entity", true, unknown, stdhttp.StatusForbidden},
		// A request that does not say which company it means is ambiguous, not
		// defaultable — the same reasoning as INV-8 for stock ownership.
		{"no entity named", true, "", stdhttp.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			guarded := requireEntityRole(service.Role.CanEditHistory, "tidak boleh mengubah transaksi lampau")(
				stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
					w.WriteHeader(stdhttp.StatusOK)
				}))

			req := httptest.NewRequestWithContext(context.Background(),
				stdhttp.MethodPost, "/api/v1/anything", stdhttp.NoBody)
			ctx := req.Context()
			if tc.authenticated {
				ctx = context.WithValue(ctx, ctxPrincipal, principal)
			}
			if tc.entityHeader != "" {
				ctx = context.WithValue(ctx, ctxEntityID, tc.entityHeader)
			}

			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// A manager of one company must not inherit authority over the other. This is
// the mistake per-entity scoping exists to prevent (R13.4).
func TestEntityScopingDoesNotLeakAcrossCompanies(t *testing.T) {
	t.Parallel()

	const pkp, nonPKP = "entity-pkp", "entity-non-pkp"

	principal := service.Principal{
		UserID: "u1",
		Roles: map[string]service.Role{
			pkp:    service.RoleOwner,
			nonPKP: service.RoleStaff,
		},
	}

	guarded := requireEntityRole(service.Role.CanEnterPurchases, "tidak boleh membuat pembelian")(
		stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			w.WriteHeader(stdhttp.StatusOK)
		}))

	for _, tc := range []struct {
		entity string
		want   int
	}{
		{pkp, stdhttp.StatusOK},
		{nonPKP, stdhttp.StatusForbidden},
	} {
		req := httptest.NewRequestWithContext(context.Background(),
			stdhttp.MethodPost, "/api/v1/purchases", stdhttp.NoBody)
		ctx := context.WithValue(req.Context(), ctxPrincipal, principal)
		ctx = context.WithValue(ctx, ctxEntityID, tc.entity)

		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != tc.want {
			t.Errorf("entity %s: status = %d, want %d", tc.entity, rec.Code, tc.want)
		}
	}
}
