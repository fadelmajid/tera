package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
)

// runBackup takes one snapshot and exits. `tera backup [--to DIR]`.
//
// Separate from the scheduler so a person can take a snapshot before doing
// something risky — a migration, an upgrade, moving the machine — without
// waiting for a tick or stopping the server. VACUUM INTO needs no downtime, so
// this is safe to run against a database the shop is trading on.
func runBackup(ctx context.Context, args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dir := fs.String("to", env("TERA_BACKUP_DIR", ""), "folder tujuan cadangan")
	dbPath := fs.String("db", env("TERA_DB_PATH", "tera.db"), "berkas basis data")
	sameDisk := fs.Bool("izinkan-disk-sama", os.Getenv("TERA_BACKUP_ALLOW_SAME_DISK") == "1",
		"tulis cadangan walau berada di disk yang sama (tidak dianjurkan)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("tujuan cadangan kosong: pakai --to atau isi TERA_BACKUP_DIR")
	}

	db, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	snapshot, err := db.Snapshot(ctx, store.SnapshotOptions{
		Dir: *dir, AppVersion: version, Now: time.Now(), AllowSameDevice: *sameDisk,
	})
	if err != nil {
		return err
	}

	logger.Info("cadangan tersimpan dan terverifikasi",
		"path", snapshot.Path, "ukuran", snapshot.SizeBytes,
		"skema", snapshot.SchemaVersion, "sha256", snapshot.SHA256)
	return nil
}

// runRestore replaces a database with a verified snapshot.
// `tera restore --from FILE [--db PATH]`.
//
// This is TASKS 8.2's mechanism, and the whole of R8.7's mitigation. It is
// written to be usable by somebody who is not the developer, on a laptop that
// is not the shop's, on the worst day of their year — so it says what it is
// about to do, refuses before it overwrites anything, and reports what came
// back rather than exiting silently on success.
func runRestore(ctx context.Context, args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "berkas cadangan (.db) yang akan dipulihkan")
	dbPath := fs.String("db", env("TERA_DB_PATH", "tera.db"), "berkas basis data tujuan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("pakai --from untuk menunjuk berkas cadangan")
	}

	logger.Info("memulihkan cadangan", "dari", *from, "ke", *dbPath)

	snapshot, err := service.Restore(ctx, *from, *dbPath, time.Now())
	if err != nil {
		return err
	}

	logger.Info("cadangan dipulihkan",
		"dari", snapshot.Path, "dibuat", snapshot.CreatedAt,
		"skema", snapshot.SchemaVersion, "asal", snapshot.SourcePath)

	// What came back, counted from the restored file. A restore that reports
	// only "berhasil" tells the person nothing they can check, and this is the
	// moment they most need something to check.
	if err := reportRestored(ctx, *dbPath, logger); err != nil {
		return err
	}
	logger.Info("jalankan `tera` seperti biasa untuk memakai basis data ini")
	return nil
}

// reportRestored counts what is in the restored database, so the operator has
// figures to compare against the shop's own records.
func reportRestored(ctx context.Context, dbPath string, logger *slog.Logger) error {
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	for _, table := range []string{
		"legal_entity", "product", "stock_layer", "purchase", "sale", "omzet_ledger",
	} {
		n, err := db.CountRows(ctx, table)
		if err != nil {
			return err
		}
		logger.Info("isi basis data yang dipulihkan", "tabel", table, "baris", n)
	}
	return nil
}

// runBackupList shows what is on the backup destination.
// `tera backups [--dir DIR]`.
func runBackupList(_ context.Context, args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("backups", flag.ContinueOnError)
	dir := fs.String("dir", env("TERA_BACKUP_DIR", ""), "folder cadangan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("folder cadangan kosong: pakai --dir atau isi TERA_BACKUP_DIR")
	}

	all, err := store.ListBackups(*dir)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return fmt.Errorf("tidak ada cadangan di %s", *dir)
	}

	for _, b := range all {
		logger.Info("cadangan",
			"dibuat", b.CreatedAt, "path", b.Path,
			"ukuran", b.SizeBytes, "skema", b.SchemaVersion, "sha256", b.SHA256[:12])
	}
	logger.Info("total cadangan", "jumlah", len(all), "folder", *dir)
	return nil
}
