package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// DefaultAddr binds every interface.
//
// Not localhost, and not by accident. One machine holds the data and serves
// every other device over the shop's own network; the cashier, the admin
// laptop, and an owner's phone are all browsers on the LAN (R8.4,
// ARCHITECTURE §1). Bound to loopback, the software runs perfectly and nobody
// but the server machine can reach it.
const DefaultAddr = "0.0.0.0:8080"

// Checker is the health surface the server needs from the database.
//
// Deliberately an interface rather than the concrete store: transport does not
// import database/sql (ARCHITECTURE §2), and the linter enforces that.
type Checker interface {
	PingContext(ctx context.Context) error
	Version(ctx context.Context) (int64, error)
}

// Config configures the server.
type Config struct {
	Addr   string
	DB     Checker
	Logger *slog.Logger
}

// Server owns the HTTP listener and its lifecycle.
type Server struct {
	srv *stdhttp.Server
	log *slog.Logger
}

// New builds a server. It does not listen; call Start.
func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{log: cfg.Logger}
	s.srv = &stdhttp.Server{
		Addr:    cfg.Addr,
		Handler: Handler(cfg),
		// ReadHeaderTimeout guards against a client that opens a connection and
		// dawdles over the headers. The others are generous: this is a shop LAN,
		// not the open internet, and a slow thermal printer must not be cut off.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	return s
}

// Handler builds the route tree without binding a port.
//
// Exported so the server can be exercised end to end in tests, and so the
// embedded SPA has something to mount onto in TASKS 0.9.
func Handler(cfg Config) stdhttp.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// Deliberately no RealIP. There is no reverse proxy in front of this — every
	// client is a browser connecting directly over the shop LAN — so trusting
	// X-Forwarded-For would let any device on the network forge the address
	// recorded against its requests. r.RemoteAddr is the socket's real peer.
	r.Use(requestLogger(cfg.Logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handleLive())
	r.Get("/readyz", handleReady(cfg.DB, cfg.Logger))

	r.Route("/api/v1", func(r chi.Router) {
		// Handlers land from TASKS 0.6 onward.
		r.NotFound(notFoundJSON)
	})

	// The SPA is embedded here in TASKS 0.9. Until then, a placeholder — in
	// Bahasa Indonesia, because the UI is built in Indonesian from the start
	// rather than translated later (R9.12).
	r.Get("/", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("Tera — server berjalan.\nAntarmuka belum dibangun (lihat docs/TASKS.md 0.9).\n"))
	})

	return r
}

// Start listens and serves until Shutdown is called.
func (s *Server) Start(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("http: listen on %s: %w", s.srv.Addr, err)
	}

	s.logReachableAt(ln.Addr().String())

	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
		return fmt.Errorf("http: serve: %w", err)
	}
	return nil
}

// Shutdown stops accepting connections and waits for in-flight requests.
//
// A sale that has begun writing must be allowed to finish: killing it mid
// transaction is the partial-commit case that corrupts stock and margin at once
// (ARCHITECTURE §4). SQLite would roll back, but the cashier would be left not
// knowing whether the sale took.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("http: shutdown: %w", err)
	}
	return nil
}

// Addr reports the configured bind address.
func (s *Server) Addr() string { return s.srv.Addr }

// logReachableAt prints the URLs staff should actually open.
//
// "Copy the binary, run it, everyone opens a URL" is the install story, so the
// URL had better be on screen. Printing 0.0.0.0:8080 helps nobody.
func (s *Server) logReachableAt(bound string) {
	_, port, err := net.SplitHostPort(bound)
	if err != nil {
		port = "8080"
	}

	urls := lanURLs(port)
	if len(urls) == 0 {
		s.log.Warn("tidak menemukan alamat LAN", "bound", bound)
		return
	}

	s.log.Info("tera siap", "alamat", strings.Join(urls, "  "))

	if isLoopback(s.srv.Addr) {
		s.log.Warn("server terikat ke localhost — perangkat lain di jaringan toko tidak dapat mengakses",
			"addr", s.srv.Addr, "expected", DefaultAddr)
	}
}

// lanURLs enumerates non-loopback IPv4 addresses on this machine.
func lanURLs(port string) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	urls := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		urls = append(urls, "http://"+ip4.String()+":"+port)
	}
	return urls
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
