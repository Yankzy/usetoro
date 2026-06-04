package ase

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// ASEConfig holds the unified configuration for the ASE layer.
type ASEConfig struct {
	Prompts         map[string]string `mapstructure:"prompts"`
	DAG             DAGConfig         `mapstructure:"dag"`
	HyperParameters HyperParameters   `mapstructure:"hyper_parameters"`
}

// HyperParameters contains dynamically tunable variables that control ASE execution behavior.
type HyperParameters struct {
	ConfidenceThreshold   float64            `mapstructure:"confidence_threshold"`
	AutoAdvance           bool               `mapstructure:"auto_advance"`
	MaxLLMRetries         int                `mapstructure:"max_llm_retries"`
	LLMTimeoutSeconds     int                `mapstructure:"llm_timeout_seconds"`
	BatchFlushSeconds     int                `mapstructure:"batch_flush_seconds"`
	ActiveAgentTTLMinutes int                `mapstructure:"active_agent_ttl_minutes"`
	LockTTLSeconds        int                `mapstructure:"lock_ttl_seconds"`
	VectorMemory          VectorMemoryConfig `mapstructure:"vector_memory"`
}

// VectorMemoryConfig controls the ASE semantic retrieval layer.
// All fields are hot-reloadable via ase*.yml without a service restart.
type VectorMemoryConfig struct {
	// Enabled is the master switch for semantic retrieval.
	// When false, the VectorStore is bypassed entirely in batchToRows.
	Enabled bool `mapstructure:"enabled"`

	// EmbeddingProvider selects the embedding backend: "openai" or "google".
	EmbeddingProvider string `mapstructure:"embedding_provider"`

	// OpenAIEmbeddingModel sets the OpenAI embedding model name.
	// Only used when EmbeddingProvider = "openai".
	OpenAIEmbeddingModel string `mapstructure:"openai_embedding_model"`

	// EmbeddingDimensions is the vector size the model produces.
	// Must match the column definition. Changing this requires a migration.
	EmbeddingDimensions int `mapstructure:"embedding_dimensions"`

	// ScaNNNumLeaves controls the num_leaves tuning knob for the ScaNN index.
	// Best practice: sqrt(row_count). Cold-start default is 10.
	ScaNNNumLeaves int `mapstructure:"scann_num_leaves"`

	// RetrievalTopK is the number of semantically similar entries fetched
	// per transaction and injected as company_rules into the LLM prompt.
	RetrievalTopK int `mapstructure:"retrieval_top_k"`

	// HydratorIntervalSeconds is how often the background VectorHydrator ticks.
	HydratorIntervalSeconds int `mapstructure:"hydrator_interval_seconds"`

	// HydratorMinConfidence is the minimum unified confidence required for a
	// resolved staging_transaction to be eligible for vector hydration.
	HydratorMinConfidence float64 `mapstructure:"hydrator_min_confidence"`

	// HydratorBatchSize is the max rows processed per hydrator tick.
	HydratorBatchSize int `mapstructure:"hydrator_batch_size"`
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
	AutoAdvance         *bool             `mapstructure:"auto_advance"`
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
	tenantConfigsMu sync.RWMutex
	tenantConfigs   = make(map[string]*ASEConfig)

	onConfigLoaded func(key string, cfg *ASEConfig)
)

// SetOnConfigLoaded sets a callback to be invoked when a config is loaded or hot-reloaded.
func SetOnConfigLoaded(cb func(key string, cfg *ASEConfig)) {
	tenantConfigsMu.Lock()
	defer tenantConfigsMu.Unlock()
	onConfigLoaded = cb
}

// GetAllConfigs returns a map of all currently loaded configurations.
func GetAllConfigs() map[string]*ASEConfig {
	tenantConfigsMu.RLock()
	defer tenantConfigsMu.RUnlock()
	
	configs := make(map[string]*ASEConfig)
	for k, v := range tenantConfigs {
		configs[k] = v
	}
	return configs
}

// GetConfig returns the ASE configuration for a specific tenant or realm, with fallback to default.
func GetConfig(tenantID, realmID string) *ASEConfig {
	tenantConfigsMu.RLock()
	defer tenantConfigsMu.RUnlock()

	if tenantID != "" {
		if cfg, ok := tenantConfigs["tenant_"+tenantID]; ok {
			return cfg
		}
	}
	if realmID != "" {
		if cfg, ok := tenantConfigs["realm_"+realmID]; ok {
			return cfg
		}
	}
	return tenantConfigs["default"]
}

// GetPrompt returns a prompt by key from the given configuration context.
func GetPrompt(tenantID, realmID, key string) string {
	cfg := GetConfig(tenantID, realmID)
	if cfg == nil || cfg.Prompts == nil {
		return ""
	}
	return cfg.Prompts[key]
}

// setConfig updates a specific tenant config securely.
func setConfig(key string, cfg *ASEConfig) {
	tenantConfigsMu.Lock()
	tenantConfigs[key] = cfg
	cb := onConfigLoaded
	tenantConfigsMu.Unlock()
	
	if cb != nil {
		cb(key, cfg)
	}
}

// parseConfig loads a single YAML file and returns the ASEConfig.
func parseConfig(file string) (*ASEConfig, error) {
	v := viper.New()
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	
	// Initialize defaults before unmarshaling
	cfg := ASEConfig{
		HyperParameters: HyperParameters{
			ConfidenceThreshold:   0.98,
			AutoAdvance:           true,
			MaxLLMRetries:         3,
			LLMTimeoutSeconds:     120,
			BatchFlushSeconds:     5,
			ActiveAgentTTLMinutes: 10,
			LockTTLSeconds:        30,
			VectorMemory: VectorMemoryConfig{
				Enabled:                 false,
				EmbeddingProvider:       "openai",
				OpenAIEmbeddingModel:    "text-embedding-3-small",
				EmbeddingDimensions:     1536,
				ScaNNNumLeaves:          10,
				RetrievalTopK:           5,
				HydratorIntervalSeconds: 30,
				HydratorMinConfidence:   0.98,
				HydratorBatchSize:       50,
			},
		},
	}
	
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// InitConfig scans for ase*.yml files and sets up a directory watcher for hot-reloading.
func InitConfig(logger *slog.Logger) error {
	paths := []string{"./go/internal/erp/ase", "./internal/erp/ase", "."}
	var configDir string
	for _, p := range paths {
		if stat, err := os.Stat(p); err == nil && stat.IsDir() {
			configDir = p
			break
		}
	}

	if configDir == "" {
		return fmt.Errorf("could not find ASE config directory")
	}

	// Initial scan for ase*.yml
	files, err := filepath.Glob(filepath.Join(configDir, "ase*.yml"))
	if err != nil {
		return fmt.Errorf("failed to glob ase*.yml files: %w", err)
	}

	for _, file := range files {
		cfg, err := parseConfig(file)
		if err != nil {
			if logger != nil {
				logger.Error("failed to load initial config", "file", file, "error", err)
			}
			continue
		}
		
		key := extractConfigKey(filepath.Base(file), "ase")
		setConfig(key, cfg)
		if logger != nil {
			logger.Info("✅ ASE config loaded", "key", key, "file", file)
		}
	}

	// Setup Hot-Reloading using fsnotify on the directory
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	if err := watcher.Add(configDir); err != nil {
		return fmt.Errorf("failed to watch config directory: %w", err)
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only process Create, Write, or Rename events for ase*.yml files
				if (event.Op&(fsnotify.Write|fsnotify.Create) != 0) && strings.HasPrefix(filepath.Base(event.Name), "ase") && strings.HasSuffix(event.Name, ".yml") {
					if logger != nil {
						logger.Info("🔄 ASE config change detected, hot-reloading", "file", event.Name)
					}
					
					// Slight delay to allow file write to finish
					time.Sleep(100 * time.Millisecond)
					
					cfg, err := parseConfig(event.Name)
					if err != nil {
						if logger != nil {
							logger.Error("Failed to unmarshal updated config", "file", event.Name, "error", err)
						}
						continue
					}
					
					key := extractConfigKey(filepath.Base(event.Name), "ase")
					setConfig(key, cfg)
					if logger != nil {
						logger.Info("✅ ASE config hot-reloaded successfully", "key", key)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if logger != nil {
					logger.Error("fsnotify watcher error", "error", err)
				}
			}
		}
	}()

	return nil
}

// extractConfigKey derives the map key from the filename (e.g., ase_tenant_A.yml -> tenant_A, ase.yml -> default)
func extractConfigKey(filename, prefix string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	if name == prefix {
		return "default"
	}
	// e.g. "ase_tenant_123" -> "tenant_123"
	return strings.TrimPrefix(name, prefix+"_")
}
