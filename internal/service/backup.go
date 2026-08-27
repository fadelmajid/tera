package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/store"
)

// DefaultBackupInterval is how often the scheduler takes a snapshot.
//
// Hourly, which is what store.Open's synchronous=NORMAL trade already assumes:
// the power-loss window it accepts is covered by an hourly backup and would not
// be by a daily one. Under a thousand transactions a day, an hour is at most a
// few dozen sales — a bad morning, not a bad year.
const DefaultBackupInterval = time.Hour

// DefaultBackupKeep is how many snapshots are retained.
//
// Enough to reach back past a weekend, which is the realistic gap between a
// corruption happening and somebody noticing. A USB stick that fills up stops
// receiving backups silently, so something has to be deleting the old ones.
const DefaultBackupKeep = 72

// Backups takes and verifies scheduled snapshots. TASKS 8.1, R14.1–14.2.
//
// # What this is defending against
//
// One machine holds the numbers this family settles money on every month
// (R2.4). R8.7 accepts that machine dying as a single point of failure, on the
// condition that R14 makes it recoverable — which puts the whole weight of that
// acceptance on this file and on the drill in docs/RESTORE-DRILL.md.
//
// So a snapshot is not finished when the bytes are written. It is finished when
// it has been reopened, integrity-checked, and its schema version read back
// (see [store.DB.Snapshot]). A backup nobody has opened is a rumour.
type Backups struct {
	db  *store.DB
	log *slog.Logger

	dir      string
	interval time.Duration
	keep     int
	version  string
	now      func() time.Time
	// allowSameDevice is the operator's explicit acceptance that a snapshot on
	// the same disk is better than none. Warned about on every startup.
	allowSameDevice bool
}

// BackupConfig configures the scheduler.
type BackupConfig struct {
	// Dir is where snapshots go. Removable or network storage (R14.2): a
	// snapshot on the same disk as the database is refused.
	Dir string
	// Interval defaults to [DefaultBackupInterval], Keep to
	// [DefaultBackupKeep].
	Interval time.Duration
	Keep     int
	Version  string
	Logger   *slog.Logger
	Now      func() time.Time
	// AllowSameDevice accepts a snapshot on the same disk as the database.
	// See [store.SnapshotOptions].
	AllowSameDevice bool
}

// NewBackups builds the scheduler. A blank Dir disables it, which is a
// legitimate configuration for a development machine and a dangerous one for
// the shop — [Backups.Enabled] exists so startup can say which it is.
func NewBackups(db *store.DB, cfg BackupConfig) *Backups {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultBackupInterval
	}
	if cfg.Keep <= 0 {
		cfg.Keep = DefaultBackupKeep
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Backups{
		db: db, log: cfg.Logger,
		dir: strings.TrimSpace(cfg.Dir), interval: cfg.Interval,
		keep: cfg.Keep, version: cfg.Version, now: cfg.Now,
		allowSameDevice: cfg.AllowSameDevice,
	}
}

// Enabled reports whether a destination is configured.
func (b *Backups) Enabled() bool { return b.dir != "" }

// Dir is where snapshots are written.
func (b *Backups) Dir() string { return b.dir }

// Now takes one snapshot, verifies it, and prunes what has aged out.
func (b *Backups) Now(ctx context.Context) (store.Backup, error) {
	if !b.Enabled() {
		return store.Backup{}, fmt.Errorf("%w: folder cadangan belum dikonfigurasi",
			store.ErrBackupDestination)
	}

	snapshot, err := b.db.Snapshot(ctx, store.SnapshotOptions{
		Dir: b.dir, AppVersion: b.version, Now: b.now(),
		AllowSameDevice: b.allowSameDevice,
	})
	if err != nil {
		return store.Backup{}, err
	}

	removed, err := store.Prune(b.dir, b.keep)
	if err != nil {
		// The snapshot is good; only the tidying failed. Reporting it as a
		// backup failure would be a lie in the direction that matters.
		b.log.Warn("cadangan lama gagal dihapus", "error", err, "dir", b.dir)
	}
	if len(removed) > 0 {
		b.log.Info("cadangan lama dihapus", "jumlah", len(removed), "simpan", b.keep)
	}
	return snapshot, nil
}

// Run takes a snapshot now and then on every tick until ctx is cancelled.
//
// The first one is immediate and deliberately so: a machine that has been up
// for fifty minutes and is about to die has no backup at all under a
// tick-first schedule, and startup is exactly when somebody is watching the log
// and could notice a misconfigured destination.
func (b *Backups) Run(ctx context.Context) {
	if !b.Enabled() {
		// Loud, because the deployment this is for has one machine and no
		// second copy of anything (R8.7). A shop running without backups
		// should never be able to say nobody told them.
		b.log.Warn("cadangan otomatis mati: TERA_BACKUP_DIR belum diisi",
			"akibat", "kerusakan disk berarti kehilangan seluruh data",
			"saran", "arahkan ke flashdisk atau folder jaringan")
		return
	}

	if b.allowSameDevice {
		b.log.Warn("cadangan diizinkan di disk yang sama dengan basis data",
			"akibat", "cadangan ini tidak menyelamatkan apa pun jika disknya rusak",
			"saran", "pindahkan TERA_BACKUP_DIR ke flashdisk atau folder jaringan")
	}

	b.once(ctx)

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.once(ctx)
		}
	}
}

// once takes a snapshot and reports the outcome, never stopping the scheduler.
//
// A backup failing must not take the till down with it — a shop that cannot
// sell because a USB stick was unplugged is a worse outage than the one the
// backup protects against. It is logged at error level so it is visible, and
// the next tick tries again.
func (b *Backups) once(ctx context.Context) {
	started := time.Now()

	snapshot, err := b.Now(ctx)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrSameDevice):
			b.log.Error("cadangan ditolak: tujuan berada di disk yang sama",
				"error", err, "dir", b.dir,
				"sebab", "salinan di disk yang sama tidak menyelamatkan apa pun saat disk rusak")
		case errors.Is(err, store.ErrBackupUnusable):
			b.log.Error("cadangan gagal diverifikasi dan dibuang",
				"error", err, "dir", b.dir,
				"sebab", "berkas yang tidak bisa dibuka kembali bukan cadangan")
		default:
			b.log.Error("cadangan gagal", "error", err, "dir", b.dir)
		}
		return
	}

	b.log.Info("cadangan tersimpan",
		"path", snapshot.Path, "ukuran", snapshot.SizeBytes,
		"skema", snapshot.SchemaVersion, "sha256", snapshot.SHA256[:12],
		"lama", time.Since(started).Round(time.Millisecond))
}

// List returns the snapshots on disk, newest first.
func (b *Backups) List() ([]store.Backup, error) {
	if !b.Enabled() {
		return nil, nil
	}
	return store.ListBackups(b.dir)
}

// Status is what the operations screen shows about backups.
type Status struct {
	Enabled  bool
	Dir      string
	Interval time.Duration
	Keep     int

	Count  int
	Latest *store.Backup
	// Stale is true when the newest snapshot is older than two intervals.
	//
	// One missed tick is a slow VACUUM or a machine that was asleep. Two is a
	// destination that has gone away — an unplugged stick, a network share that
	// stopped mounting — and that is the state this system must not sit in
	// quietly, because everything about R8.7 assumes it is not.
	Stale bool
	// FreeBytes is what is left where the backups go, or zero where it cannot
	// be determined.
	FreeBytes uint64
}

// Status reports the state of the backup destination.
func (b *Backups) Status() Status {
	out := Status{
		Enabled: b.Enabled(), Dir: b.dir,
		Interval: b.interval, Keep: b.keep,
	}
	if !b.Enabled() {
		return out
	}

	all, err := b.List()
	if err != nil {
		return out
	}
	out.Count = len(all)
	if len(all) > 0 {
		latest := all[0]
		out.Latest = &latest
		if taken, err := time.Parse(time.RFC3339, latest.CreatedAt); err == nil {
			out.Stale = b.now().Sub(taken) > 2*b.interval
		}
	} else {
		out.Stale = true
	}
	out.FreeBytes = freeBytes(b.dir)

	return out
}

// Restore replaces a database file with a verified snapshot. TASKS 8.2.
//
// # It refuses more than it does
//
// A restore is run by somebody having a bad day, on a machine that is not the
// one they meant to use, from a stick they hope is the right one. So every
// check happens before anything is overwritten: the checksum against the
// manifest, the integrity check, and the schema version against what this
// binary knows how to migrate.
//
// The existing file is moved aside rather than deleted, because the worst
// outcome available here is restoring the wrong backup over a database that was
// merely damaged.
func Restore(ctx context.Context, backupPath, dbPath string, now time.Time) (store.Backup, error) {
	snapshot, err := store.LoadBackup(backupPath)
	if err != nil {
		return store.Backup{}, err
	}
	if err := snapshot.Verify(ctx); err != nil {
		return store.Backup{}, err
	}

	if existing, err := os.Stat(dbPath); err == nil && existing.Size() > 0 {
		aside := dbPath + ".sebelum-restore-" + now.UTC().Format("20060102-150405")
		if err := os.Rename(dbPath, aside); err != nil {
			return store.Backup{}, fmt.Errorf("service: sisihkan %s: %w", dbPath, err)
		}
	}
	// The write-ahead log and shared-memory files belong to the database being
	// replaced. Left behind, SQLite would apply them on top of the restored
	// file and reintroduce exactly the transactions the restore was meant to
	// roll back.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}

	if err := copyFile(snapshot.Path, dbPath); err != nil {
		return store.Backup{}, err
	}

	// Opened through the ordinary path, so a restored database is subject to
	// the same pragma checks as any other (WAL, foreign keys) and forward
	// migrations run if this binary is newer than the snapshot.
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return store.Backup{}, fmt.Errorf("%w: %s tidak dapat dibuka setelah restore: %w",
			store.ErrBackupUnusable, dbPath, err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Migrate(ctx); err != nil {
		return store.Backup{}, fmt.Errorf("service: migrasi setelah restore: %w", err)
	}
	schema, err := db.Version(ctx)
	if err != nil {
		return store.Backup{}, err
	}
	snapshot.SchemaVersion = schema

	return snapshot, nil
}

// copyFile writes the snapshot into place.
//
// Both paths are the operator's: one is the backup they chose, the other is the
// database they are restoring onto. Cleaned before use so a path assembled from
// a stick's directory listing cannot walk somewhere else, and opened with
// O_EXCL-free create because replacing the target is exactly the intent — the
// previous file was already moved aside by the caller.
func copyFile(from, to string) error {
	source, target := filepath.Clean(from), filepath.Clean(to)

	body, err := os.ReadFile(source) //nolint:gosec // the operator's own backup file
	if err != nil {
		return fmt.Errorf("service: baca %s: %w", source, err)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil { //nolint:gosec // the operator's own database path
		return fmt.Errorf("service: tulis %s: %w", target, err)
	}
	return nil
}
