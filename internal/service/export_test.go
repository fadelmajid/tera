package service

import (
	"time"

	"github.com/fadelmajid/tera/internal/store"
)

// NewAuthWithCost builds an Auth with a lowered password hashing cost so the
// suite does not spend most of its time deliberately being slow. Per instance,
// not a package variable — parallel tests must not share it.
func NewAuthWithCost(db *store.DB, now func() time.Time, cost int) *Auth {
	a := NewAuth(db, now)
	a.cost = cost
	return a
}
