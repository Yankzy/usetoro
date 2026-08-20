package builtin

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// ToolFactory is a function that instantiates a tool with the necessary environment and logger.
type ToolFactory func(env core.Environment, logger *slog.Logger) tools.Tool

var (
	registryMu sync.RWMutex
	registry   = make(map[string]ToolFactory)
)

// Register records a tool factory by name. It is typically called from init() functions.
func Register(name string, factory ToolFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// GetTool instantiates a tool by name if it exists in the registry.
func GetTool(name string, env core.Environment, logger *slog.Logger) tools.Tool {
	registryMu.RLock()
	factory, exists := registry[name]
	if !exists {
		// Fallback to case-insensitive and normalized (ignoring underscores and hyphens) matching
		normTarget := normalizeToolName(name)
		for k, f := range registry {
			if normalizeToolName(k) == normTarget {
				factory = f
				exists = true
				break
			}
		}
	}
	registryMu.RUnlock()
	
	if !exists {
		return nil
	}
	return factory(env, logger)
}

func normalizeToolName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}
