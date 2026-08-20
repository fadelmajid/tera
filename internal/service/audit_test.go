package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

func newAuditor(t *testing.T) (*service.Auditor, *store.DB, *gen.Queries, context.Context) {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	at := time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC)
	return service.NewAuditor(func() time.Time { return at }), db, gen.New(db), ctx
}

func TestRecordCapturesWhoWhenBeforeAfter(t *testing.T) {
	t.Parallel()

	aud, db, q, ctx := newAuditor(t)

	type product struct {
		Name  string `json:"name"`
		Price int64  `json:"sale_price_idr"`
	}

	err := db.InTx(ctx, func(tx *sql.Tx) error {
		return aud.Record(ctx, tx, &service.Entry{
			ActorUserID: "",
			RecordType:  "product",
			RecordID:    store.NewID(),
			Action:      service.ActionUpdate,
			Before:      product{Name: "Masker", Price: 25_000},
			After:       product{Name: "Masker", Price: 27_500},
		})
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	rows, err := q.ListAuditByPeriod(ctx, gen.ListAuditByPeriodParams{
		FromAt: 0, ToAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d audit rows, want 1", len(rows))
	}

	got := rows[0]
	if got.Action != "UPDATE" {
		t.Errorf("action = %q, want UPDATE", got.Action)
	}
	if got.OccurredAt != time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC).Unix() {
		t.Errorf("occurred_at = %d, want the injected clock", got.OccurredAt)
	}
	if got.BeforeJson == nil || got.AfterJson == nil {
		t.Fatal("before/after not recorded")
	}

	var before, after product
	if err := json.Unmarshal([]byte(*got.BeforeJson), &before); err != nil {
		t.Fatalf("before: %v", err)
	}
	if err := json.Unmarshal([]byte(*got.AfterJson), &after); err != nil {
		t.Fatalf("after: %v", err)
	}
	if before.Price != 25_000 || after.Price != 27_500 {
		t.Errorf("prices %d -> %d, want 25000 -> 27500", before.Price, after.Price)
	}
}

// The point of taking a transaction. A committed change with no trail is the
// hole INV-10 exists to close, and with period locking declined (R7.3) this log
// is the only mitigation there is.
func TestAuditRowCommitsWithTheChangeOrNotAtAll(t *testing.T) {
	t.Parallel()

	aud, db, q, ctx := newAuditor(t)

	owner := gen.CreateOwnerParams{ID: store.NewID(), Code: "BUDI", Name: "Budi", CreatedAt: 0, UpdatedAt: 0}
	sentinel := errors.New("something failed after the write")

	err := db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := gen.New(tx).CreateOwner(ctx, owner); err != nil {
			return err
		}
		if err := aud.Record(ctx, tx, &service.Entry{
			RecordType: "owner", RecordID: owner.ID,
			Action: service.ActionCreate, After: owner,
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the sentinel", err)
	}

	n, err := q.CountAuditLog(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("audit rows survived a rolled-back transaction: %d", n)
	}
	if _, err := q.GetOwner(ctx, owner.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Error("the owner survived the rollback; the two are not atomic")
	}

	// And the committing case writes both.
	err = db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := gen.New(tx).CreateOwner(ctx, owner); err != nil {
			return err
		}
		return aud.Record(ctx, tx, &service.Entry{
			RecordType: "owner", RecordID: owner.ID,
			Action: service.ActionCreate, After: owner,
		})
	})
	if err != nil {
		t.Fatalf("commit path: %v", err)
	}

	if n, err = q.CountAuditLog(ctx); err != nil || n != 1 {
		t.Errorf("audit rows = %d (err %v), want 1", n, err)
	}
	if _, err := q.GetOwner(ctx, owner.ID); err != nil {
		t.Errorf("owner missing after commit: %v", err)
	}
}

func TestEntryValidation(t *testing.T) {
	t.Parallel()

	aud, db, _, ctx := newAuditor(t)
	snapshot := map[string]any{"qty": 1}

	tests := []struct {
		name  string
		entry service.Entry
		want  error
	}{
		{
			name:  "unknown action",
			entry: service.Entry{RecordType: "sale", RecordID: "x", Action: "ARCHIVE", After: snapshot},
			want:  service.ErrUnknownAction,
		},
		{
			// R12.5: adjustments carry a reason code.
			name:  "adjust without a reason",
			entry: service.Entry{RecordType: "stock_layer", RecordID: "x", Action: service.ActionAdjust, Before: snapshot, After: snapshot},
			want:  service.ErrReasonRequired,
		},
		{
			// A void reverses a finalised transaction (INV-2). Unexplained is
			// exactly what this table exists to prevent.
			name:  "void without a reason",
			entry: service.Entry{RecordType: "sale", RecordID: "x", Action: service.ActionVoid, Before: snapshot, After: snapshot},
			want:  service.ErrReasonRequired,
		},
		{
			name:  "update with no before",
			entry: service.Entry{RecordType: "sale", RecordID: "x", Action: service.ActionUpdate, After: snapshot},
			want:  service.ErrAuditIncomplete,
		},
		{
			name:  "create with no after",
			entry: service.Entry{RecordType: "sale", RecordID: "x", Action: service.ActionCreate},
			want:  service.ErrAuditIncomplete,
		},
		{
			name:  "delete with no before",
			entry: service.Entry{RecordType: "sale", RecordID: "x", Action: service.ActionDelete},
			want:  service.ErrAuditIncomplete,
		},
		{
			name:  "no record identity",
			entry: service.Entry{Action: service.ActionCreate, After: snapshot},
			want:  service.ErrAuditIncomplete,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := db.InTx(ctx, func(tx *sql.Tx) error {
				return aud.Record(ctx, tx, &tc.entry)
			})
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// SPEC §6: queryable by period and by actor, or it is decoration.
func TestAuditIsQueryable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	q := gen.New(db)

	auth := service.NewAuthWithCost(db, time.Now, 4)
	budi, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	siti, err := auth.CreateUser(ctx, "siti", "Siti", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	october := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	november := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	recordID := store.NewID()

	write := func(at time.Time, actor string) {
		t.Helper()
		aud := service.NewAuditor(func() time.Time { return at })
		if err := db.InTx(ctx, func(tx *sql.Tx) error {
			return aud.Record(ctx, tx, &service.Entry{
				ActorUserID: actor, RecordType: "sale", RecordID: recordID,
				Action: service.ActionUpdate,
				Before: map[string]any{"total": 100_000},
				After:  map[string]any{"total": 120_000},
			})
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	write(october, budi.ID)
	write(november, budi.ID)
	write(november, siti.ID)

	// By period: October's edits, separate from November's.
	octRows, err := q.ListAuditByPeriod(ctx, gen.ListAuditByPeriodParams{
		FromAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(),
		ToAt:   time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Unix(),
	})
	if err != nil {
		t.Fatalf("by period: %v", err)
	}
	if len(octRows) != 1 {
		t.Errorf("October holds %d edits, want 1", len(octRows))
	}

	// By actor.
	budiRows, err := q.ListAuditByActor(ctx, gen.ListAuditByActorParams{
		ActorUserID: &budi.ID, FromAt: 0,
		ToAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
	})
	if err != nil {
		t.Fatalf("by actor: %v", err)
	}
	if len(budiRows) != 2 {
		t.Errorf("Budi made %d edits, want 2", len(budiRows))
	}

	// By record: the full history of one sale.
	recRows, err := q.ListAuditForRecord(ctx, gen.ListAuditForRecordParams{
		RecordType: "sale", RecordID: recordID,
	})
	if err != nil {
		t.Fatalf("by record: %v", err)
	}
	if len(recRows) != 3 {
		t.Errorf("the sale carries %d audit rows, want 3", len(recRows))
	}
}

// The trail outlives the account. A departed family member's edits must remain
// visible, which is why actor_user_id is ON DELETE SET NULL rather than CASCADE.
func TestTrailSurvivesActorDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	auth := service.NewAuthWithCost(db, time.Now, 4)
	u, err := auth.CreateUser(ctx, "budi", "Budi", "rahasia-panjang")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	aud := service.NewAuditor(time.Now)
	if err := db.InTx(ctx, func(tx *sql.Tx) error {
		return aud.Record(ctx, tx, &service.Entry{
			ActorUserID: u.ID, RecordType: "sale", RecordID: store.NewID(),
			Action: service.ActionVoid, Reason: "salah input",
			Before: map[string]any{"status": "final"},
			After:  map[string]any{"status": "void"},
		})
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM app_user WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	n, err := gen.New(db).CountAuditLog(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d after deleting the actor, want the trail to survive", n)
	}
}
