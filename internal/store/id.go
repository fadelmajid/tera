package store

import (
	"strings"

	"github.com/google/uuid"
)

// NewID mints a primary key: UUIDv7, canonical lowercase (D-003).
//
// Version 7 rather than 4 because it is time-ordered. FIFO consumption breaks
// ties on acquired_at by id for determinism (SPEC §3.3), and two layers
// acquired in the same second is ordinary — one purchase writes several. A v4
// tiebreak is stable but arbitrary, so the layer created second could be
// consumed first; v7 resolves it to insertion order, which is what FIFO means.
//
// Lowercased explicitly. Mixed case in a TEXT primary key is a duplicate
// waiting to happen, and the id CHECK constraint on every table rejects it.
func NewID() string {
	return strings.ToLower(uuid.Must(uuid.NewV7()).String())
}
