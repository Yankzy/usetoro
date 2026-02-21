package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/llm"
)

// Supervisor manages the lifecycle of all active agents.
// It ensures that agents are started, stopped, and updated safely.
type Supervisor struct {
	mu     sync.RWMutex
	agents map[string]*Runtime

	logger *slog.Logger
	bus    EventBus
	mem    MemoryStore
}

// NewSupervisor creates the control plane for the agent hive.
// It requires the EventBus (nervous system) and Memory Store (long-term storage).
func NewSupervisor(logger *slog.Logger, bus EventBus, mem MemoryStore) *Supervisor {
	return &Supervisor{
		agents: make(map[string]*Runtime),
		logger: logger,
		bus:    bus,
		mem:    mem,
	}
}

// LoadAgents reconciles the desired state (configs) with the actual running state.
// It is idempotent: calling it with the same config does nothing.
// Calling it with updated config restarts the specific agent.
func (s *Supervisor) LoadAgents(configs []AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, cfg := range configs {
		// Check if agent is already running
		if existing, exists := s.agents[cfg.DID]; exists {
			s.logger.Info("♻️ Reloading Agent", "did", cfg.DID)
			if err := existing.Stop(); err != nil {
				s.logger.Error("Failed to stop existing agent during reload", "did", cfg.DID, "error", err)
			}
			delete(s.agents, cfg.DID)
		}

		// Initialize the LLM Client using the Factory
		// We pass empty string for apiKey to let the factory/adapter resolve it from ENV based on provider
		// Or we could pass specific keys if we had them in config.
		llmClient, err := llm.GetClient(cfg.Provider, "")
		if err != nil {
			s.logger.Error("Failed to create LLM client", "did", cfg.DID, "provider", cfg.Provider, "error", err)
			continue
		}

		// Initialize the Agent Runtime
		rt := NewRuntime(s.logger, s.bus, cfg, llmClient, s.mem)

		// Start the Agent (Non-blocking)
		if err := rt.Start(); err != nil {
			s.logger.Error("❌ Failed to start agent", "did", cfg.DID, "error", err)
			// We continue loading other agents even if one fails
			continue
		}

		s.agents[cfg.DID] = rt
		s.logger.Info("✅ Agent Active", "did", cfg.DID, "role", cfg.Name)
	}

	return nil
}

// Run starts the supervisor's monitoring loop.
// It blocks until the context is canceled.
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
			count := len(s.agents)
			s.mu.RUnlock()

			s.logger.Info("Creating Heartbeat", "active_agents", count)
			// Future: Add deeper health checks here (e.g., ping agents, check NATS lag)
		}
	}
}

// Shutdown gracefully stops all running agents, ensuring in-flight tasks
// have a chance to complete (up to the context deadline).
func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("🔻 Supervisor shutting down all agents...")

	var wg sync.WaitGroup
	for did, agent := range s.agents {
		wg.Add(1)
		go func(d string, a *Runtime) {
			defer wg.Done()
			if err := a.Stop(); err != nil {
				s.logger.Error("Error stopping agent", "did", d, "error", err)
			}
		}(did, agent)
	}

	// Wait for cleanup or context timeout
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
