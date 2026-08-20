package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fadelmajid/tera/internal/store"
)

func openTemp(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return db
}

// A misspelled DSN parameter is ignored silently by the driver. These assert
// the settings actually took, rather than that we asked for them.
func TestOpenAppliesPragmas(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	ctx := context.Background()

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"synchronous", "1"}, // NORMAL
	}

	for _, tc := range tests {
		t.Run(tc.pragma, func(t *testing.T) {
			var got string
			if err := db.QueryRowContext(ctx, "PRAGMA "+tc.pragma).Scan(&got); err != nil {
				t.Fatalf("read %s: %v", tc.pragma, err)
			}
			if !equalFold(got, tc.want) {
				t.Errorf("PRAGMA %s = %q, want %q", tc.pragma, got, tc.want)
			}
		})
	}
}

// Foreign keys being enforced is the difference between a stock layer that
// points at a real product and one that points at nothing.
func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	ctx := context.Background()

	stmts := []string{
		`CREATE TABLE parent (id TEXT PRIMARY KEY)`,
		`CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent(id))`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	_, err := db.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES ('c1', 'nonexistent')`)
	if err == nil {
		t.Fatal("inserted a child row referencing a parent that does not exist; foreign keys are not enforced")
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := store.Open(context.Background(), "  "); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

// There are no migrations to apply yet — the first lands in TASKS 0.4, and the
// idempotency test lands with it. What is worth asserting now is that the goose
// wiring works at all: the dialect resolves and the version table is reachable.
func TestVersionOnFreshDatabase(t *testing.T) {
	t.Parallel()

	db := openTemp(t)

	v, err := db.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != 0 {
		t.Errorf("fresh database reports schema version %d, want 0", v)
	}
}

// A partial commit corrupts stock and margin at the same time
// (ARCHITECTURE §4), so the rollback path matters as much as the commit path.
func TestInTx(t *testing.T) {
	t.Parallel()

	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	t.Run("commits on success", func(t *testing.T) {
		err := db.InTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES ('a')`)
			return err
		})
		if err != nil {
			t.Fatalf("InTx: %v", err)
		}
		if got := count(); got != 1 {
			t.Errorf("got %d rows, want 1", got)
		}
	})

	t.Run("rolls back on error", func(t *testing.T) {
		sentinel := errors.New("boom")

		err := db.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES ('b')`); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("got %v, want %v", err, sentinel)
		}
		if got := count(); got != 1 {
			t.Errorf("got %d rows, want 1 — the failed insert was not rolled back", got)
		}
	})

	t.Run("rolls back on panic", func(t *testing.T) {
		defer func() {
			if p := recover(); p == nil {
				t.Error("expected the panic to propagate")
			}
			if got := count(); got != 1 {
				t.Errorf("got %d rows, want 1 — a panic mid-transaction committed", got)
			}
		}()

		_ = db.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES ('c')`); err != nil {
				return err
			}
			panic("mid-transaction")
		})
	})
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
