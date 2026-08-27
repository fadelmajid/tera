package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/store"
)

var backupClock = time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC)

// elsewhere is a directory that is not the database's own, so the same-device
// check has something to distinguish. On a single-disk test machine it is still
// the same device, which is why these tests use it only where that is the
// behaviour under test.
func elsewhere(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cadangan")
}

// TestASnapshotIsVerifiedBeforeItIsCalledABackup is R14.1's real requirement.
//
// "Automated local backup, scheduled, with a restore path that has been tested."
// The restore path is a drill; this is the half a machine can check every hour
// — that the file written is a database that opens, passes integrity_check, and
// reports the schema version it was taken at. A backup nobody has opened is a
// rumour.
func TestASnapshotIsVerifiedBeforeItIsCalledABackup(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)
	f.layer(ctx, t, q, &f.budi, 1_800_000_000, 7, 100_000, 1)

	dir := elsewhere(t)
	got, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: backupClock, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if _, err := os.Stat(got.Path); err != nil {
		t.Fatalf("the snapshot is not on disk: %v", err)
	}
	if _, err := os.Stat(got.Manifest); err != nil {
		t.Fatalf("no manifest beside it: %v", err)
	}
	if got.SizeBytes == 0 {
		t.Error("the snapshot is empty")
	}
	if len(got.SHA256) != 64 {
		t.Errorf("checksum = %q, want a sha256", got.SHA256)
	}
	// The schema version was read back out of the file, not copied from the
	// source: that is what makes it evidence rather than an assertion.
	source, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if got.SchemaVersion != source {
		t.Errorf("snapshot is at schema %d, database at %d", got.SchemaVersion, source)
	}

	if err := got.Verify(ctx); err != nil {
		t.Errorf("the snapshot does not verify: %v", err)
	}
}

// TestASnapshotCarriesTheRowsThatWereThere. A file that opens and is empty
// would pass every check above and be worthless.
func TestASnapshotCarriesTheRowsThatWereThere(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)
	f.layer(ctx, t, q, &f.budi, 1_800_000_000, 7, 100_000, 1)

	before, err := db.CountRows(ctx, "stock_layer")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if before == 0 {
		t.Fatal("the fixture wrote nothing")
	}

	got, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: elsewhere(t), AppVersion: "test", Now: backupClock, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored, err := store.Open(ctx, got.Path)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = restored.Close() }()

	after, err := restored.CountRows(ctx, "stock_layer")
	if err != nil {
		t.Fatalf("count in snapshot: %v", err)
	}
	if after != before {
		t.Errorf("the snapshot holds %d layers, the database had %d", after, before)
	}
}

// TestABackupOnTheSameDiskIsRefused is R14.2, enforced rather than advised.
//
// "A backup on the same disk is not a backup." A copy beside the original
// survives a deleted file and nothing else — not the disk failing, not the
// machine being stolen. The whole reason the requirement is worded that way is
// that "we have backups" is a sentence people say about copies that would not
// have helped.
func TestABackupOnTheSameDiskIsRefused(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)

	beside := filepath.Dir(db.Path())
	_, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: beside, AppVersion: "test", Now: backupClock})
	if err == nil {
		t.Fatal("a backup was written into the database's own folder")
	}
	if !strings.Contains(err.Error(), "disk yang sama") &&
		!strings.Contains(err.Error(), "folder yang sama") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}

// TestACorruptedBackupFailsVerification is the failure this whole design
// exists to catch early.
//
// A truncated copy to a USB stick looks exactly like a good one until somebody
// opens it, and by then it is the only copy anybody has.
func TestACorruptedBackupFailsVerification(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	newStockFixtures(ctx, t, q)

	got, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: elsewhere(t), AppVersion: "test", Now: backupClock, AllowSameDevice: true})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := got.Verify(ctx); err != nil {
		t.Fatalf("a good snapshot did not verify: %v", err)
	}

	// Truncated in transit, the way a stick pulled mid-write does it.
	if err := os.Truncate(got.Path, got.SizeBytes/2); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := got.Verify(ctx); err == nil {
		t.Error("a half-written backup verified")
	}

	// And a file of the right length whose bytes are wrong: the checksum is
	// what catches this one, since it may still open.
	body := make([]byte, got.SizeBytes)
	if err := os.WriteFile(got.Path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := got.Verify(ctx); err == nil {
		t.Error("a backup of the right size full of zeroes verified")
	}
}

// TestPruneKeepsTheNewest. A destination that fills up stops receiving backups,
// and it does it silently.
func TestPruneKeepsTheNewest(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)
	dir := elsewhere(t)

	for i := range 5 {
		at := backupClock.Add(time.Duration(i) * time.Hour)
		if _, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: at, AllowSameDevice: true}); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
	}

	all, err := store.ListBackups(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("%d backups, want 5", len(all))
	}
	// Newest first, so the one a restore reaches for is at the top.
	if all[0].CreatedAt < all[len(all)-1].CreatedAt {
		t.Error("backups are not listed newest first")
	}

	removed, err := store.Prune(dir, 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("pruned %d, want 3", len(removed))
	}

	left, err := store.ListBackups(dir)
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("%d backups left, want 2", len(left))
	}
	if left[0].CreatedAt != all[0].CreatedAt {
		t.Error("prune removed the newest backup")
	}
	// The manifests go with them; an orphaned manifest would make a deleted
	// backup look present.
	for _, path := range removed {
		if _, err := os.Stat(strings.TrimSuffix(path, ".db") + ".json"); err == nil {
			t.Errorf("%s left its manifest behind", path)
		}
	}
}

// TestASnapshotNeverOverwritesAnother. Two backups a second apart is a clock
// problem; one overwriting the other is a data-loss problem.
func TestASnapshotNeverOverwritesAnother(t *testing.T) {
	t.Parallel()

	db, _, ctx := migrated(t)
	dir := elsewhere(t)

	if _, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: backupClock, AllowSameDevice: true}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := db.Snapshot(ctx, store.SnapshotOptions{Dir: dir, AppVersion: "test", Now: backupClock, AllowSameDevice: true}); err == nil {
		t.Error("a second snapshot at the same instant overwrote the first")
	}
}
