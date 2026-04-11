package core

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
)

// EventBus abstracts the underlying messaging system (e.g., NATS).
type EventBus interface {
	Publish(subject string, data []byte) error
	RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error)
	QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// MemoryStore abstracts the long-term memory/RAG storage.
type MemoryStore interface {
	Recall(ctx context.Context, realmID, query string) (string, error)
	Learn(ctx context.Context, realmID, trigger, instruction string) error
}

type AgentConfig struct {
	DID            string                  `yaml:"did" mapstructure:"did"`
	Name           string                  `yaml:"name" mapstructure:"name"`
	Model          string                  `yaml:"model" mapstructure:"model"`
	Engine         string                  `yaml:"engine" mapstructure:"engine"`
	InternalModule string                  `yaml:"internal_module" mapstructure:"internal_module"`
	Provider       string                  `yaml:"provider" mapstructure:"provider"`
	SystemPrompt   string                  `yaml:"system_prompt" mapstructure:"system_prompt"`
	Tools          []ToolConfig            `yaml:"tools" mapstructure:"tools"`
	Steps          []Step                  `yaml:"steps" mapstructure:"steps"`
	Dependencies   AgentDependenciesConfig `yaml:"dependencies" mapstructure:"dependencies"`

	// ActivityType is the semantic capability this agent provides (e.g. agents.accounting.map_csv).
	// Used by the Workflow Orchestrator to resolve which agent handles a given workflow step.
	ActivityType string `yaml:"activity_type" mapstructure:"activity_type"`

	// WorkflowSchema is the JSON Schema string used by the Redux engine for state validation.
	WorkflowSchema string `yaml:"workflow_schema" mapstructure:"workflow_schema"`

	// TaskQueue is the public NATS subject the Orchestrator assigns to this agent's activity_type.
	// Populated at runtime by the Orchestrator after loading workflow definitions — not set in defaults.yaml.
	TaskQueue string `yaml:"-" mapstructure:"-"`

	// QueueGroup and DurableName are derived at runtime from the DID. Not set in defaults.yaml.
	QueueGroup  string `yaml:"-" mapstructure:"-"`
	DurableName string `yaml:"-" mapstructure:"-"`
}

type AgentDependenciesConfig struct {
	Database       bool `yaml:"database" mapstructure:"database"`           // populates env.DBPool (*pgxpool.Pool)
	DBQueries      bool `yaml:"db_queries" mapstructure:"db_queries"`       // populates env.DB (*database.Queries)
	EntityResolver bool `yaml:"entity_resolver" mapstructure:"entity_resolver"`
}

type ToolConfig struct {
	Name        string `yaml:"name" mapstructure:"name"`
	Subject     string `yaml:"subject" mapstructure:"subject"`
	Description string `yaml:"description" mapstructure:"description"`
}

type State struct{}

type Step struct {
	Name    string        `yaml:"name" mapstructure:"name"`
	Skill   string        `yaml:"skill" mapstructure:"skill"`
	Timeout time.Duration `yaml:"timeout" mapstructure:"timeout"`
}

// Runnable interfaces all runnable agent instances (both declarative Runtime and internal modules).
type Runnable interface {
	Start() error
	Stop() error
}

// Environment encapsulates all cross-cutting infrastructure injected into internal agents.
type Environment struct {
	Logger         *slog.Logger
	Bus            EventBus
	Config         AgentConfig
	Memory         MemoryStore
	Queries        *database.Queries // populated when db_queries: true in YAML
	DBPool         *pgxpool.Pool     // populated when database: true in YAML
	EntityResolver *ai.EntityResolver
}
