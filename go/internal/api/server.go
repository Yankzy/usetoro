package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/store"
)

// Server represents the API server and its dependencies.
type Server struct {
	server *http.Server
	logger *slog.Logger
}

// NewServer constructs a new Server.
// It takes necessary dependencies as arguments.
// Note: We inject the store and publisher here rather than creating them inside.
// This allows for better testability (mocking) and separation of concerns.
func NewServer(
	cfg config.Config,
	logger *slog.Logger,
	st *store.Store,
	pub *ingest.Publisher,
) *Server {
	h := NewHandler(logger, st, pub, cfg.MaxWebhookBodySize)
	mux := NewRouter(h)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return &Server{
		server: srv,
		logger: logger,
	}
}

// Start runs the server in a goroutine and returns a channel for errors.
func (s *Server) Start() <-chan error {
	errs := make(chan error, 1)
	go func() {
		s.logger.Info("🐂 Toro Ingress started", "addr", s.server.Addr)
		errs <- s.server.ListenAndServe()
	}()
	return errs
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Close immediately closes the server.
func (s *Server) Close() error {
	return s.server.Close()
}
