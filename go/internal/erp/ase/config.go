package ase

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// ASEConfig holds the unified configuration for the ASE layer.
type ASEConfig struct {
	Prompts map[string]string `mapstructure:"prompts"`
	DAG     DAGConfig         `mapstructure:"dag"`
}

// DAGConfig represents the DAG topology configuration.
type DAGConfig struct {
	EntryNode string                   `mapstructure:"entry_node"`
	Nodes     map[string]DAGNodeConfig `mapstructure:"nodes"`
}

// DAGNodeConfig defines a single node in the DAG topology.
type DAGNodeConfig struct {
	Kind                string            `mapstructure:"kind"`
	Name                string            `mapstructure:"name"`
	BatchSize           int               `mapstructure:"batch_size"`
	BatchFlushSeconds   int               `mapstructure:"batch_flush_seconds"`
	PromptKey           string            `mapstructure:"prompt_key"`
	EdgeType            string            `mapstructure:"edge_type"`
	DynamicEdgeProvider string            `mapstructure:"dynamic_edge_provider"`
	HoldStateSignal     string            `mapstructure:"hold_state_signal"`
	HoldReasonString    string            `mapstructure:"hold_reason_string"`
	ResumeChild         string            `mapstructure:"resume_child"`
	Children            map[string]string `mapstructure:"children"`
	DefaultChild        string            `mapstructure:"default_child"`
	ExecutionParams     map[string]string `mapstructure:"execution_parameters"`
}

var (
	globalConfigMu sync.RWMutex
	globalConfig   *ASEConfig
)

// GetConfig returns the full current hot-reloaded ASE configuration.
func GetConfig() *ASEConfig {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	return globalConfig
}

// GetPrompt returns a prompt by key from the current hot-reloaded configuration.
func GetPrompt(key string) string {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	if globalConfig == nil || globalConfig.Prompts == nil {
		return ""
	}
	return globalConfig.Prompts[key]
}

// setConfig updates the global config securely.
func setConfig(cfg *ASEConfig) {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()
	globalConfig = cfg
}

// InitConfig loads ase.yml and sets up a fsnotify watcher for hot-reloading.
func InitConfig(logger *slog.Logger) error {
	v := viper.New()
	v.SetConfigName("ase")
	v.SetConfigType("yml")
	// Add paths to search for the config file in
	v.AddConfigPath("./go/internal/erp/ase")
	v.AddConfigPath("./internal/erp/ase")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read ase.yml: %w", err)
	}

	var cfg ASEConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("failed to unmarshal ase config: %w", err)
	}
	setConfig(&cfg)

	if logger != nil {
		logger.Info("✅ ASE config loaded successfully")
	}

	// Setup Hot-Reloading
	v.OnConfigChange(func(e fsnotify.Event) {
		if logger != nil {
			logger.Info("🔄 ase.yml change detected, hot-reloading ASE config", "file", e.Name)
		}
		var newCfg ASEConfig
		if err := v.Unmarshal(&newCfg); err != nil {
			if logger != nil {
				logger.Error("Failed to unmarshal updated ase.yml", "error", err)
			}
			return
		}
		setConfig(&newCfg)
		if logger != nil {
			logger.Info("✅ ASE config hot-reloaded successfully")
		}
	})
	v.WatchConfig()

	return nil
}
