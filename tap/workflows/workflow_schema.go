package workflows

import "github.com/Yankzy/usetoro/tap/pkg/core"

// WorkflowDef defines a complete declarative orchestrator definition.
type WorkflowDef struct {
	Name         string         `yaml:"name" json:"name" mapstructure:"name"`
	Version      string         `yaml:"version" json:"version" mapstructure:"version"`
	Description  string         `yaml:"description,omitempty" json:"description,omitempty" mapstructure:"description"`
	TriggerTopic string         `yaml:"trigger_topic" json:"trigger_topic" mapstructure:"trigger_topic"` // The NATS subject that starts this workflow
	Steps        []WorkflowStep `yaml:"steps" json:"steps" mapstructure:"steps"`
}

// ActorType specifies if the processing block is an AI Agent or a persistent DB Worker.
type ActorType string

const (
	ActorTypeAgent  ActorType = "agent"
	ActorTypeWorker ActorType = "worker"
	// ActorTypeHITL is a built-in Orchestrator primitive — no external dispatch.
	ActorTypeHITL ActorType = "hitl"
)

// WorkflowStep defines a single execution stage in the pipeline.
type WorkflowStep struct {
	ID             string              `yaml:"id" json:"id" mapstructure:"id"`
	ActivityType   string              `yaml:"activity_type" json:"activity_type" mapstructure:"activity_type"` // e.g., agents.accounting.map_csv
	TaskQueue      string              `yaml:"task_queue" json:"task_queue" mapstructure:"task_queue"`          // The public NATS topic or private Inbox for CFPs
	Complexity     core.TaskComplexity `yaml:"complexity" json:"complexity" mapstructure:"complexity"`
	Negotiate      bool                `yaml:"negotiate" json:"negotiate" mapstructure:"negotiate"` // If true, Orchestrator publishes CFP and accepts bids
	Timeout        string              `yaml:"timeout" json:"timeout" mapstructure:"timeout"`       // Duration string (e.g. "60s")
	Description    string              `yaml:"description,omitempty" json:"description,omitempty" mapstructure:"description"`
	WorkflowSchema string              `yaml:"workflow_schema,omitempty" json:"workflow_schema,omitempty" mapstructure:"workflow_schema"` // Optional JSON schema hint scoped to this step
	SystemPrompt   string              `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty" mapstructure:"system_prompt"`       // Optional system prompt override
	RBACPolicy     []string            `yaml:"rbac_policy,omitempty" json:"rbac_policy,omitempty" mapstructure:"rbac_policy"`             // Allowed Redux path prefixes

	// DAG fields — allow branching, fan-in, and nested workflows.
	DependsOn   []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty" mapstructure:"depends_on"`
	OnSuccess   []string `yaml:"on_success,omitempty" json:"on_success,omitempty" mapstructure:"on_success"`
	OnFailure   []string `yaml:"on_failure,omitempty" json:"on_failure,omitempty" mapstructure:"on_failure"`
	SubWorkflow string   `yaml:"sub_workflow,omitempty" json:"sub_workflow,omitempty" mapstructure:"sub_workflow"`

	// Generic routing helpers.
	Config               map[string]interface{} `yaml:"config,omitempty" json:"config,omitempty" mapstructure:"config"`
	IncludeHistory       bool                   `yaml:"include_history,omitempty" json:"include_history,omitempty" mapstructure:"include_history"`
	RouteCondition       *RouteCondition        `yaml:"route_condition,omitempty" json:"route_condition,omitempty" mapstructure:"route_condition"`
	SuspendRoutes        []int                  `yaml:"suspend_routes,omitempty" json:"suspend_routes,omitempty" mapstructure:"suspend_routes"`
	SuspensionReasonPath string                 `yaml:"suspension_reason_path,omitempty" json:"suspension_reason_path,omitempty" mapstructure:"suspension_reason_path"`
}

type RouteCondition struct {
	StepID string `yaml:"step_id,omitempty" json:"step_id,omitempty" mapstructure:"step_id"`
	Values []int  `yaml:"values,omitempty" json:"values,omitempty" mapstructure:"values"`
}
