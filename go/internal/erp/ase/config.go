package ase

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/database"
)

// ASEConfig holds the unified configuration for the ASE layer.
type ASEConfig struct {
	Prompts         map[string]any    `json:"prompts"`
	DAG             DAGConfig         `json:"dag"`
	HyperParameters HyperParameters   `json:"hyper_parameters"`
}

// HyperParameters contains dynamically tunable variables that control ASE execution behavior.
type HyperParameters struct {
	ConfidenceThreshold   float64            `json:"confidence_threshold"`
	AutoAdvance           bool               `json:"auto_advance"`
	MaxLLMRetries         int                `json:"max_llm_retries"`
	LLMTimeoutSeconds     int                `json:"llm_timeout_seconds"`
	BatchFlushSeconds     int                `json:"batch_flush_seconds"`
	ActiveAgentTTLMinutes int                `json:"active_agent_ttl_minutes"`
	LockTTLSeconds        int                `json:"lock_ttl_seconds"`
}

// VectorMemoryConfig controls the ASE semantic retrieval layer.
type VectorMemoryConfig struct {
	Enabled                 bool    `json:"enabled"`
	EmbeddingProvider       string  `json:"embedding_provider"`
	OpenAIEmbeddingModel    string  `json:"openai_embedding_model"`
	EmbeddingDimensions     int     `json:"embedding_dimensions"`
	ScaNNNumLeaves          int     `json:"scann_num_leaves"`
	RetrievalTopK           int     `json:"retrieval_top_k"`
	HydratorIntervalSeconds int     `json:"hydrator_interval_seconds"`
	HydratorMinConfidence   float64 `json:"hydrator_min_confidence"`
	HydratorBatchSize       int     `json:"hydrator_batch_size"`
}

// DAGConfig represents the DAG topology configuration.
type DAGConfig struct {
	EntryNode string                   `json:"entry_node"`
	Nodes     map[string]DAGNodeConfig `json:"nodes"`
}

// DAGNodeConfig defines a single node in the DAG topology.
type DAGNodeConfig struct {
	Kind                string            `json:"kind"`
	Name                string            `json:"name"`
	AutoAdvance         *bool             `json:"auto_advance"`
	BatchSize           int               `json:"batch_size"`
	BatchFlushSeconds   int               `json:"batch_flush_seconds"`
	PromptKey           string            `json:"prompt_key"`
	EdgeType            string            `json:"edge_type"`
	DynamicEdgeProvider string            `json:"dynamic_edge_provider"`
	HoldStateSignal     string            `json:"hold_state_signal"`
	HoldReasonString    string            `json:"hold_reason_string"`
	ResumeChild         string            `json:"resume_child"`
	Children            map[string]string `json:"children"`
	DefaultChild        string            `json:"default_child"`
	ExecutionParams     map[string]string `json:"execution_parameters"`
}

var (
	dbQueries      *database.Queries
	redisClient    *redis.Client
	configCache    *expirable.LRU[string, *ASEConfig]
	logger         *slog.Logger
	onConfigLoaded func(key string, cfg *ASEConfig)
	
	systemVectorConfigMu sync.RWMutex
	systemVectorConfig   *VectorMemoryConfig
)

// SetOnConfigLoaded sets a callback to be invoked when a config is loaded or hot-reloaded.
func SetOnConfigLoaded(cb func(key string, cfg *ASEConfig)) {
	onConfigLoaded = cb
}

// InitConfig initializes the DB and Redis dependencies, and the LRU cache.
func InitConfig(db *database.Queries, rc *redis.Client, l *slog.Logger) error {
	dbQueries = db
	redisClient = rc
	logger = l
	configCache = expirable.NewLRU[string, *ASEConfig](1000, nil, time.Minute*60)

	if rc != nil {
		go listenForConfigUpdates()
	}

	return nil
}

// GetAllConfigs is deprecated for the DB flow but kept to satisfy existing callers if needed.
func GetAllConfigs() map[string]*ASEConfig {
	return nil
}

func parseDBRow(dagConfig []byte, hyperParams []byte, prompts []byte) (*ASEConfig, error) {
	var cfg ASEConfig
	if len(dagConfig) > 0 {
		if err := json.Unmarshal(dagConfig, &cfg.DAG); err != nil {
			return nil, err
		}
	}
	if len(hyperParams) > 0 {
		if err := json.Unmarshal(hyperParams, &cfg.HyperParameters); err != nil {
			return nil, err
		}
	}
	if len(prompts) > 0 {
		if err := json.Unmarshal(prompts, &cfg.Prompts); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

// GetConfig returns the ASE configuration for a specific tenant or realm.
// If dagName is empty, it returns nil instead of defaulting.
// If no tenant-specific or realm-specific config is found in the database,
// it falls back hierarchically to realm-specific and then global configuration.
func GetConfig(tenantID, realmID, dagName string) *ASEConfig {
	ctx := context.Background()

	if dagName == "" {
		if logger != nil {
			logger.Error("GetConfig called with empty dagName")
		}
		return nil
	}

	key := "tenant_" + tenantID + "_" + dagName
	if tenantID == "" {
		if realmID != "" {
			key = "realm_" + realmID + "_" + dagName
		} else {
			key = "global_" + dagName
		}
	}

	// 1. Check Cache
	if configCache != nil {
		if cfg, ok := configCache.Get(key); ok {
			return cfg
		}
	}

	// 2. Load from DB
	if dbQueries == nil {
		if logger != nil {
			logger.Error("GetConfig called but dbQueries is nil")
		}
		return nil
	}

	var dbRow database.ToroCoreAseDag
	var err error
	loaded := false

	isNoRows := func(e error) bool {
		if e == nil {
			return false
		}
		return e.Error() == "no rows in result set" || strings.Contains(e.Error(), "no rows")
	}

	// 1. Try Tenant-specific config
	if tenantID != "" {
		uid, parseErr := uuid.Parse(tenantID)
		if parseErr == nil {
			dbRow, err = dbQueries.GetASEConfigByTenant(ctx, database.GetASEConfigByTenantParams{
				TenantID: pgtype.UUID{Bytes: uid, Valid: true},
				Name:     dagName,
			})
			if err == nil {
				loaded = true
			} else if !isNoRows(err) {
				if logger != nil {
					logger.Warn("failed to load tenant ASE config from db", "key", key, "error", err)
				}
				return nil
			}
		} else {
			if logger != nil {
				logger.Warn("invalid tenant UUID, skipping tenant config", "tenant_id", tenantID, "error", parseErr)
			}
		}
	}

	// 2. Try Realm-specific config
	if !loaded && realmID != "" {
		dbRow, err = dbQueries.GetASEConfigByRealm(ctx, database.GetASEConfigByRealmParams{
			RealmID: pgtype.Text{String: realmID, Valid: true},
			Name:    dagName,
		})
		if err == nil {
			loaded = true
		} else if !isNoRows(err) {
			if logger != nil {
				logger.Warn("failed to load realm ASE config from db", "key", key, "error", err)
			}
			return nil
		}
	}

	// 3. Try Global config
	if !loaded {
		dbRow, err = dbQueries.GetASEConfigGlobalByName(ctx, dagName)
		if err == nil {
			loaded = true
		}
	}

	if err != nil {
		if logger != nil {
			logger.Warn("failed to load ASE config from db (including fallback)", "key", key, "error", err)
		}
		return nil
	}

	cfg, parseErr := parseDBRow(dbRow.DagConfig, dbRow.HyperParameters, dbRow.Prompts)
	if parseErr != nil {
		if logger != nil {
			logger.Error("failed to parse DB ASE config", "key", key, "error", parseErr)
		}
		return nil
	}

	// Update cache
	if configCache != nil {
		configCache.Add(key, cfg)
	}

	if onConfigLoaded != nil {
		onConfigLoaded(key, cfg)
	}

	return cfg
}

// InvalidateConfigCache removes the cached configuration for a specific tenant or realm,
// and publishes a message to Redis Pub/Sub to signal other instances to invalidate their cache.
func InvalidateConfigCache(tenantID, realmID, dagName string) {
	if dagName == "" {
		return
	}

	key := "tenant_" + tenantID + "_" + dagName
	if tenantID == "" {
		if realmID != "" {
			key = "realm_" + realmID + "_" + dagName
		} else {
			key = "global_" + dagName
		}
	}

	if configCache != nil {
		configCache.Remove(key)
		if strings.HasPrefix(key, "global_") {
			configCache.Purge()
		}
	}

	if redisClient != nil {
		redisClient.Publish(context.Background(), "ase:config_updates", key)
	}
}

// GetPrompt returns a prompt by key from the given configuration context.
func GetPrompt(tenantID, realmID, dagName, key string) string {
	cfg := GetConfig(tenantID, realmID, dagName)
	if cfg == nil || cfg.Prompts == nil {
		return ""
	}
	val := cfg.Prompts[key]
	switch v := val.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, part := range v {
			if s, ok := part.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

// listenForConfigUpdates subscribes to Redis Pub/Sub for hot-reloading configurations.
func listenForConfigUpdates() {
	pubsub := redisClient.Subscribe(context.Background(), "ase:config_updates")
	defer pubsub.Close()

	for msg := range pubsub.Channel() {
		if msg.Payload == "" {
			continue
		}

		key := msg.Payload

		// Invalidate cache
		if configCache != nil {
			configCache.Remove(key)
			if strings.HasPrefix(key, "global_") {
				configCache.Purge()
			}
		}

		// Eagerly reload
		tenantID := ""
		realmID := ""
		dagName := ""
		
		parts := strings.Split(key, "_")
		if strings.HasPrefix(key, "tenant_") && len(parts) >= 2 {
			tenantID = parts[1]
			if len(parts) > 2 {
				dagName = strings.Join(parts[2:], "_")
			}
		} else if strings.HasPrefix(key, "realm_") && len(parts) >= 2 {
			realmID = parts[1]
			if len(parts) > 2 {
				dagName = strings.Join(parts[2:], "_")
			}
		} else if strings.HasPrefix(key, "global_") && len(parts) >= 2 {
			dagName = strings.Join(parts[1:], "_")
		} else {
			dagName = key
		}

		if dagName != "" {
			GetConfig(tenantID, realmID, dagName)
		}
	}
}

// GetSystemVectorConfig returns the global vector memory tuning settings.
func GetSystemVectorConfig() VectorMemoryConfig {
	systemVectorConfigMu.RLock()
	if systemVectorConfig != nil {
		defer systemVectorConfigMu.RUnlock()
		return *systemVectorConfig
	}
	systemVectorConfigMu.RUnlock()

	ctx := context.Background()
	defaultCfg := VectorMemoryConfig{
		Enabled:                 true,
		EmbeddingProvider:       "openai",
		OpenAIEmbeddingModel:    "text-embedding-3-small",
		RetrievalTopK:           5,
		HydratorIntervalSeconds: 30,
		HydratorBatchSize:       50,
		HydratorMinConfidence:   0.98,
		ScaNNNumLeaves:          10,
	}

	if dbQueries == nil {
		return defaultCfg
	}

	dbRow, err := dbQueries.GetSystemVectorConfig(ctx)
	if err != nil {
		if logger != nil {
			logger.Warn("GetSystemVectorConfig failed, using defaults", "error", err)
		}
		return defaultCfg
	}

	cfg := VectorMemoryConfig{
		Enabled:                 true,
		EmbeddingProvider:       dbRow.EmbeddingProvider,
		OpenAIEmbeddingModel:    dbRow.EmbeddingModel,
		RetrievalTopK:           int(dbRow.RetrievalTopK),
		HydratorIntervalSeconds: int(dbRow.HydratorIntervalSeconds),
		HydratorBatchSize:       int(dbRow.HydratorBatchSize),
		HydratorMinConfidence:   dbRow.HydratorMinConfidence,
		ScaNNNumLeaves:          int(dbRow.ScannNumLeaves),
	}

	systemVectorConfigMu.Lock()
	systemVectorConfig = &cfg
	systemVectorConfigMu.Unlock()

	return cfg
}

// InvalidateSystemVectorConfigCache clears the global vector config cache.
func InvalidateSystemVectorConfigCache() {
	systemVectorConfigMu.Lock()
	systemVectorConfig = nil
	systemVectorConfigMu.Unlock()

	if redisClient != nil {
		redisClient.Publish(context.Background(), "ase:vector_config_updates", "invalidate")
	}
}

