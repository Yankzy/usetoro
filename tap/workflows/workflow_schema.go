package workflows

// WorkflowDef defines a complete declarative orchestrator definition.
type WorkflowDef struct {
	Name         string         `yaml:"name" json:"name"`
	Version      string         `yaml:"version" json:"version"`
	Description  string         `yaml:"description,omitempty" json:"description,omitempty"`
	TriggerTopic string         `yaml:"trigger_topic" json:"trigger_topic"` // The NATS subject that starts this workflow
	Steps        []WorkflowStep `yaml:"steps" json:"steps"`
}

// ActorType specifies if the processing block is an AI Agent or a persistent DB Worker.
type ActorType string

const (
	ActorTypeAgent  ActorType = "agent"
	ActorTypeWorker ActorType = "worker"
)

// WorkflowStep defines a single execution stage in the pipeline.
type WorkflowStep struct {
	ID             string `yaml:"id" json:"id"`
	ActivityType   string `yaml:"activity_type" json:"activity_type"` // e.g., agents.accounting.map_csv
	TaskQueue      string `yaml:"task_queue" json:"task_queue"`       // The public NATS topic or private Inbox for CFPs
	Negotiate      bool   `yaml:"negotiate" json:"negotiate"`         // If true, Orchestrator publishes CFP and accepts bids
	Timeout        string `yaml:"timeout" json:"timeout"`             // Duration string (e.g. "60s")
	Description    string `yaml:"description,omitempty" json:"description,omitempty"`
	WorkflowSchema string `yaml:"workflow_schema,omitempty" json:"workflow_schema,omitempty"` // Optional JSON schema hint scoped to this step
}
