package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// Action is what happened to a record.
type Action string

const (
	// ActionCreate records a row coming into existence.
	ActionCreate Action = "CREATE"
	// ActionUpdate records a change, carrying both sides of it.
	ActionUpdate Action = "UPDATE"
	// ActionDelete records a row being removed.
	ActionDelete Action = "DELETE"
	// ActionVoid records a finalised transaction being reversed (INV-2).
	ActionVoid Action = "VOID"
	// ActionAdjust records a stock adjustment (R12.5).
	ActionAdjust Action = "ADJUST"
)

var (
	// ErrUnknownAction is returned for an action outside the five.
	ErrUnknownAction = errors.New("service: unknown audit action")
	// ErrReasonRequired is returned when a correction carries no explanation.
	ErrReasonRequired = errors.New("service: alasan wajib diisi")
	// ErrAuditIncomplete is returned when an entry omits a snapshot the action
	// requires — an UPDATE with no before, a CREATE with no after.
	ErrAuditIncomplete = errors.New("service: catatan audit tidak lengkap")
)

// Entry is one line of the audit trail.
type Entry struct {
	ActorUserID     string
	LegalEntityID   string
	RecordType      string
	RecordID        string
	Action          Action
	Before          any
	After           any
	Reason          string
	ClientRequestID string
}

// Auditor writes the audit trail.
//
// Every method takes a transaction, deliberately. The audit row and the change
// it describes commit together or neither does — a committed change with no
// trail is exactly the hole INV-10 exists to close, and it is the only
// mitigation in place given the user declined period locking (R7.3).
type Auditor struct {
	now func() time.Time
}

// NewAuditor builds the auditor.
func NewAuditor(now func() time.Time) *Auditor {
	if now == nil {
		now = time.Now
	}
	return &Auditor{now: now}
}

// Record writes one audit row inside the caller's transaction.
func (a *Auditor) Record(ctx context.Context, tx *sql.Tx, e *Entry) error {
	if err := e.validate(); err != nil {
		return err
	}

	before, err := marshalSnapshot(e.Before)
	if err != nil {
		return fmt.Errorf("service: audit before: %w", err)
	}
	after, err := marshalSnapshot(e.After)
	if err != nil {
		return fmt.Errorf("service: audit after: %w", err)
	}

	err = gen.New(tx).WriteAuditLog(ctx, gen.WriteAuditLogParams{
		ID:              store.NewID(),
		ActorUserID:     nilIfEmpty(e.ActorUserID),
		OccurredAt:      a.now().Unix(),
		LegalEntityID:   nilIfEmpty(e.LegalEntityID),
		RecordType:      e.RecordType,
		RecordID:        e.RecordID,
		Action:          string(e.Action),
		BeforeJson:      before,
		AfterJson:       after,
		Reason:          nilIfEmpty(e.Reason),
		ClientRequestID: nilIfEmpty(e.ClientRequestID),
	})
	if err != nil {
		return fmt.Errorf("service: write audit log: %w", err)
	}
	return nil
}

// validate refuses an entry that would not answer the question the log exists
// to answer: who changed what, from what, to what.
func (e *Entry) validate() error {
	switch e.Action {
	case ActionCreate, ActionUpdate, ActionDelete, ActionVoid, ActionAdjust:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}

	if e.RecordType == "" || e.RecordID == "" {
		return fmt.Errorf("%w: record_type dan record_id wajib", ErrAuditIncomplete)
	}

	// A correction that nobody can explain later is the thing this table exists
	// to prevent. R12.5 requires it for adjustments; a void reverses a
	// finalised transaction (INV-2), which deserves the same.
	if (e.Action == ActionAdjust || e.Action == ActionVoid) && e.Reason == "" {
		return fmt.Errorf("%w: %s", ErrReasonRequired, e.Action)
	}

	switch e.Action {
	case ActionCreate:
		if e.After == nil {
			return fmt.Errorf("%w: CREATE tanpa nilai sesudah", ErrAuditIncomplete)
		}
	case ActionDelete:
		if e.Before == nil {
			return fmt.Errorf("%w: DELETE tanpa nilai sebelum", ErrAuditIncomplete)
		}
	case ActionUpdate, ActionVoid, ActionAdjust:
		// "before, after" is the requirement's own wording (INV-10). A row
		// saying something changed without saying from what is not a trail.
		if e.Before == nil || e.After == nil {
			return fmt.Errorf("%w: %s butuh nilai sebelum dan sesudah", ErrAuditIncomplete, e.Action)
		}
	}

	return nil
}

func marshalSnapshot(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
