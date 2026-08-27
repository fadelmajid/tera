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

// CanSeeAllOwnerMargin reports whether the role may see owner margin figures.
//
// R13.3, confirmed by the user (D-009): owners and managers can, regular staff
// cannot. A cashier is not one of the family members products are attributed
// to, so "other owners' figures" is all of them — staff see no margin at all,
// not a redacted version.
//
// This is an authority the query layer enforces, not the screen. A margin
// figure that reaches the browser has already left the building.
func (r Role) CanSeeAllOwnerMargin() bool { return r.AtLeast(RoleManager) }

// CanSeeTaxPosition reports whether the role may see the PPN position
// (SPEC §2.4).
//
// Manager or above, for the same reason as owner margin: it is a figure the
// business owes and plans around, not something a cashier needs at the till.
func (r Role) CanSeeTaxPosition() bool { return r.AtLeast(RoleManager) }

// CanManageTaxRules reports whether the role may change the tax configuration.
//
// Owner only, and the strictest gate in the system after user management. A
// tax_rule decides what every subsequent sale charges: set it wrong in one
// direction and the shop overcharges its customers, in the other it accrues a
// liability it is not collecting for (SPEC §2.3). It is also the one screen
// whose figures a konsultan pajak will be shown, and the person who answers to
// them should be the person who set them.
//
// Historical sales carry their own snapshot and never move when this changes
// (INV-3), so the blast radius is forward-looking — which is the only reason
// this is a settings screen at all rather than a migration.
func (r Role) CanManageTaxRules() bool { return r.AtLeast(RoleOwner) }

// CanSeeReports reports whether the role may read the sales, purchase, stock,
// hutang and piutang reports (TASKS 6.1–6.5).
//
// Manager or above. Every one of them carries a cost figure — what stock is
// worth, what a supplier was paid, which customers owe money — and a cashier
// needs none of it to ring a sale. The till's product grid is what a cashier
// uses to know whether something is in stock.
func (r Role) CanSeeReports() bool { return r.AtLeast(RoleManager) }

// CanExportEverything reports whether the role may download the whole database
// (R14.3, TASKS 6.6).
//
// Owner, and — because the archive is not entity-scoped and cannot be — owner
// in every active company rather than in the one selected. The transport layer
// enforces that half; this predicate is only the per-entity question.
//
// The export exists so the family can leave with their data. Anyone who can
// take the whole business out of the building should be someone entitled to
// the whole business.
func (r Role) CanExportEverything() bool { return r.AtLeast(RoleOwner) }
