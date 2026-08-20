package service

import "fmt"

// Role is what a user may do inside one company. R13.2.
//
// A user holds at most one role per entity and may hold different roles in
// each (R13.4), so a role is never meaningful on its own — always as
// (user, entity, role).
type Role string

const (
	// RoleOwner is a family member. Sees margin, edits history, manages users.
	RoleOwner Role = "owner"
	// RoleManager runs the shop day to day: purchasing, master data, corrections.
	RoleManager Role = "manager"
	// RoleStaff is the cashier. Rings sales; does not reach backwards.
	RoleStaff Role = "staff"
)

// ErrUnknownRole is returned for a role string outside the three.
var ErrUnknownRole = fmt.Errorf("service: unknown role")

// ParseRole converts a stored role string, rejecting anything unrecognised
// rather than defaulting. A role that silently degrades to staff would lock an
// owner out of their own figures; one that silently upgrades is worse.
func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleOwner, RoleManager, RoleStaff:
		return Role(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRole, s)
	}
}

// Valid reports whether r is one of the three roles.
func (r Role) Valid() bool {
	_, err := ParseRole(string(r))
	return err == nil
}

// rank orders the roles for AtLeast. Not exported: the capability predicates
// below are the intended interface, because "manager or above" is a detail
// that should be stated once, next to the requirement it encodes.
func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleManager:
		return 2
	case RoleStaff:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether r carries at least the authority of required.
func (r Role) AtLeast(required Role) bool {
	return r.Valid() && required.Valid() && r.rank() >= required.rank()
}

// CanEditHistory reports whether the role may edit past transactions.
//
// R7.1: permitted for owners and managers, regular staff excluded. Every such
// edit is logged with actor, timestamp, and before/after (INV-10) — the user
// declined period locking, so that log is the entire mitigation.
func (r Role) CanEditHistory() bool { return r.AtLeast(RoleManager) }

// CanEnterPurchases reports whether the role may record purchases.
//
// R10.4: entered by admin or manager, not cashier staff. Purchases set the
// faktur status that decides a layer's cost basis (INV-9), so this is a
// margin-bearing screen, not a data-entry one.
func (r Role) CanEnterPurchases() bool { return r.AtLeast(RoleManager) }

// CanManageMasterData reports whether the role may edit products, suppliers,
// customers, and owner records (R11).
func (r Role) CanManageMasterData() bool { return r.AtLeast(RoleManager) }

// CanManageUsers reports whether the role may create users and grant roles.
func (r Role) CanManageUsers() bool { return r.AtLeast(RoleOwner) }

// CanSeeAllOwnerMargin reports whether the role may see every owner's margin,
// not just aggregate figures.
//
// R13.3 as written: staff "should not see other owners' margin figures", with
// the requirement itself noting this needs confirming with the user. This
// implements the requirement as written — the conservative reading — and the
// question is still open. In a family business it is sensitive in both
// directions, so it must be answered before the Phase 3 margin screen ships,
// not defaulted to silently.
func (r Role) CanSeeAllOwnerMargin() bool { return r.AtLeast(RoleManager) }
