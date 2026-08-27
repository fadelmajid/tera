// Command tera is the whole system in one binary: HTTP API, embedded SPA,
// domain core, and the ESC/POS driver for the printer and cash drawer.
//
// Deployment is a LAN server (ARCHITECTURE §1). One machine holds the database
// and serves every other device over the shop's own network, so the listener
// binds 0.0.0.0 and never localhost. "Offline" here means no internet, not no
// network (INV-11).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
	terahttp "github.com/fadelmajid/tera/internal/transport/http"

	// Embed the IANA timezone database. Book-year and business-day boundaries
	// are resolved in the entity's timezone (INV-5, DECISIONS D-005), and the
	// server is a shop PC that may carry no system zoneinfo — or be restored
	// onto a laptop that doesn't (R8.7). Without this, LoadLocation("Asia/Jakarta")
	// fails, the fallback is UTC, and a 23:30 WIB sale on 31 December books into
	// the wrong year: silently, once a year, in the figure the omzet alarm reads.
	_ "time/tzdata"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// dispatch owns the deferred signal teardown; main only decides the exit
	// code, so os.Exit never skips it.
	if err := dispatch(); err != nil {
		slog.Error("tera berhenti", "error", err)
		os.Exit(1)
	}
}

func dispatch() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Subcommands, deliberately few. The backup and restore paths are the whole
	// of R8.7's mitigation, and they have to be runnable by somebody who is not
	// the developer on a laptop that is not the shop's — which means one binary,
	// no arguments to remember, and no server running.
	switch cmd, rest := subcommand(); cmd {
	case "backup":
		return runBackup(ctx, rest, logger)
	case "restore":
		return runRestore(ctx, rest, logger)
	case "backups":
		return runBackupList(ctx, rest, logger)
	case "help", "-h", "--help":
		usage(logger)
		return nil
	default:
		return run(ctx, logger)
	}
}

// subcommand splits argv into a verb and its flags. No verb means run the
// server, which is what the shop machine does and what a double-click does.
func subcommand() (cmd string, rest []string) {
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		return "", os.Args[1:]
	}
	return os.Args[1], os.Args[2:]
}

func usage(logger *slog.Logger) {
	logger.Info("tera — sistem dagang dan persediaan", "version", version)
	logger.Info("  tera                          jalankan server")
	logger.Info("  tera backup --to DIR          ambil satu cadangan sekarang")
	logger.Info("  tera backups --dir DIR        daftar cadangan yang ada")
	logger.Info("  tera restore --from BERKAS    pulihkan cadangan ke basis data")
}

func run(ctx context.Context, logger *slog.Logger) error {
	logger.Info("tera", "version", version)

	dbPath := env("TERA_DB_PATH", "tera.db")
	addr := env("TERA_ADDR", terahttp.DefaultAddr)

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			logger.Error("gagal menutup database", "error", cerr)
		}
	}()

	if err := db.Migrate(ctx); err != nil {
		return err
	}
	schema, err := db.Version(ctx)
	if err != nil {
		return err
	}
	logger.Info("database siap", "path", db.Path(), "schema_version", schema)

	// R8.6: the address other devices reach this machine on may have moved
	// while it was off, and nothing else in the building can tell.
	warnIfAddressChanged(db.Path(), logger)

	auth := service.NewAuth(db, time.Now)
	if err := bootstrap(ctx, auth, logger); err != nil {
		return err
	}
	if err := auth.PurgeExpiredSessions(ctx); err != nil {
		logger.Warn("gagal membersihkan sesi kedaluwarsa", "error", err)
	}

	// Frees idempotency claims abandoned by a previous crash, so a client
	// retrying a sale from before the restart is not blocked by its own claim.
	idem := service.NewIdempotency(db, time.Now)
	if err := idem.PurgeStaleClaims(ctx); err != nil {
		logger.Warn("gagal membersihkan klaim tertunda", "error", err)
	}

	// One auditor for every service: the audit row and the change it describes
	// commit in the same transaction, so there is nothing to share but the
	// clock (INV-10).
	aud := service.NewAuditor(time.Now)
	master := service.NewMasterData(db, aud, time.Now)
	purchasing := service.NewPurchasing(db, aud, time.Now)
	opname := service.NewOpname(db, aud, time.Now)
	opening := service.NewOpening(db, aud, time.Now)
	sales := service.NewSales(db, aud, time.Now)
	marginReport := service.NewMargin(db, aud, time.Now)
	transfers := service.NewTransfers(db, aud, time.Now)
	taxes := service.NewTax(db, aud, time.Now)
	exporter := service.NewExport(db, time.Now)
	reports := service.NewReports(db, time.Now)
	omzetClock := service.NewOmzet(db, aud, time.Now)

	// The printer is attached to the server machine, which is where the cashier
	// sits (ARCHITECTURE §1). Only the two facts that vary by model are
	// configurable -- how it is attached and how wide the paper is -- because
	// everything the driver sends is the portable ESC/POS subset and the shop's
	// actual printer is not known yet.
	//
	// Unset means no printer. That is a supported state: the shop can trade and
	// read receipts on screen until the hardware arrives.
	printer := service.NewPrinter(service.PrinterConfig{
		Addr:       os.Getenv("TERA_PRINTER_ADDR"),
		Device:     os.Getenv("TERA_PRINTER_DEVICE"),
		Width:      envInt("TERA_PRINTER_WIDTH", 32),
		DrawerPin:  drawerPin(),
		PartialCut: os.Getenv("TERA_PRINTER_PARTIAL_CUT") == "1",
	})
	printing := service.NewPrinting(gen.New(db), printer,
		[]string{env("TERA_RECEIPT_FOOTER", "Terima kasih")}, time.Now)
	if printing.Configured() {
		logger.Info("printer siap", "addr", os.Getenv("TERA_PRINTER_ADDR"),
			"device", os.Getenv("TERA_PRINTER_DEVICE"))
	} else {
		logger.Warn("printer belum dikonfigurasi; struk hanya bisa dilihat di layar",
			"set", "TERA_PRINTER_ADDR atau TERA_PRINTER_DEVICE")
	}

	srv := terahttp.New(terahttp.Config{
		Addr:         addr,
		DB:           db,
		Auth:         auth,
		Idem:         idem,
		Master:       master,
		Purchasing:   purchasing,
		Opname:       opname,
		Opening:      opening,
		Sales:        sales,
		Printing:     printing,
		Margin:       marginReport,
		Transfers:    transfers,
		Tax:          taxes,
		Export:       exporter,
		Reports:      reports,
		Omzet:        omzetClock,
		Logger:       logger,
		CookieSecure: os.Getenv("TERA_COOKIE_SECURE") == "1",
	})

	// TASKS 8.1. Runs beside the server rather than as a cron job, because the
	// deployment story is "copy one binary and run it" (ARCHITECTURE §1) — a
	// backup that needs a second thing installed is a backup that will not
	// exist on the shop machine.
	backups := service.NewBackups(db, service.BackupConfig{
		Dir:             os.Getenv("TERA_BACKUP_DIR"),
		Interval:        time.Duration(envInt("TERA_BACKUP_INTERVAL_MINUTES", 60)) * time.Minute,
		Keep:            envInt("TERA_BACKUP_KEEP", service.DefaultBackupKeep),
		Version:         version,
		Logger:          logger,
		AllowSameDevice: os.Getenv("TERA_BACKUP_ALLOW_SAME_DISK") == "1",
	})
	go backups.Run(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Start(ctx) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("menerima sinyal berhenti, menyelesaikan permintaan yang berjalan")
	}

	// Give in-flight work a chance to finish. A sale that has begun writing
	// must be allowed to complete, or the cashier is left not knowing whether
	// it took (ARCHITECTURE §4).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serveErr; err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	logger.Info("tera berhenti dengan bersih")
	return nil
}

// bootstrap creates the first user on an empty database and prints the
// generated password once. It is the only time a credential is ever logged.
func bootstrap(ctx context.Context, auth *service.Auth, logger *slog.Logger) error {
	username, password, created, err := auth.BootstrapAdmin(ctx)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}

	logger.Warn("pengguna pertama dibuat — catat kata sandi ini, tidak akan ditampilkan lagi",
		"username", username, "password", password)
	return nil
}

// drawerPin reads which of the printer's two drawer pins the cable uses.
//
// A fact about the wiring, not about the software, and there are exactly two
// valid answers. Anything else is a typo and falls back to 0, the common
// wiring, rather than sending a byte the printer will interpret as something
// else entirely.
func drawerPin() byte {
	if envInt("TERA_DRAWER_PIN", 0) == 1 {
		return 1
	}
	return 0
}

// envInt reads an integer setting, falling back when unset or unparsable. A
// typo in the paper width should print a ragged receipt, not refuse to start.
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
