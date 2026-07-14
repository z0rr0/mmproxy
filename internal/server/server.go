// Package server implements the MMProxy HTTP server: health/version endpoints
// and the Miniflux webhook receiver, wrapped with logging and panic-recovery
// middleware.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/z0rr0/mmproxy/internal/config"
)

const (
	readTimeout       = 10 * time.Second
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 30 * time.Second // must exceed the Mattermost request timeout
	maxHeaderBytes    = 1 << 20          // 1 MiB
)

// Poster publishes a message to a Mattermost channel. Defined on the consumer
// side; satisfied by *mattermost.Client.
type Poster interface {
	Post(ctx context.Context, channelID, message string) error
}

// Server wraps the standard library HTTP server with the MMProxy routes.
type Server struct {
	srv      *http.Server
	poster   Poster
	version  string
	miniflux config.Miniflux
	secret   []byte // Miniflux webhook secret, precomputed to avoid per-request allocation
}

// New builds the HTTP server. The Miniflux route is registered only when the
// source is enabled, so requests to a disabled webhook get a 404.
func New(cfg *config.Config, poster Poster, version string) *Server {
	s := &Server{
		poster:   poster,
		version:  version,
		miniflux: cfg.Miniflux,
		secret:   []byte(cfg.Miniflux.WebhookSecret),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /version", s.handleVersion)
	if cfg.Miniflux.Enabled() {
		mux.HandleFunc("POST /miniflux", s.handleMiniflux)
	}

	// Order matters: RecoverMiddleware is outermost so it also catches panics
	// raised inside the logging middleware.
	handler := RecoverMiddleware(LoggingMiddleware(mux))

	s.srv = &http.Server{
		Addr:              cfg.Base.Addr,
		Handler:           handler,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	return s
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.srv.Addr }

// ListenAndServe starts serving and blocks until the server is shut down.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server, draining in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
