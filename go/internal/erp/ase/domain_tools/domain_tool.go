package domain_tools

import (
	"context"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

type ToolDependencies struct {
	DB        *database.Queries
	Logger    *slog.Logger
	Store   ase.StatePersister
	NC      *nats.Conn
	Runtime *agent.Runtime
}

type DomainTool interface {
	// BuildAgents extracts or queries the domain-specific data from the payload/database
	// and converts them into AutonomousSemanticEngineNode agents.
	BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error)

	// GenerateAlertPayload generates the domain-specific prompt and context map
	// to be sent to the General Agent (LLM) when an agent is stuck.
	GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error)

	// ResumeAgent reconstructs an agent from the database for resuming execution.
	ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error)

	// GetBacktrackingInstructions returns domain-specific instructions and context for the LLM during automated backtracking.
	GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (domainSystemPrompt string, userPrompt string)
}

var registry = make(map[string]DomainTool)

// Register adds a tool to the domain tool registry.
func Register(name string, t DomainTool) {
	registry[name] = t
}

// Get retrieves a tool by name from the registry.
func Get(name string) DomainTool {
	return registry[name]
}
