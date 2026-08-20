package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the binary stays one file
)

//go:embed migrations
var migrationsFS embed.FS

const migrationsDir = "migrations"

// DB is the handle to the single SQLite file that holds everything.
type DB struct {
	*sql.DB
	path string
}

// Open opens the database at path, applies the connection pragmas, and verifies
// they actually took effect.
//
// # Why one connection
//
// MaxOpenConns is 1. SQLite permits one writer at a time regardless, and
// serialising readers alongside it removes an entire class of SQLITE_BUSY bug
// for a cost this business cannot measure: under 1,000 transactions a day with
// fewer than ten users is roughly one write every thirty seconds, against a
// dataset where a year of history is a few hundred thousand rows
// (ARCHITECTURE §1). A cashier who sees "database is locked" stops trusting the
// till, and no amount of throughput buys that back.
//
// If a report ever grows slow enough to make a sale wait, the fix is a second
// read-only pool behind this same type — a change confined to this file.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store: database path is empty")
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	d := &DB{DB: db, path: path}
	if err := d.verifyPragmas(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

// dsn builds the connection string. Each pragma is deliberate:
//
//   - journal_mode(WAL) — readers never block on the writer, and a crash
//     mid-write leaves the file consistent. The backup story leans on this.
//   - busy_timeout(5000) — wait rather than fail when a lock is contended.
//   - foreign_keys(1) — SQLite disables enforcement by default. A stock layer
//     pointing at a deleted product is not a state worth allowing.
//   - synchronous(NORMAL) — the documented pairing with WAL. Durable across a
//     process crash, trading only the power-loss window, which the hourly
//     backup already covers.
//   - _txlock=immediate — take the write lock when the transaction begins. A
//     deferred transaction that reads and then writes can hit a SQLITE_BUSY
//     that busy_timeout will not retry, the upgrade deadlock. Every sale reads
//     FIFO layers before writing them (ARCHITECTURE §4), so that is exactly
//     the shape in play here.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// verifyPragmas reads the settings back.
//
// A misspelled DSN parameter is ignored silently by the driver, which would
// leave the database in rollback-journal mode with foreign keys unenforced —
// looking fine until the day it matters. Assert instead of assuming.
func (db *DB) verifyPragmas(ctx context.Context) error {
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("store: read journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, "wal") {
		return fmt.Errorf("store: journal_mode is %q, want wal", journal)
	}

	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: read foreign_keys: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign_keys is off")
	}
	return nil
}

// Path returns the database file location, for logs and the backup job.
func (db *DB) Path() string { return db.path }

// Migrate applies every pending migration. Forward-only; goose runs them in one
// transaction each and records what it applied.
func (db *DB) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db.DB, migrationsDir); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Version reports the current schema version.
func (db *DB) Version(ctx context.Context) (int64, error) {
	if err := goose.SetDialect("sqlite3"); err != nil {
		return 0, fmt.Errorf("store: goose dialect: %w", err)
	}
	v, err := goose.GetDBVersionContext(ctx, db.DB)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}

// InTx runs fn inside a single transaction, committing on success and rolling
// back on error or panic.
//
// This is the boundary the service layer builds on. A sale writes its header,
// lines, stock consumptions, tax snapshot, and omzet ledger row together or not
// at all — a partial commit corrupts stock and margin simultaneously
// (ARCHITECTURE §4). The same shape makes an inter-company transfer atomic,
// which is the bug Olsera has (SPEC §3.4).
func (db *DB) InTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p) // a panic mid-transaction must not commit half a sale
		}
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("store: rollback: %w", rbErr))
			}
		}
	}()

	// Assigns the named return deliberately: the deferred rollback above reads
	// err to decide whether to roll back. `:=` here would shadow it and every
	// failed transaction would silently commit.
	if err = fn(tx); err != nil { //nolint:gocritic // sloppyReassign is wrong; see above

		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
