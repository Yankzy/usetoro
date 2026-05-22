package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AgentRegistry holds AgentDefinitions and can resolve tool pools from them.
type AgentRegistry struct {
	mu   sync.RWMutex
	defs map[string]AgentDefinition
}

// NewAgentRegistry returns an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		defs: make(map[string]AgentDefinition),
	}
}

// Register adds an AgentDefinition. Overwrites if type already exists.
func (r *AgentRegistry) Register(def AgentDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs[def.Type] = def
}

// Get returns the AgentDefinition for a given agent type, or false if not found.
func (r *AgentRegistry) Get(agentType string) (AgentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[agentType]
	return def, ok
}

// List returns a copy of all registered definitions.
func (r *AgentRegistry) List() []AgentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentDefinition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	return out
}

// RegisterBuiltIns registers the three built-in agent definitions.
func (r *AgentRegistry) RegisterBuiltIns() {
	r.Register(AgentDefinition{
		Type:            "general-purpose",
		Tools:           []string{"*"},
		DisallowedTools: nil,
		SystemPrompt:    "You are a helpful, general-purpose AI agent. Use tools to accomplish the user's request. Break complex tasks into steps and use sub-agents when parallel exploration would help.",
	})
	r.Register(AgentDefinition{
		Type:            "explore",
		Tools:           []string{"*"},
		DisallowedTools: []string{"Agent", "FileWrite", "FileEdit"},
		SystemPrompt:    "You are a code exploration agent. Your job is to search, read, and understand code. You cannot write or edit files, and you cannot spawn sub-agents. Report your findings concisely.",
		MaxTurns:        10,
	})
	r.Register(AgentDefinition{
		Type:            "plan",
		Tools:           []string{"*"},
		DisallowedTools: []string{"Agent", "FileWrite", "FileEdit"},
		SystemPrompt:    "You are a software architect agent. Your job is to design implementation plans, identify critical files, and consider architectural trade-offs. You cannot write or edit files, and you cannot spawn sub-agents.",
		MaxTurns:        15,
	})
}

// LoadFromDir reads JSON or YAML files from a directory and registers them as AgentDefinitions.
// Files must have a .json extension and contain a single AgentDefinition or an array of them.
func (r *AgentRegistry) LoadFromDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read agent definition dir %s: %w", path, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return fmt.Errorf("read agent def file %s: %w", entry.Name(), err)
		}
		// Try array first
		var list []AgentDefinition
		if err := json.Unmarshal(data, &list); err == nil {
			for _, d := range list {
				r.Register(d)
			}
			continue
		}
		// Try single
		var single AgentDefinition
		if err := json.Unmarshal(data, &single); err != nil {
			return fmt.Errorf("parse agent def file %s: %w", entry.Name(), err)
		}
		r.Register(single)
	}
	return nil
}

// ResolveTools filters a tool map according to an AgentDefinition's allowlist/denylist.
// Pass the full tool map and optionally a set of names to always exclude (e.g., "Agent").
// Returns a new map containing only the allowed tools.
func (r *AgentRegistry) ResolveTools(def AgentDefinition, allTools map[string]Tool, alwaysExclude ...string) map[string]Tool {
	exclude := make(map[string]bool)
	for _, name := range def.DisallowedTools {
		exclude[name] = true
	}
	for _, name := range alwaysExclude {
		exclude[name] = true
	}

	// If the allowlist is ["*"], include everything not excluded.
	allowAll := len(def.Tools) == 1 && def.Tools[0] == "*"

	result := make(map[string]Tool)
	for name, tool := range allTools {
		if exclude[name] {
			continue
		}
		if allowAll {
			result[name] = tool
			continue
		}
		for _, allowed := range def.Tools {
			if allowed == name {
				result[name] = tool
				break
			}
		}
	}
	return result
}
