package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Runnable interfaces all runnable agent instances (both declarative Runtime and internal modules).
type Runnable interface {
	Start() error
	Stop() error
}

// Supervisor manages the lifecycle of all active agents.
// It acts as the central Runner, supporting both generic pipeline agents and custom compiled internal agents.
type Supervisor struct {
	mu     sync.RWMutex
	agents map[string]Runnable

	logger *slog.Logger
	bus    EventBus
	mem    MemoryStore

	// Registry of internal compiled agent modules
	internalRegistry map[string]func(*slog.Logger, EventBus, AgentConfig, MemoryStore) Runnable
}

// NewSupervisor creates the control plane for the agent hive.
func NewSupervisor(logger *slog.Logger, bus EventBus, mem MemoryStore) *Supervisor {
	return &Supervisor{
		agents:           make(map[string]Runnable),
		internalRegistry: make(map[string]func(*slog.Logger, EventBus, AgentConfig, MemoryStore) Runnable),
		logger:           logger,
		bus:              bus,
		mem:              mem,
	}
}

// Bus returns the internal NATS/EventBus router securely.
func (s *Supervisor) Bus() EventBus {
	return s.bus
}

// RegisterInternalAgent binds an internal module string (from config) to a factory function.
// e.g. "cleanup-agent" -> cleanup.NewAgent
func (s *Supervisor) RegisterInternalAgent(moduleName string, factory func(*slog.Logger, EventBus, AgentConfig, MemoryStore) Runnable) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.internalRegistry[moduleName] = factory
}

// LoadAgents reconciles the desired state (configs) with the actual running state.
func (s *Supervisor) LoadAgents(configs []AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, cfg := range configs {
		if existing, exists := s.agents[cfg.DID]; exists {
			s.logger.Info("♻️ Reloading Agent", "did", cfg.DID)
			if err := existing.Stop(); err != nil {
				s.logger.Error("Failed to stop existing agent during reload", "did", cfg.DID, "error", err)
			}
			delete(s.agents, cfg.DID)
		}

		var agentInstance Runnable

		// Determine if this is an internal compiled Go agent or a generic declarative agent
		if cfg.Engine == "internal" {
			factory, ok := s.internalRegistry[cfg.InternalModule]
			if !ok {
				s.logger.Error("Unknown internal module", "module", cfg.InternalModule, "did", cfg.DID)
				continue
			}
			agentInstance = factory(s.logger, s.bus, cfg, s.mem)
		} else {
			// Declarative Runtime
			agentInstance = NewRuntime(s.logger, s.bus, cfg, s.mem)
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
		go func(d string, a Runnable) {
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
