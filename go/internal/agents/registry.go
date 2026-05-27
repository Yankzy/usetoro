package agents

import (
	"github.com/Yankzy/usetoro/internal/config"
)

// AgentAlias maps directly to the configuration type
type AgentAlias = config.AgentAlias

// Lookup returns the agent alias config for a given mailbox name from the hot-reloaded global configuration, or nil if unknown.
func Lookup(alias string) *AgentAlias {
	cfg := config.GetGlobal()
	if cfg == nil {
		return nil
	}
	if a, ok := cfg.VirtualEmployees[alias]; ok {
		return &a
	}
	return nil
}
