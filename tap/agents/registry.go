package agents

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/tap/agents/cleanup"
	"github.com/Yankzy/usetoro/tap/agents/reconcile_expense"
	"github.com/Yankzy/usetoro/tap/agents/reconcile_revenue"
	"github.com/Yankzy/usetoro/tap/agents/stripe_processor"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
)

// AgentDependencies bundles all cross-cutting infrastructure clients required 
// to construct specialized Toro agents, circumventing param explosion.
type AgentDependencies struct {
	Ctx            context.Context
	DBPool         *pgxpool.Pool
	WalletManager  *micrion.WalletManager
	EntityResolver *ai.EntityResolver
}

// RegisterAll defines the centralized Factory plugin matrix. 
// This protects the generic Daemon network loop from importing individual discrete business logic pipelines.
func RegisterAll(supervisor *agent.Supervisor, deps AgentDependencies) {
	ensureAgentFunded := func(did string) {
		bal, err := deps.WalletManager.GetAggregateBalance(context.Background(), did)
		if err != nil || bal < 1_000_000 {
			_ = deps.WalletManager.TopUp(did, 5_000_000, "SYSTEM")
		}
	}

	supervisor.RegisterInternalAgent("cleanup-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		ensureAgentFunded(c.DID)
		tolledBus := micrion.NewTolledEventBus(b, deps.WalletManager, c.DID)
		return cleanup.NewAgent(l, tolledBus, c, m, database.New(deps.DBPool))
	})

	supervisor.RegisterInternalAgent("reconcile-expense-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		ensureAgentFunded(c.DID)
		tolledBus := micrion.NewTolledEventBus(b, deps.WalletManager, c.DID)
		tolledDBTX := micrion.NewTolledDBTX(deps.DBPool, deps.WalletManager, c.DID)
		return reconcile_expense.NewAgent(l, tolledBus, c, m, database.New(tolledDBTX), deps.EntityResolver)
	})

	supervisor.RegisterInternalAgent("reconcile-revenue-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		ensureAgentFunded(c.DID)
		tolledBus := micrion.NewTolledEventBus(b, deps.WalletManager, c.DID)
		tolledDBTX := micrion.NewTolledDBTX(deps.DBPool, deps.WalletManager, c.DID)
		return reconcile_revenue.NewAgent(l, tolledBus, c, m, database.New(tolledDBTX), deps.EntityResolver)
	})

	supervisor.RegisterInternalAgent("stripe-processor", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		return stripe_processor.NewAgent(l, b, c, m, deps.WalletManager, database.New(deps.DBPool))
	})
}
