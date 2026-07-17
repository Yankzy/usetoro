package api

import (
	"context"
	"crypto"
	"log/slog"
	"net/http"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
	"github.com/nats-io/nats.go"
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
	redisClient *redis.Client,
	natsClient *queue.Client,
	exporter Exporter,
) *Server {
	// Initialize webhook verifier registry
	registry := NewVerifierRegistry()

	// Register supported webhook providers
	registry.Register(NewHMACVerifier("qbo", "intuit-signature", crypto.SHA256))

	connector := connectors.NewQBOConnector(logger, cfg, st, natsClient.Conn())
	reconciler := accounting.NewReconciliationService(logger, st.Queries, connector.ClientForRealm)

	// Since we are migrating toward standard erp.Providers, passing nil will gracefully fall back to the QBO factory resolver.
	// For now, the legacy AttachableService uses QBOConnector directly or an erp.ProviderFactory if we have one.
	attachableService := accounting.NewAttachableService(logger, nil)

	var natsConn *nats.Conn
	if natsClient != nil {
		natsConn = natsClient.Conn()
	}

	var kv nats.KeyValue
	var wm *micrion.WalletManager
	if natsConn != nil {
		js, err := natsConn.JetStream()
		if err != nil {
			logger.Error("Failed to get JetStream context for Micrions", "error", err)
			panic(err)
		}
		kv, err = micrion.SetupKV(js)
		if err != nil {
			logger.Error("Failed to setup Micrions KV store", "error", err)
			panic(err)
		}
		ledger := store.NewWalletLedger(st.Queries)
		wm = micrion.NewWalletManager(ledger, kv)
	} else {
		logger.Warn("NATS connection is nil - Gate Agent routes will fail securely without KV store")
	}

	transactionService := accounting.NewTransactionService(logger, st.Queries, nil, nil, nil, nil, natsConn, "toro.erp.events.*")
	entityService := accounting.NewEntityService(logger, st.Queries)

	if err := ase.InitConfig(st.Queries, redisClient, logger); err != nil {
		logger.Error("Failed to initialize ASE config", "error", err)
	}

	h := NewHandler(
		logger, st, pub, registry, cfg.MaxWebhookBodySize, qboConfig,
		authenticator, redisClient, st.Queries, reconciler, attachableService,
		transactionService, entityService,
		st.Pool, st.Queries, natsClient, exporter, wm,
	)
	var mpHandler *mailpool.Handler
	if cfg.MailpoolAPIKey != "" {
		mpClient, err := mailpool.NewMailpool(cfg.MailpoolEndpoint, cfg.MailpoolAPIKey)
		if err == nil {
			mpHandler = mailpool.NewHandler(mpClient, cfg.MailpoolAPIKey)
		} else {
			logger.Error("Failed to initialize Mailpool client", "error", err)
		}
	}

	mux := NewRouter(h, wm, mpHandler)

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
