package http

import (
	"encoding/json"
	"log/slog"
	stdhttp "net/http"
	"time"
)

// handleLive answers whether the process is up. It touches nothing else, so a
// database problem cannot make the process look dead and get it restarted.
func handleLive() stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writeJSON(w, stdhttp.StatusOK, map[string]any{"status": "ok"})
	}
}

// handleReady answers whether the system can actually serve: the database
// responds and the schema is migrated.
//
// This is what a shop laptop's bookmark hits to tell staff the server is
// genuinely usable, and what the restore drill (R8.7) checks after bringing
// the database up on another machine.
func handleReady(db Checker, log *slog.Logger) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if db == nil {
			writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{
				"status": "unavailable", "reason": "database tidak terkonfigurasi",
			})
			return
		}

		ctx, cancel := contextWithTimeout(r, 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			log.ErrorContext(ctx, "readiness: database tidak merespons", "error", err)
			writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{
				"status": "unavailable", "reason": "database tidak merespons",
			})
			return
		}

		version, err := db.Version(ctx)
		if err != nil {
			log.ErrorContext(ctx, "readiness: versi skema tidak terbaca", "error", err)
			writeJSON(w, stdhttp.StatusServiceUnavailable, map[string]any{
				"status": "unavailable", "reason": "versi skema tidak terbaca",
			})
			return
		}

		writeJSON(w, stdhttp.StatusOK, map[string]any{
			"status":         "ok",
			"schema_version": version,
		})
	}
}

func notFoundJSON(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	writeJSON(w, stdhttp.StatusNotFound, map[string]any{"error": "endpoint tidak ditemukan"})
}

func writeJSON(w stdhttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
