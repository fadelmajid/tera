package service_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/service"
)

func TestParseRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    service.Role
		wantErr bool
	}{
		{in: "owner", want: service.RoleOwner},
		{in: "manager", want: service.RoleManager},
		{in: "staff", want: service.RoleStaff},
		{in: "Owner", wantErr: true}, // stored values are lowercase; no coercion
		{in: "admin", wantErr: true},
		{in: "", wantErr: true},
		{in: "superuser", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := service.ParseRole(tc.in)
			if tc.wantErr {
				if !errors.Is(err, service.ErrUnknownRole) {
					t.Fatalf("ParseRole(%q) error = %v, want ErrUnknownRole", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// R7.1: edits to past transactions are for owners and managers; regular staff
// are excluded. This is the capability the audit log exists to police
// (INV-10), so getting it wrong is not a cosmetic error.
func TestCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role                                                      service.Role
		editHistory, purchases, masterData, users, allOwnerMargin bool
	}{
		{service.RoleOwner, true, true, true, true, true},
		{service.RoleManager, true, true, true, false, true},
		{service.RoleStaff, false, false, false, false, false},
		{service.Role("nonsense"), false, false, false, false, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.role), func(t *testing.T) {
			t.Parallel()

			checks := []struct {
				name string
				got  bool
				want bool
			}{
				{"CanEditHistory", tc.role.CanEditHistory(), tc.editHistory},
				{"CanEnterPurchases", tc.role.CanEnterPurchases(), tc.purchases},
				{"CanManageMasterData", tc.role.CanManageMasterData(), tc.masterData},
				{"CanManageUsers", tc.role.CanManageUsers(), tc.users},
				{"CanSeeAllOwnerMargin", tc.role.CanSeeAllOwnerMargin(), tc.allOwnerMargin},
			}
			for _, c := range checks {
				if c.got != c.want {
					t.Errorf("%s() = %v, want %v", c.name, c.got, c.want)
				}
			}
		})
	}
}

func TestAtLeastRejectsUnknownRoles(t *testing.T) {
	t.Parallel()

	if service.Role("").AtLeast(service.RoleStaff) {
		t.Error("an empty role satisfied the lowest requirement")
	}
	if service.RoleStaff.AtLeast(service.Role("nonsense")) {
		t.Error("a nonsense minimum was satisfied")
	}
}

// R13.4: a user may hold different roles in each company. Every authorization
// question is asked per entity, so a manager of one company cannot reach the
// other's history.
func TestPrincipalCanIsScopedPerEntity(t *testing.T) {
	t.Parallel()

	const pkp, nonPKP = "entity-pkp", "entity-non-pkp"

	p := service.Principal{
		UserID: "u1", Username: "budi",
		Roles: map[string]service.Role{
			pkp:    service.RoleManager,
			nonPKP: service.RoleStaff,
		},
	}

	if !p.Can(pkp, service.Role.CanEditHistory) {
		t.Error("manager of the PKP entity cannot edit its history")
	}
	if p.Can(nonPKP, service.Role.CanEditHistory) {
		t.Error("staff of the non-PKP entity was allowed to edit its history")
	}
	if p.Can("some-third-entity", service.Role.CanEditHistory) {
		t.Error("granted authority in an entity the user holds no role in")
	}

	if _, ok := p.RoleIn("some-third-entity"); ok {
		t.Error("reported a role in an entity the user has no access to")
	}
}
