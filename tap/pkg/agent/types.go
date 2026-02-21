package agent

import (
	"time"

	"github.com/sashabaranov/go-openai"
)

type AgentConfig struct {
	DID          string       `yaml:"did" mapstructure:"did"`
	Name         string       `yaml:"name" mapstructure:"name"`
	Model        string       `yaml:"model" mapstructure:"model"`
	Provider     string       `yaml:"provider" mapstructure:"provider"` // "openai", "google", "anthropic"
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
	History []openai.ChatCompletionMessage
}

type Subscription struct {
	Subject    string `yaml:"subject" mapstructure:"subject"`
	QueueGroup string `yaml:"queue_group" mapstructure:"queue_group"`
}

type Step struct {
	Name    string        `yaml:"name" mapstructure:"name"`
	Skill   string        `yaml:"skill" mapstructure:"skill"`     // The NATS subject for the skill (e.g. "skill.ocr")
	Timeout time.Duration `yaml:"timeout" mapstructure:"timeout"` // Parsed duration
}
