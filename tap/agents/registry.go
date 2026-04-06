package agents

import (
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

var (
	registry = make(map[string]func(core.Environment) core.Runnable)
)

// Register allows individual compiled agents to inject their factories.
func Register(moduleName string, factory func(core.Environment) core.Runnable) {
	registry[moduleName] = factory
}

// GetRegistry returns the read-only map of registered factories
func GetRegistry() map[string]func(core.Environment) core.Runnable {
	return registry
}
