package service_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// TestARestoreBringsBackTheNumbersThatWereThere is TASKS 8.2's automatable half.
//
// One machine holds the figures this family settles money on (R2.4), and R8.7
// accepts that machine dying on the condition that a restore works. This proves
// the mechanism on data that was traded through the real paths: a purchase with
// its FIFO layers, a sale that consumed them, and the tax and omzet rows that
// went with it.
//
// What it cannot prove is that a person who is not the developer can do it on a
// different laptop under pressure. That is docs/RESTORE-DRILL.md, and it is not
// passed by reading it.
func TestARestoreBringsBackTheNumbersThatWereThere(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)

	// What the shop is worth, before anything is backed up.
	before := figures(ctx, t, w.q)
	if before.layers == 0 || before.sales == 0 {
		t.Fatalf("the fixture traded nothing: %+v", before)
	}

	dir := filepath.Join(t.TempDir(), "flashdisk")
	snapshot, err := w.db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: fixedNow, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// A different machine, as far as this process can arrange one: a directory
	// that has never held a database, with nothing in it but the backup.
	fresh := filepath.Join(t.TempDir(), "laptop-cadangan", "tera.db")
	if err := os.MkdirAll(filepath.Dir(fresh), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	restored, err := service.Restore(ctx, snapshot.Path, fresh, fixedNow)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.SchemaVersion != snapshot.SchemaVersion {
		t.Errorf("restored at schema %d, backed up at %d",
			restored.SchemaVersion, snapshot.SchemaVersion)
	}

	db, err := store.Open(ctx, fresh)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = db.Close() }()

	after := figures(ctx, t, gen.New(db))
	if after != before {
		t.Errorf("the restored database holds %+v, the original held %+v", after, before)
	}
}

// shopFigures is what has to survive a restore: not row counts for their own
// sake, but the money and the stock.
type shopFigures struct {
	layers    int64
	sales     int64
	saleTotal int64
	cogs      int64
	onHand    int64
	omzetRows int64
	taxRows   int64
}

func figures(ctx context.Context, t *testing.T, q *gen.Queries) shopFigures {
	t.Helper()

	sales, err := q.ListSales(ctx, gen.ListSalesParams{
		EntityID: entityOf(ctx, t, q), FromDate: "0000-01-01", ToDate: "9999-12-31",
	})
	if err != nil {
		t.Fatalf("sales: %v", err)
	}

	out := shopFigures{sales: int64(len(sales))}
	for _, s := range sales {
		out.saleTotal += s.TotalIdr
		out.cogs += s.CogsIdr
	}

	layers, err := q.ListStockOnHandByOwner(ctx, entityOf(ctx, t, q))
	if err != nil {
		t.Fatalf("stock: %v", err)
	}
	out.layers = int64(len(layers))
	for _, l := range layers {
		out.onHand += l.QtyOnHand
	}

	omzet, err := q.ListOmzetBookYears(ctx, entityOf(ctx, t, q))
	if err != nil {
		t.Fatalf("omzet: %v", err)
	}
	out.omzetRows = int64(len(omzet))

	for _, s := range sales {
		rows, err := q.ListSaleTax(ctx, s.ID)
		if err != nil {
			t.Fatalf("sale tax: %v", err)
		}
		out.taxRows += int64(len(rows))
	}
	return out
}

func entityOf(ctx context.Context, t *testing.T, q *gen.Queries) string {
	t.Helper()

	rows, err := q.ListLegalEntities(ctx)
	if err != nil || len(rows) == 0 {
		t.Fatalf("entities: %v (%d)", err, len(rows))
	}
	return rows[0].ID
}

// TestARestoreRefusesABackupItCannotVouchFor, before it overwrites anything.
//
// A restore is run by somebody having a bad day, from a stick they hope is the
// right one. Every check happens before anything is overwritten.
func TestARestoreRefusesABackupItCannotVouchFor(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	dir := filepath.Join(t.TempDir(), "flashdisk")
	snapshot, err := w.db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: fixedNow, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	target := filepath.Join(t.TempDir(), "tera.db")
	if err := os.WriteFile(target, []byte("basis data yang masih ada"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Corrupt the snapshot after its manifest was written, the way a stick
	// pulled mid-copy does.
	if err := os.Truncate(snapshot.Path, snapshot.SizeBytes/2); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	if _, err := service.Restore(ctx, snapshot.Path, target, fixedNow); err == nil {
		t.Fatal("a corrupted backup was restored")
	}

	// And the file that was there is still there, untouched. Restoring a bad
	// backup over a database that was merely damaged is the worst outcome
	// available here.
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Error("the existing database was overwritten by a failed restore")
	}
}

// TestARestoreKeepsWhatItReplaced. The same reasoning: the operator may be
// restoring the wrong backup, and finding that out must not be terminal.
func TestARestoreKeepsWhatItReplaced(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	dir := filepath.Join(t.TempDir(), "flashdisk")
	snapshot, err := w.db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: fixedNow, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	target := filepath.Join(t.TempDir(), "tera.db")
	if err := os.WriteFile(target, []byte("yang lama"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := service.Restore(ctx, snapshot.Path, target, fixedNow); err != nil {
		t.Fatalf("restore: %v", err)
	}

	aside, err := filepath.Glob(target + ".sebelum-restore-*")
	if err != nil || len(aside) != 1 {
		t.Fatalf("the replaced database was not kept: %v (%d)", err, len(aside))
	}
	body, err := os.ReadFile(aside[0])
	if err != nil {
		t.Fatalf("read aside: %v", err)
	}
	if string(body) != "yang lama" {
		t.Errorf("the kept copy is %q", body)
	}
}

// TestTheSchedulerReportsAStaleDestination rather than failing quietly.
//
// A destination that has gone away — an unplugged stick, a share that stopped
// mounting — fails one snapshot at a time and does it quietly. Everything about
// R8.7 assumes this system is not sitting in that state.
func TestTheSchedulerReportsAStaleDestination(t *testing.T) {
	t.Parallel()

	w, ctx := tradedWorld(t)
	dir := filepath.Join(t.TempDir(), "flashdisk")

	clock := fixedNow
	backups := service.NewBackups(w.db, service.BackupConfig{
		Dir: dir, Interval: time.Hour, Keep: 3, Version: "test",
		Now: func() time.Time { return clock },
	})

	// Nothing taken yet: stale, because a destination with no backup in it is
	// indistinguishable from one that never worked.
	if got := backups.Status(); !got.Enabled || !got.Stale || got.Count != 0 {
		t.Errorf("empty destination: %+v", got)
	}

	if _, err := w.db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: clock, AllowSameDevice: true}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if got := backups.Status(); got.Stale || got.Count != 1 || got.Latest == nil {
		t.Errorf("fresh destination: %+v", got)
	}

	// Two intervals later with nothing new.
	clock = clock.Add(3 * time.Hour)
	if got := backups.Status(); !got.Stale {
		t.Error("a destination three hours stale on an hourly schedule reads as healthy")
	}
}

// TestAnUnconfiguredDestinationIsNotSilent. The deployment has one machine and
// no second copy of anything; a shop running without backups should never be
// able to say nobody told them.
func TestAnUnconfiguredDestinationIsNotSilent(t *testing.T) {
	t.Parallel()

	w, _ := tradedWorld(t)
	backups := service.NewBackups(w.db, service.BackupConfig{Version: "test"})

	if backups.Enabled() {
		t.Error("a blank destination reads as configured")
	}
	if got := backups.Status(); got.Enabled || got.Stale {
		t.Errorf("status = %+v", got)
	}
	if _, err := backups.Now(context.Background()); err == nil {
		t.Error("a backup was taken with nowhere to put it")
	}
}
