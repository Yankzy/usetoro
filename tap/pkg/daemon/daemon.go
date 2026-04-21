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

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/Yankzy/usetoro/tap/pkg/memory"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"
)

// LoadFunc provides an abstract way to deliver fresh configuration to the Daemon without locking it to a specific format
type LoadFunc func() (*config.Config, *viper.Viper, error)

// ProtocolDaemon represents the running service state
type ProtocolDaemon struct {
	LoadFunc   LoadFunc
	Logger     *slog.Logger
	Supervisor *agent.Supervisor
	AdminPort  string

	// Shared Infrastructure (Injected)
	DBPool         *pgxpool.Pool
	NATS           *nats.Conn
	JS             nats.JetStreamContext
	EntityResolver *ai.EntityResolver

	// Orchestrator is the singleton Workflow Orchestrator.
	// It loads pipeline YAMLs, subscribes to trigger topics, and advances WorkflowInstances.
	Orchestrator *workflows.Orchestrator

	// Almanac is the central registry for actors.
	Almanac *lookup.Registry

	currentConfig *config.Config
	v             *viper.Viper
}

// New creates a new ProtocolDaemon with injected dependencies
func New(
	logger *slog.Logger,
	loadFunc LoadFunc,
	adminPort string,
	dbPool *pgxpool.Pool,
	nc *nats.Conn,
	js nats.JetStreamContext,
	entityResolver *ai.EntityResolver,
) *ProtocolDaemon {
	if adminPort == "" {
		adminPort = ":9090"
	}
	return &ProtocolDaemon{
		LoadFunc:       loadFunc,
		Logger:         logger,
		AdminPort:      adminPort,
		DBPool:         dbPool,
		NATS:           nc,
		JS:             js,
		EntityResolver: entityResolver,
	}
}

// Run executes the main event loop with lifecycle management
func (d *ProtocolDaemon) Run(ctx context.Context) error {
	// 1. Initial Load of configuration
	if err := d.loadConfig(); err != nil {
		return fmt.Errorf("initial config load failed: %w", err)
	}

	// 2. Initialize the Nervous System (EventBus)
	// We use the injected NATS connections
	bus := agent.NewNatsAdapter(d.NATS, d.JS)

	// 3. Initialize Memory (Long-term Storage)
	mem := memory.NewManager(d.DBPool)

	// 4. Initialize the Agent Supervisor (The Hive)
	d.Supervisor = agent.NewSupervisor(d.Logger, bus, mem, d.DBPool, d.EntityResolver)

	for name, factory := range agents.GetRegistry() {
		d.Supervisor.RegisterInternalAgent(name, factory)
	}

	// 5. Initialize the Workflow Orchestrator
	d.Orchestrator = workflows.NewOrchestrator(d.Logger, bus, d.NATS, d.JS, d.Supervisor.Queries)

	// YAML bootstrap: upsert blueprints into the DB.
	if err := d.Orchestrator.LoadFromDir(ctx, "tap/workflows"); err != nil {
		d.Logger.Warn("Orchestrator: workflow config load error (non-fatal)", "error", err)
	}

	// 6. Sync blueprints from DB and reconcile trigger stream subjects.
	if err := d.Orchestrator.SyncBlueprints(ctx); err != nil {
		return fmt.Errorf("orchestrator blueprint sync failed: %w", err)
	}

	// 8. Load Initial Agent Configuration into the supervisor
	if err := d.Supervisor.LoadAgents(d.currentConfig.Agents); err != nil {
		return err
	}

	// 9. Initialize the Almanac Registry
	d.Almanac = lookup.NewRegistry(d.Logger, d.NATS, d.JS)

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

	// Sub-system B.2: Viper Watcher (Native hot-reloading)
	g.Go(func() error {
		return d.watchWithViper(ctx)
	})

	// Sub-system C: The Supervisor (keeps agents alive)
	// Monitors health and prints heartbeats
	g.Go(func() error {
		return d.Supervisor.Run(ctx)
	})

	// Sub-system D: Global Redux Rollup State Compactor
	rollupWorker := redux.NewRollupWorker(d.Logger, d.JS, d.DBPool)
	g.Go(func() error {
		if err := rollupWorker.Start(ctx); err != nil {
			d.Logger.Error("Fatal Rollup Initialization failing bounds", "err", err)
			return err
		}
		return nil
	})

	// Sub-system E: Workflow Orchestrator
	g.Go(func() error {
		if err := d.Orchestrator.Start(ctx); err != nil {
			d.Logger.Error("Orchestrator fatal error", "err", err)
			return err
		}
		return nil
	})

	// Sub-system F: Almanac Discovery Registry
	g.Go(func() error {
		if err := d.Almanac.Start(ctx); err != nil {
			d.Logger.Error("Almanac fatal error", "err", err)
			return err
		}
		return nil
	})
 
	// Sub-system G: Workflow Directory Watcher (for hot-reloading)
	g.Go(func() error {
		return d.Orchestrator.WatchWorkflows(ctx, "tap/workflows")
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

// watchWithViper leverages native Viper file watching to reload configurations
func (d *ProtocolDaemon) watchWithViper(ctx context.Context) error {
	if d.v == nil {
		d.Logger.Warn("Viper instance missing, skipping native hot-reload")
		return nil
	}

	d.v.OnConfigChange(func(e fsnotify.Event) {
		d.Logger.Info("🔄 Config file change detected via Viper", "file", e.Name)

		newCfg, err := config.Unmarshal(d.v)
		if err != nil {
			d.Logger.Error("Failed to unmarshal updated configuration", "error", err)
			return
		}

		d.currentConfig = newCfg

		// HotLoad: The Supervisor will diff the new config against running agents
		if d.Supervisor != nil {
			if err := d.Supervisor.LoadAgents(d.currentConfig.Agents); err != nil {
				d.Logger.Error("Failed to reload agents after config change", "error", err)
			} else {
				d.Logger.Info("✅ Configuration reloaded successfully via Viper")
			}
		}
	})

	d.v.WatchConfig()
	d.Logger.Info("👁️  Viper Config Watcher active")

	<-ctx.Done()
	return nil
}

// loadConfig abstracts away the native configuration system to hot load gracefully
func (d *ProtocolDaemon) loadConfig() error {
	newCfg, v, err := d.LoadFunc()
	if err != nil {
		return fmt.Errorf("failed to load external config: %w", err)
	}

	d.currentConfig = newCfg
	d.v = v

	for i := range d.currentConfig.Agents {
		cfg := &d.currentConfig.Agents[i]
		if cfg.ActivityType == "" {
			continue
		}
		if cfg.TaskQueue != "" {
			continue
		}
		derived, err := core.NormalizeTaskQueueWithComplexity(cfg.ActivityType, "", core.ComplexityEntry)
		if err != nil {
			d.Logger.Warn("Agent config missing task_queue and normalization failed", "did", cfg.DID, "activity_type", cfg.ActivityType, "error", err)
			continue
		}
		cfg.TaskQueue = derived
		d.Logger.Warn("Agent config missing explicit task queue; derived canonical queue", "did", cfg.DID, "activity_type", cfg.ActivityType, "task_queue", cfg.TaskQueue)
	}

	// HotLoad: The Supervisor will diff the new config against running agents
	// It starts new ones, updates existing ones, and stops removed ones.
	if d.Supervisor != nil {
		if err := d.Supervisor.LoadAgents(d.currentConfig.Agents); err != nil {
			return err
		}
	}

	return nil
}
