package http

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/fadelmajid/tera/internal/service"
)

// ClientRequestHeader carries the caller's idempotency key (INV-6).
const ClientRequestHeader = "X-Client-Request-Id"

const (
	maxRequestBody = 1 << 20 // 1 MiB
	maxStoredBody  = 256 << 10
)

// idempotencySkip lists path prefixes that carry no idempotency key.
//
// Auth routes, because logging in is not a business mutation and its effect is
// a Set-Cookie header a replayed body could not reproduce.
//
// Previews, because they write nothing and are POSTs only so a cart fits in the
// body. Replaying a cached preview would be actively wrong: it plans against
// live stock, so the answer must be recomputed each time rather than served
// from what was true when the key was first seen.
var idempotencySkip = []string{
	"/api/v1/auth/",
	"/api/v1/transfers/preview",
}

func skipsIdempotency(path string) bool {
	for _, prefix := range idempotencySkip {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// idempotent makes mutating calls safe to retry.
//
// The failure it prevents is concrete: a cashier taps Bayar, the WiFi stutters,
// the browser retries, and the shop has rung the same sale twice — two sets of
// FIFO consumptions, two omzet rows, stock short by a whole cart. Retrying is
// correct behaviour on the client's part, so the server has to be what makes it
// safe.
//
// Some routes are exempt; see [idempotencySkip] for which and why.
func idempotent(idem *service.Idempotency, log *slog.Logger) func(stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			if idem == nil || !mutating(r.Method) || skipsIdempotency(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			id := strings.TrimSpace(r.Header.Get(ClientRequestHeader))
			if !validClientRequestID(id) {
				writeJSON(w, stdhttp.StatusBadRequest, map[string]any{
					"error": "header " + ClientRequestHeader + " wajib berisi UUID huruf kecil",
				})
				return
			}

			body, err := io.ReadAll(stdhttp.MaxBytesReader(w, r.Body, maxRequestBody))
			if err != nil {
				writeJSON(w, stdhttp.StatusRequestEntityTooLarge, map[string]any{
					"error": "permintaan terlalu besar",
				})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			var userID string
			if p, ok := PrincipalFrom(r.Context()); ok {
				userID = p.UserID
			}

			replay, err := idem.Claim(r.Context(), id, userID, r.Method, r.URL.Path, service.HashBody(body))
			switch {
			case errors.Is(err, service.ErrRequestInFlight):
				writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
				return
			case errors.Is(err, service.ErrRequestMismatch):
				writeJSON(w, stdhttp.StatusConflict, map[string]any{"error": err.Error()})
				return
			case err != nil:
				log.ErrorContext(r.Context(), "idempotency: gagal mengklaim permintaan", "error", err)
				writeJSON(w, stdhttp.StatusInternalServerError, map[string]any{"error": "kesalahan internal"})
				return
			}

			if replay != nil {
				// A retry gets the original answer, byte for byte. The second
				// sale was never rung.
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Idempotent-Replay", "true")
				w.WriteHeader(replay.StatusCode)
				// gosec traces database value -> response and calls it XSS. The
				// value is this server's own earlier response to this same
				// request, replayed verbatim, served as application/json with
				// nosniff set globally. There is no HTML context to escape into.
				_, _ = io.WriteString(w, replay.Body) //nolint:gosec // G705: replay of our own JSON response
				return
			}

			rec := &recorder{ResponseWriter: w, status: stdhttp.StatusOK}
			next.ServeHTTP(rec, r)

			// Only a success is worth replaying. A 4xx or 5xx means nothing was
			// committed — handlers write inside a single transaction — so the
			// claim is released and the caller may correct the payload and retry
			// with the same id rather than being locked out by their own
			// failed attempt.
			if rec.status < 200 || rec.status > 299 || rec.overflowed {
				if rerr := idem.Release(r.Context(), id); rerr != nil {
					log.ErrorContext(r.Context(), "idempotency: gagal melepas klaim", "error", rerr)
				}
				return
			}

			if cerr := idem.Complete(r.Context(), id, rec.status, rec.body.String()); cerr != nil {
				log.ErrorContext(r.Context(), "idempotency: gagal menyimpan hasil", "error", cerr)
			}
		})
	}
}

func mutating(method string) bool {
	switch method {
	case stdhttp.MethodPost, stdhttp.MethodPut, stdhttp.MethodPatch, stdhttp.MethodDelete:
		return true
	default:
		return false
	}
}

// validClientRequestID mirrors the CHECK constraint on request_log: a canonical
// lowercase UUID. Enforced here so a bad key is a clear 400 rather than a
// constraint violation surfacing as a 500.
func validClientRequestID(id string) bool {
	if len(id) != 36 || id != strings.ToLower(id) {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

// recorder captures the response so it can be replayed to a retry.
type recorder struct {
	stdhttp.ResponseWriter
	status     int
	body       bytes.Buffer
	wrote      bool
	overflowed bool
}

func (r *recorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(stdhttp.StatusOK)
	}
	if r.body.Len()+len(b) > maxStoredBody {
		// Too large to store. The response still goes to the client; it simply
		// will not be replayable, which is recorded by releasing the claim.
		r.overflowed = true
	} else {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}
