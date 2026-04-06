package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // Essential for profiling running agents
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/memory"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

// LoadFunc provides an abstract way to deliver fresh configuration to the Daemon without locking it to a specific format
type LoadFunc func() (*config.Config, error)

// ProtocolDaemon represents the running service state
type ProtocolDaemon struct {
	LoadFunc   LoadFunc
	Logger     *slog.Logger
	Supervisor *agent.Supervisor
	AdminPort  string

	currentConfig *config.Config
}

// New creates a new isolated ProtocolDaemon
func New(logger *slog.Logger, loadFunc LoadFunc, adminPort string) *ProtocolDaemon {
	if adminPort == "" {
		adminPort = ":9090"
	}
	return &ProtocolDaemon{
		LoadFunc:  loadFunc,
		Logger:    logger,
		AdminPort: adminPort,
	}
}

// Run executes the main event loop with lifecycle management
func (d *ProtocolDaemon) Run(ctx context.Context) error {
	// Create a context that cancels on OS Signals
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Initial Load of configuration
	if err := d.loadConfig(); err != nil {
		return fmt.Errorf("initial config load failed: %w", err)
	}

	// 1. Initialize NATS (The Nervous System)
	natsURL := d.currentConfig.NATS.URL
	if natsURL == "" {
		natsURL = "nats://nats:4222"
	}
	nc, err := transport.Connect(natsURL)
	if err != nil {
		return fmt.Errorf("nats connect error: %w", err)
	}
	defer nc.Close()

	js, err := transport.JetStream(nc)
	if err != nil {
		return fmt.Errorf("jetstream error: %w", err)
	}

	// Ensure all standard TAP streams (TASKS, EVENTS, CONTRACTS) exist
	if err := transport.InitStreams(js); err != nil {
		d.Logger.Error("failed to initialize nats streams", "error", err)
		return fmt.Errorf("stream init error: %w", err)
	}

	_, err = micrion.SetupKV(js)
	if err != nil {
		return fmt.Errorf("micrion kv error: %w", err)
	}

	// Wrap NATS with our EventBus Adapter
	bus := agent.NewNatsAdapter(nc, js)

	// 1.5 Initialize Database & Memory (Long-term Storage)
	dbConfig, err := pgxpool.ParseConfig(d.currentConfig.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		d.Logger.Error("db ping failed", "error", err)
		return fmt.Errorf("db ping failed: %w", err)
	}

	mem := memory.NewManager(dbPool)

	pineconeKey := os.Getenv("PINECONE_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	if pineconeKey == "" {
		d.Logger.Warn("PINECONE_API_KEY missing, skipping AI Entity Resolvers")
	}

	pcObj, _ := vector.NewPineconeClient(pineconeKey, d.currentConfig.PineconeIndex, d.currentConfig.EmbeddingDimensions)
	embedder, _ := vector.NewEmbedder(openaiKey, d.currentConfig.EmbeddingModel, d.currentConfig.EmbeddingDimensions)
	st, _ := store.NewStore(nil, nil, d.currentConfig.EncryptionKey)
	entityResolver := ai.NewEntityResolver(st, pcObj, embedder, d.currentConfig.AIThreshold)

	// 2. Initialize the Agent Supervisor (The Hive)
	d.Supervisor = agent.NewSupervisor(d.Logger, bus, mem, dbPool, entityResolver)

	for name, factory := range agents.GetRegistry() {
		d.Supervisor.RegisterInternalAgent(name, factory)
	}

	// 3. Load Initial Agent Configuration into the supervisor
	if err := d.Supervisor.LoadAgents(d.currentConfig.Agents); err != nil {
		return err
	}

	// 4. Use ErrGroup to manage concurrent sub-systems
	// If one dies, they all die (fail fast)
	g, ctx := errgroup.WithContext(ctx)

	// Sub-system A: The Admin Server (Health, Pprof, Metrics)
	g.Go(func() error {
		return d.startAdminServer(ctx)
	})

	// Sub-system B: Signal Watcher (For Hot Reloading SIGHUP)
	g.Go(func() error {
		return d.watchForReload(ctx)
	})

	// Sub-system C: The Supervisor (keeps agents alive)
	// Monitors health and prints heartbeats
	g.Go(func() error {
		return d.Supervisor.Run(ctx)
	})

	// Sub-system D: Global Redux Rollup State Compactor
	rollupWorker := redux.NewRollupWorker(d.Logger, js, dbPool)
	g.Go(func() error {
		if err := rollupWorker.Start(ctx); err != nil {
			d.Logger.Error("Fatal Rollup Initialization failing bounds", "err", err)
			return err
		}
		return nil
	})

	d.Logger.Info("🚀 Protocol Hive Active", "admin_port", d.AdminPort)

	// Wait for termination signal
	<-ctx.Done()
	d.Logger.Info("Shutting down...")

	// 5. Graceful Shutdown
	// Give agents 5 seconds to finish in-flight tasks (LLM calls/Payments)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	d.Supervisor.Shutdown(shutdownCtx)

	return g.Wait()
}

// startAdminServer exposes internal metrics and debugging without exposing agents
func (d *ProtocolDaemon) startAdminServer(ctx context.Context) error {
	mux := http.NewServeMux()

	// Health Check for K8s
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Hot Reload Trigger (Alternative to SIGHUP)
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		if err := d.loadConfig(); err != nil {
			d.Logger.Error("Hot reload failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("Agents Reloaded"))
	})

	// Pprof is automatically registered on DefaultServeMux, so we mount it
	// This lets us debug memory leaks and goroutine blocks in production
	mux.Handle("/debug/pprof/", http.DefaultServeMux)

	srv := &http.Server{
		Addr:    d.AdminPort,
		Handler: mux,
	}

	// Run server in goroutine
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	d.Logger.Info("Admin server listening", "port", d.AdminPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// watchForReload listens for SIGHUP to reload configs without downtime
func (d *ProtocolDaemon) watchForReload(ctx context.Context) error {
	// Create a separate channel for SIGHUP (Reload), distinct from SIGTERM (Kill)
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c:
			d.Logger.Info("🔄 Received SIGHUP. Hot-reloading agent configurations...")
			if err := d.loadConfig(); err != nil {
				d.Logger.Error("Failed to reload config", "error", err)
				// Don't crash on bad config reload, just log it
				continue
			}
			d.Logger.Info("✅ Configuration reloaded successfully")
		}
	}
}

// loadConfig abstracts away the native configuration system to hot load gracefully
func (d *ProtocolDaemon) loadConfig() error {
	newCfg, err := d.LoadFunc()
	if err != nil {
		return fmt.Errorf("failed to load external config: %w", err)
	}

	d.currentConfig = newCfg

	// HotLoad: The Supervisor will diff the new config against running agents
	// It starts new ones, updates existing ones, and stops removed ones.
	if d.Supervisor != nil {
		if err := d.Supervisor.LoadAgents(d.currentConfig.Agents); err != nil {
			return err
		}
	}

	return nil
}
