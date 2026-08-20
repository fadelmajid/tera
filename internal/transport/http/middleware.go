package http

import (
	"context"
	"log/slog"
	stdhttp "net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// requestLogger logs one structured line per request.
//
// Every mutating request is eventually accountable to someone: R13.5 records
// who entered a transaction, and INV-10 requires an audit trail on historical
// edits. This log is the operational counterpart — it answers "what did the
// server actually receive" when the audit log answers "what changed".
func requestLogger(log *slog.Logger) func(stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			started := time.Now()

			defer func() {
				log.LogAttrs(r.Context(), levelFor(ww.Status()), "http",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", ww.Status()),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Duration("took", time.Since(started)),
					slog.String("from", r.RemoteAddr),
					slog.String("request_id", middleware.GetReqID(r.Context())),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func levelFor(status int) slog.Level {
	switch {
	case status >= stdhttp.StatusInternalServerError:
		return slog.LevelError
	case status >= stdhttp.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func contextWithTimeout(r *stdhttp.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
