package agent

import (
	"time"
)

type AgentConfig struct {
	DID          string       `yaml:"did" mapstructure:"did"`
	Name         string       `yaml:"name" mapstructure:"name"`
	Model          string       `yaml:"model" mapstructure:"model"`
	Engine         string       `yaml:"engine" mapstructure:"engine"`
	InternalModule string       `yaml:"internal_module" mapstructure:"internal_module"`
	Provider       string       `yaml:"provider" mapstructure:"provider"`
	SystemPrompt string       `yaml:"system_prompt" mapstructure:"system_prompt"`
	Tools        []ToolConfig `yaml:"tools" mapstructure:"tools"`
	Subscription Subscription `yaml:"subscription" mapstructure:"subscription"`
	Steps        []Step       `yaml:"steps" mapstructure:"steps"`
}

type ToolConfig struct {
	Name        string `yaml:"name" mapstructure:"name"`
	Subject     string `yaml:"subject" mapstructure:"subject"`
	Description string `yaml:"description" mapstructure:"description"`
}

type State struct {
	// Refactored: We no longer track sashabaranov ChatCompletionMessage history
	// Instead, the static openai-go/v3 Exec wrapper handles state generation where applicable.
}

type Subscription struct {
	Subject    string `yaml:"subject" mapstructure:"subject"`
	QueueGroup string `yaml:"queue_group" mapstructure:"queue_group"`
}

type Step struct {
	Name    string        `yaml:"name" mapstructure:"name"`
	Skill   string        `yaml:"skill" mapstructure:"skill"`
	Timeout time.Duration `yaml:"timeout" mapstructure:"timeout"`
}
