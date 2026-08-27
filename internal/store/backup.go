package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sentinel errors, for callers that need to branch on the kind of failure.
var (
	// ErrBackupDestination means the destination could not be used.
	ErrBackupDestination = errors.New("store: tujuan cadangan tidak dapat dipakai")

	// ErrSameDevice means the backup would land on the same disk as the
	// database it is backing up.
	//
	// R14.2, and it is refused rather than warned about. A copy on the same
	// disk survives a deleted file and nothing else — not the disk failing, not
	// the machine being stolen, not the shop flooding. The whole reason this
	// requirement exists is that "we have backups" is a sentence people say
	// about copies that would not have helped.
	ErrSameDevice = errors.New("store: cadangan berada di disk yang sama dengan basis data")

	// ErrBackupUnusable means the file written could not be opened and read
	// back as a database.
	ErrBackupUnusable = errors.New("store: berkas cadangan tidak dapat dibaca kembali")
)

// Backup is one snapshot on disk, and the manifest written beside it.
type Backup struct {
	// Path is the .db file. Manifest is the .json beside it.
	Path     string `json:"-"`
	Manifest string `json:"-"`

	CreatedAt string `json:"created_at"`
	// SchemaVersion is the goose migration the snapshot was taken at. A restore
	// onto an older binary would find migrations it does not have, so this is
	// checked before anything is overwritten.
	SchemaVersion int64 `json:"schema_version"`
	SizeBytes     int64 `json:"size_bytes"`
	// SHA256 is what makes a restore verifiable rather than hopeful. Without it
	// a truncated copy looks exactly like a good one until it is opened, and by
	// then it is the only copy anybody has.
	SHA256 string `json:"sha256"`
	// SourcePath is the database it came from, for a person reading the
	// manifest a year later on a laptop that is not the shop's.
	SourcePath string `json:"source_path"`
	Version    string `json:"app_version"`
}

// SnapshotOptions configures one snapshot.
type SnapshotOptions struct {
	// Dir is where the snapshot goes. Removable or network storage (R14.2).
	Dir string
	// AppVersion is stamped into the manifest, so a person reading it on a
	// laptop that is not the shop's knows which binary wrote it.
	AppVersion string
	Now        time.Time

	// AllowSameDevice writes the snapshot even where it would land on the same
	// disk as the database.
	//
	// Off by default and refused loudly, because R14.2 is right: a copy on the
	// same disk survives a deleted file and nothing else. But refusing outright
	// leaves a single-disk machine with no backup at all, which is worse than a
	// weak one — and the moment somebody most wants a snapshot is often the
	// moment before a risky change, on whatever disk is there.
	//
	// So it is an explicit choice that says what it is giving up, and every use
	// of it is logged as a warning rather than passing quietly.
	AllowSameDevice bool
}

// Snapshot writes a consistent copy of the database and verifies it.
//
// # VACUUM INTO, not a file copy
//
// The database runs in WAL mode with writers active. Copying the file with cp
// captures the main file without the write-ahead log, which is a database
// missing its most recent transactions — the sales rung in the last few minutes
// — and it looks perfectly valid. VACUUM INTO takes a consistent snapshot
// through SQLite itself, compacts it, and needs no downtime.
//
// # The verification is the point
//
// A backup nobody has opened is a rumour (R14.1). Every snapshot is reopened
// read-only, integrity-checked, and its schema version read back before this
// returns success, so a broken backup is an error on the day it is taken rather
// than a discovery on the day it is needed.
func (db *DB) Snapshot(ctx context.Context, opts SnapshotOptions) (Backup, error) {
	dir, appVersion, now := opts.Dir, opts.AppVersion, opts.Now

	if strings.TrimSpace(dir) == "" {
		return Backup{}, fmt.Errorf("%w: tujuan kosong", ErrBackupDestination)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Backup{}, fmt.Errorf("%w: %s: %w", ErrBackupDestination, dir, err)
	}
	if !opts.AllowSameDevice {
		if err := db.checkDestination(dir); err != nil {
			return Backup{}, err
		}
	}

	name := "tera-" + now.UTC().Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return Backup{}, fmt.Errorf("%w: %s sudah ada", ErrBackupDestination, path)
	}

	// SQLite refuses to VACUUM INTO an existing file, which is the behaviour
	// wanted: a backup must never quietly overwrite another one.
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return Backup{}, fmt.Errorf("store: snapshot ke %s: %w", path, err)
	}

	// Opened, checked and read back before it is called a backup — and before
	// it is fingerprinted, so the checksum describes the file as it will be
	// found later rather than as it was for one instant.
	schema, err := VerifyBackup(ctx, path)
	if err != nil {
		// A file that cannot be read back is worse than no file: it occupies
		// the slot a real backup would have had.
		_ = os.Remove(path)
		return Backup{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return Backup{}, fmt.Errorf("store: stat %s: %w", path, err)
	}
	sum, err := checksum(path)
	if err != nil {
		return Backup{}, err
	}

	out := Backup{
		Path:      path,
		Manifest:  strings.TrimSuffix(path, ".db") + ".json",
		CreatedAt: now.UTC().Format(time.RFC3339),
		SizeBytes: info.Size(), SHA256: sum,
		SourcePath: db.Path(), Version: appVersion,
		SchemaVersion: schema,
	}

	if err := writeManifest(out); err != nil {
		_ = os.Remove(path)
		return Backup{}, err
	}
	return out, nil
}

// VerifyBackup opens a snapshot read-only, integrity-checks it, and returns its
// schema version.
//
// Used by Snapshot on the way out and by the restore path on the way in, so the
// same check runs at both ends and a backup cannot be declared good by one
// definition and bad by the other.
//
// # Read-only, and that is not a detail
//
// Opening through [Open] would apply this system's connection pragmas, and
// journal_mode(WAL) writes to the file. Verifying a backup would then change
// it: the checksum recorded a moment earlier would no longer match, and — worse
// — every subsequent verification would invalidate the file it was checking.
// A verification that mutates its subject is not a verification.
func VerifyBackup(ctx context.Context, path string) (int64, error) {
	probe, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrBackupUnusable, path, err)
	}
	defer func() { _ = probe.Close() }()

	if err := probe.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrBackupUnusable, path, err)
	}

	var result string
	if err := probe.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrBackupUnusable, path, err)
	}
	if result != "ok" {
		return 0, fmt.Errorf("%w: %s: integrity_check %s", ErrBackupUnusable, path, result)
	}

	// goose's own table, read directly rather than through goose: this is a
	// read-only handle and the library's version call takes a package-global
	// lock it has no business taking here.
	var schema int64
	err = probe.QueryRowContext(ctx,
		`SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1`,
	).Scan(&schema)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: versi skema tidak terbaca: %w", ErrBackupUnusable, path, err)
	}
	return schema, nil
}

// checkDestination refuses a backup that would not survive the thing it is for.
func (db *DB) checkDestination(dir string) error {
	source, err := filepath.Abs(db.Path())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBackupDestination, err)
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBackupDestination, err)
	}

	// The obvious case first, and the one that needs no syscall: a backup
	// inside the database's own directory.
	if target == filepath.Dir(source) {
		return fmt.Errorf("%w: %s berada di folder yang sama dengan %s",
			ErrSameDevice, target, source)
	}

	same, known := sameDevice(filepath.Dir(source), target)
	if known && same {
		return fmt.Errorf("%w: %s dan %s berada pada perangkat yang sama",
			ErrSameDevice, target, source)
	}
	// Where the platform cannot tell — Windows — the path check above is all
	// there is, and the drill in docs/RESTORE-DRILL.md is what catches the
	// rest. Better a backup taken than a backup refused for want of a syscall.
	return nil
}

func checksum(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a path this package just wrote
	if err != nil {
		return "", fmt.Errorf("store: baca %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("store: checksum %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify re-reads a snapshot and checks it against its manifest.
//
// Both halves matter and they fail differently: the checksum catches a file
// that was truncated or corrupted in transit to the USB stick, and the
// integrity check catches one that copied perfectly and was never a valid
// database.
func (b Backup) Verify(ctx context.Context) error {
	sum, err := checksum(b.Path)
	if err != nil {
		return err
	}
	if sum != b.SHA256 {
		return fmt.Errorf("%w: %s: checksum %s, manifes menyebut %s",
			ErrBackupUnusable, b.Path, sum, b.SHA256)
	}

	schema, err := VerifyBackup(ctx, b.Path)
	if err != nil {
		return err
	}
	if schema != b.SchemaVersion {
		return fmt.Errorf("%w: %s: versi skema %d, manifes menyebut %d",
			ErrBackupUnusable, b.Path, schema, b.SchemaVersion)
	}
	return nil
}

// writeManifest puts the checksum and schema version beside the snapshot.
//
// A sidecar rather than a header inside the file, so the snapshot stays a plain
// SQLite database anybody can open with any tool — the same reasoning as the
// data export (R14.3). The manifest is a convenience for verifying it; it is
// never required to read it.
func writeManifest(b Backup) error {
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("store: manifes cadangan: %w", err)
	}
	if err := os.WriteFile(b.Manifest, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("store: tulis %s: %w", b.Manifest, err)
	}
	return nil
}

// LoadBackup reads a snapshot's manifest.
func LoadBackup(path string) (Backup, error) {
	manifest := path
	if strings.HasSuffix(path, ".db") {
		manifest = strings.TrimSuffix(path, ".db") + ".json"
	}

	body, err := os.ReadFile(manifest) //nolint:gosec // an operator-supplied backup path
	if err != nil {
		return Backup{}, fmt.Errorf("%w: %s: %w", ErrBackupUnusable, manifest, err)
	}

	var out Backup
	if err := json.Unmarshal(body, &out); err != nil {
		return Backup{}, fmt.Errorf("%w: %s: %w", ErrBackupUnusable, manifest, err)
	}
	out.Manifest = manifest
	out.Path = strings.TrimSuffix(manifest, ".json") + ".db"
	return out, nil
}

// ListBackups returns every snapshot in dir, newest first.
func ListBackups(dir string) ([]Backup, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "tera-*.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBackupDestination, dir, err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))

	out := make([]Backup, 0, len(entries))
	for _, m := range entries {
		b, err := LoadBackup(m)
		if err != nil {
			// One unreadable manifest must not hide the backups beside it.
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// Prune deletes all but the newest keep snapshots, oldest first.
//
// Returns what it removed. Retention is a real requirement rather than tidiness:
// a USB stick that fills up stops receiving backups, and the failure is silent
// unless something is deleting the old ones.
func Prune(dir string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("%w: menyimpan %d cadangan", ErrBackupDestination, keep)
	}

	all, err := ListBackups(dir)
	if err != nil {
		return nil, err
	}
	if len(all) <= keep {
		return nil, nil
	}

	var removed []string
	for _, b := range all[keep:] {
		if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("store: hapus %s: %w", b.Path, err)
		}
		if err := os.Remove(b.Manifest); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("store: hapus %s: %w", b.Manifest, err)
		}
		removed = append(removed, b.Path)
	}
	return removed, nil
}
