package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// StaleClaimAfter is how long an unanswered claim is honoured before a retry
// may take it over.
//
// A claim is released when the request finishes, including on error. It is only
// left behind if the process died mid-request — so this bound exists solely for
// that case, and it should be long enough that a slow but living request is
// never overtaken. A sale writes a handful of rows to a local SQLite file; two
// minutes is several orders of magnitude of headroom.
const StaleClaimAfter = 2 * time.Minute

var (
	// ErrRequestInFlight means an identical request is still being processed.
	ErrRequestInFlight = errors.New("service: permintaan yang sama sedang diproses")
	// ErrRequestMismatch means the same client_request_id arrived with a
	// different body — a client bug, refused rather than papered over.
	ErrRequestMismatch = errors.New("service: client_request_id sudah dipakai untuk permintaan lain")
)

// Replay is a previously computed response, returned verbatim to a retry.
type Replay struct {
	StatusCode int
	Body       string
}

// Idempotency makes mutating calls safe to retry (INV-6).
type Idempotency struct {
	q   *gen.Queries
	now func() time.Time
}

// NewIdempotency builds the coordinator.
func NewIdempotency(db *store.DB, now func() time.Time) *Idempotency {
	if now == nil {
		now = time.Now
	}
	return &Idempotency{q: gen.New(db), now: now}
}

// HashBody is the request fingerprint stored alongside a claim.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Claim attempts to take ownership of a client_request_id.
//
// It returns:
//   - (nil, nil) — this caller owns the request and should execute it, then
//     call Complete or Release.
//   - (replay, nil) — the request was already answered; return that verbatim.
//   - (nil, ErrRequestInFlight) — a duplicate is executing right now.
//   - (nil, ErrRequestMismatch) — the id was used for a different body.
//
// The claim is inserted before the handler runs, not after. Checking first and
// inserting later leaves a window where two copies of the same retry both see
// "not found" and both ring the sale.
func (i *Idempotency) Claim(ctx context.Context, id, userID, method, path, bodyHash string) (*Replay, error) {
	var user *string
	if userID != "" {
		user = &userID
	}

	now := i.now()
	inserted, err := i.q.ClaimRequest(ctx, gen.ClaimRequestParams{
		ClientRequestID: id, UserID: user, Method: method,
		Path: path, RequestHash: bodyHash, CreatedAt: now.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("service: claim request: %w", err)
	}
	if inserted == 1 {
		return nil, nil
	}

	existing, err := i.q.GetRequest(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Claimed and released between the two statements. Treat as
			// in-flight: the caller retries, which is always safe.
			return nil, ErrRequestInFlight
		}
		return nil, fmt.Errorf("service: read request: %w", err)
	}

	if existing.RequestHash != bodyHash {
		return nil, ErrRequestMismatch
	}

	if existing.StatusCode == 0 {
		if now.Sub(time.Unix(existing.CreatedAt, 0)) < StaleClaimAfter {
			return nil, ErrRequestInFlight
		}
		// The previous attempt died without answering. Take the claim over.
		if err := i.q.ReleaseRequest(ctx, id); err != nil {
			return nil, fmt.Errorf("service: release stale claim: %w", err)
		}
		return i.Claim(ctx, id, userID, method, path, bodyHash)
	}

	return &Replay{StatusCode: int(existing.StatusCode), Body: existing.ResponseBody}, nil
}

// Complete records the response so a retry replays it.
func (i *Idempotency) Complete(ctx context.Context, id string, status int, body string) error {
	completed := i.now().Unix()
	err := i.q.CompleteRequest(ctx, gen.CompleteRequestParams{
		ClientRequestID: id, StatusCode: int64(status),
		ResponseBody: body, CompletedAt: &completed,
	})
	if err != nil {
		return fmt.Errorf("service: complete request: %w", err)
	}
	return nil
}

// Release drops a claim so the call can be retried cleanly.
//
// Used when a request fails in a way that left nothing behind. A failed sale
// that wrote nothing should be retryable immediately, not blocked for two
// minutes by its own claim.
func (i *Idempotency) Release(ctx context.Context, id string) error {
	if err := i.q.ReleaseRequest(ctx, id); err != nil {
		return fmt.Errorf("service: release request: %w", err)
	}
	return nil
}

// PurgeStaleClaims frees claims abandoned by a crash. Called at startup.
func (i *Idempotency) PurgeStaleClaims(ctx context.Context) error {
	cutoff := i.now().Add(-StaleClaimAfter).Unix()
	if err := i.q.ReleaseStaleClaims(ctx, cutoff); err != nil {
		return fmt.Errorf("service: purge stale claims: %w", err)
	}
	return nil
}
