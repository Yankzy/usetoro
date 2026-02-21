package api

import (
	"context"
	"crypto"
	"log/slog"
	"net/http"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/redis/go-redis/v9"
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
	cfg *config.Config,
	logger *slog.Logger,
	st *store.Store,
	pub *ingest.Publisher,
	qboConfig *QBOConfig,
	authenticator *auth.Authenticator,
	redisClient *redis.Client, // Added redisClient parameter
) *Server { // <-- TYPE: returns an address
	// Initialize webhook verifier registry
	registry := NewVerifierRegistry()

	// Register supported webhook providers
	registry.Register(NewStripeVerifier())
	// QBO webhook verification using generic HMAC verifier
	registry.Register(NewHMACVerifier("qbo", "intuit-signature", crypto.SHA256))
	// Future HMAC-based providers can be registered similarly:
	// registry.Register(NewHMACVerifier("plaid", "x-plaid-signature", crypto.SHA256))

	h := NewHandler(logger, st, pub, registry, cfg.MaxWebhookBodySize, qboConfig, authenticator, redisClient) // Passed redisClient to NewHandler
	mux := NewRouter(h)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return &Server{ // <-- TYPE: The address of a Server struct
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
