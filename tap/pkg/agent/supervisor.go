package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Supervisor manages the lifecycle of all active agents.
// It acts as the central Runner, supporting both generic pipeline agents and custom compiled internal agents.
type Supervisor struct {
	mu     sync.RWMutex
	agents map[string]core.Runnable

	logger  *slog.Logger
	bus     core.EventBus
	mem     core.MemoryStore
	dbPool  *pgxpool.Pool
	er      *ai.EntityResolver
	Queries *database.Queries
	Mailpool *mailpool.Mailpool

	// Registry of internal compiled agent modules
	internalRegistry map[string]func(core.Environment) core.Runnable
}

// NewSupervisor creates the control plane for the agent hive.
func NewSupervisor(logger *slog.Logger, bus core.EventBus, mem core.MemoryStore, dbPool *pgxpool.Pool, er *ai.EntityResolver, mp *mailpool.Mailpool) *Supervisor {
	return &Supervisor{
		agents:           make(map[string]core.Runnable),
		internalRegistry: make(map[string]func(core.Environment) core.Runnable),
		logger:           logger,
		bus:              bus,
		mem:              mem,
		dbPool:           dbPool,
		Queries:          database.New(dbPool),
		er:               er,
		Mailpool:         mp,
	}
}

// Bus returns the internal NATS/EventBus router securely.
func (s *Supervisor) Bus() core.EventBus {
	return s.bus
}

// RegisterInternalAgent binds an internal module string (from config) to a factory function.
func (s *Supervisor) RegisterInternalAgent(moduleName string, factory func(core.Environment) core.Runnable) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.internalRegistry[moduleName] = factory
}

// LoadAgents reconciles the desired state (configs) with the actual running state.
func (s *Supervisor) LoadAgents(configs []core.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, cfg := range configs {
		if cfg.ActivityType != "" {
			normalizedQueue, err := core.NormalizeTaskQueue(cfg.ActivityType, cfg.TaskQueue)
			if err != nil {
				s.logger.Warn("Invalid task queue configuration, skipping agent",
					"did", cfg.DID,
					"activity_type", cfg.ActivityType,
					"task_queue", cfg.TaskQueue,
					"error", err,
				)
				continue
			}
			cfg.TaskQueue = normalizedQueue
		}

		if existing, exists := s.agents[cfg.DID]; exists {
			s.logger.Info("♻️ Reloading Agent", "did", cfg.DID)
			if err := existing.Stop(); err != nil {
				s.logger.Error("Failed to stop existing agent during reload", "did", cfg.DID, "error", err)
			}
			delete(s.agents, cfg.DID)
		}

		var agentInstance core.Runnable

		// Determine if this is an internal compiled Go agent or a generic declarative agent
		if cfg.Engine == "internal" {
			factory, ok := s.internalRegistry[cfg.InternalModule]
			if !ok {
				s.logger.Error("Unknown internal module", "module", cfg.InternalModule, "did", cfg.DID)
				continue
			}

			env := core.Environment{
				Logger:   s.logger,
				Bus:      s.bus,
				Config:   cfg,
				Memory:   s.mem,
				Mailpool: s.Mailpool,
			}

			if cfg.Dependencies.Database {
				env.DBPool = s.dbPool
			}
			if cfg.Dependencies.DBQueries {
				env.Queries = database.New(s.dbPool)
			}
			if cfg.Dependencies.EntityResolver {
				env.EntityResolver = s.er
			}

			agentInstance = factory(env)
		} else {
			agentInstance = NewRuntime(s.logger, s.bus, cfg)
		}

		// Start the Agent (Non-blocking)
		if err := agentInstance.Start(); err != nil {
			s.logger.Error("❌ Failed to start agent", "did", cfg.DID, "error", err)
			continue
		}

		s.agents[cfg.DID] = agentInstance
		s.logger.Info("✅ Agent Active", "did", cfg.DID, "role", cfg.Name)
	}

	return nil
}

// Run starts the supervisor's monitoring heartbeat.
func (s *Supervisor) Run(ctx context.Context) error {
	s.logger.Info("👀 Supervisor Monitoring Active")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.mu.RLock()
			s.mu.RUnlock()

			// count := len(s.agents)
			// s.logger.Info("Creating Heartbeat", "active_agents", count)
		}
	}
}

// Shutdown gracefully stops all running agents.
func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("🔻 Supervisor shutting down all agents...")

	var wg sync.WaitGroup
	for did, agentInstance := range s.agents {
		wg.Add(1)
		go func(d string, a core.Runnable) {
			defer wg.Done()
			if err := a.Stop(); err != nil {
				s.logger.Error("Error stopping agent", "did", d, "error", err)
			}
		}(did, agentInstance)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("✅ All agents stopped gracefully")
	case <-ctx.Done():
		s.logger.Warn("⚠️ Shutdown timed out, forcing exit")
	}
}
