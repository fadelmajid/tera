package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/service"
	"github.com/fadelmajid/tera/internal/store"
)

// countingHandler stands in for a sale: it has a side effect, and ringing it
// twice is the bug (INV-6).
type countingHandler struct {
	calls  atomic.Int64
	status int
	delay  time.Duration
}

func (h *countingHandler) ServeHTTP(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	n := h.calls.Add(1)
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	status := h.status
	if status == 0 {
		status = stdhttp.StatusCreated
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"sale_number": n})
}

func idemHarness(t *testing.T) (*service.Idempotency, *store.DB) {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "tera.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewIdempotency(db, time.Now), db
}

func post(t *testing.T, h stdhttp.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(),
		stdhttp.MethodPost, "/api/v1/sales", bytes.NewReader([]byte(body)))
	if id != "" {
		req.Header.Set(ClientRequestHeader, id)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The cashier taps Bayar, the network stutters, the browser retries. The sale
// must be rung once and the retry must see the original answer.
func TestRetryDoesNotRingTheSaleTwice(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{}
	h := idempotent(idem, slog.Default())(counter)

	id := store.NewID()
	const body = `{"lines":[{"product":"masker","qty":2}]}`

	first := post(t, h, id, body)
	if first.Code != stdhttp.StatusCreated {
		t.Fatalf("first status = %d, want 201 (%s)", first.Code, first.Body)
	}

	second := post(t, h, id, body)
	if second.Code != stdhttp.StatusCreated {
		t.Fatalf("retry status = %d, want the original 201", second.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("retry body %q differs from the original %q", second.Body, first.Body)
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("retry was not marked as a replay")
	}
	if got := counter.calls.Load(); got != 1 {
		t.Errorf("handler ran %d times; the sale was rung more than once", got)
	}
}

// A different cart under the same key is a client bug. Answering it with the
// first sale's result would silently discard the second.
func TestSameKeyDifferentBodyIsRefused(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{}
	h := idempotent(idem, slog.Default())(counter)

	id := store.NewID()
	if got := post(t, h, id, `{"qty":1}`); got.Code != stdhttp.StatusCreated {
		t.Fatalf("first status = %d", got.Code)
	}

	got := post(t, h, id, `{"qty":99}`)
	if got.Code != stdhttp.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", got.Code, got.Body)
	}
	if counter.calls.Load() != 1 {
		t.Error("the mismatched request was executed")
	}
}

func TestClientRequestIDIsRequiredAndValidated(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{}
	h := idempotent(idem, slog.Default())(counter)

	tests := []struct {
		name string
		id   string
	}{
		{"missing", ""},
		{"not a uuid", "abc"},
		{"uppercase", "0199C0FF-EE00-7000-8000-000000000000"},
		{"uuid without hyphens", "0199c0ffee0070008000000000000000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := post(t, h, tc.id, `{}`)
			if got.Code != stdhttp.StatusBadRequest {
				t.Errorf("status = %d, want 400 (%s)", got.Code, got.Body)
			}
		})
	}
	if counter.calls.Load() != 0 {
		t.Error("a request with an invalid key reached the handler")
	}
}

// Two copies of the same retry arriving at once must not both execute. The
// claim is inserted before the handler runs precisely for this.
func TestConcurrentDuplicatesExecuteOnce(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{delay: 50 * time.Millisecond}
	h := idempotent(idem, slog.Default())(counter)

	id := store.NewID()
	const body = `{"lines":[{"product":"masker","qty":2}]}`
	const attempts = 8

	var wg sync.WaitGroup
	codes := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = post(t, h, id, body).Code
		}(i)
	}
	wg.Wait()

	if got := counter.calls.Load(); got != 1 {
		t.Errorf("handler ran %d times under %d concurrent duplicates, want 1", got, attempts)
	}

	var created, conflict int
	for _, c := range codes {
		switch c {
		case stdhttp.StatusCreated:
			created++
		case stdhttp.StatusConflict:
			conflict++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if created+conflict != attempts {
		t.Errorf("accounted for %d of %d responses", created+conflict, attempts)
	}
	if created == 0 {
		t.Error("no attempt succeeded")
	}
}

// A failure committed nothing, so the caller must be able to fix the payload
// and retry rather than being locked out by their own failed attempt.
func TestFailedRequestReleasesItsClaim(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	failing := &countingHandler{status: stdhttp.StatusUnprocessableEntity}
	h := idempotent(idem, slog.Default())(failing)

	id := store.NewID()
	if got := post(t, h, id, `{"qty":-1}`); got.Code != stdhttp.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", got.Code)
	}

	// Same key, corrected body: must be accepted rather than refused as a mismatch.
	ok := &countingHandler{}
	h2 := idempotent(idem, slog.Default())(ok)
	if got := post(t, h2, id, `{"qty":1}`); got.Code != stdhttp.StatusCreated {
		t.Fatalf("corrected retry status = %d, want 201 (%s)", got.Code, got.Body)
	}
}

// GETs carry no key and are never recorded.
func TestReadsAreUntouched(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{}
	h := idempotent(idem, slog.Default())(counter)

	req := httptest.NewRequestWithContext(context.Background(),
		stdhttp.MethodGet, "/api/v1/products", stdhttp.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusCreated {
		t.Errorf("status = %d; the GET did not reach the handler", rec.Code)
	}
}

// Logging in is not a business mutation and its effect is a Set-Cookie header,
// which a replayed body cannot reproduce.
func TestAuthRoutesAreExempt(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)
	counter := &countingHandler{}
	h := idempotent(idem, slog.Default())(counter)

	for i := range 2 {
		req := httptest.NewRequestWithContext(context.Background(),
			stdhttp.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{}`)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != stdhttp.StatusCreated {
			t.Fatalf("attempt %d: status = %d", i, rec.Code)
		}
	}
	if got := counter.calls.Load(); got != 2 {
		t.Errorf("auth route ran %d times, want 2 — it must not be deduplicated", got)
	}
}

// The handler must still see the body the middleware buffered to hash it.
func TestHandlerStillReceivesTheBody(t *testing.T) {
	t.Parallel()

	idem, _ := idemHarness(t)

	var seen string
	echo := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		seen = string(b)
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	})

	const body = `{"lines":[{"product":"masker","qty":2}]}`
	if got := post(t, idempotent(idem, slog.Default())(echo), store.NewID(), body); got.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d", got.Code)
	}
	if seen != body {
		t.Errorf("handler saw %q, want %q", seen, body)
	}
}
