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
	"syscall"
	"time"

	"github.com/fadelmajid/tera/internal/store"
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
	if err := run(); err != nil {
		slog.Error("tera berhenti", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("tera", "version", version)

	// A SIGINT or SIGTERM cancels this, which starts the graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	srv := terahttp.New(terahttp.Config{Addr: addr, DB: db, Logger: logger})

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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
