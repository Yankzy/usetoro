package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/spf13/viper"
)

// Config mirrors your defaults.yaml structure and environment variables.
type Config struct {
	Port        string `mapstructure:"port"`
	DatabaseURL string `mapstructure:"database_url"`

	// Infrastructure Knobs
	DBMinConns int `mapstructure:"db_min_conns"`
	DBMaxConns int `mapstructure:"db_max_conns"`

	// Timeouts
	ReadTimeout        time.Duration `mapstructure:"server_read_timeout"`
	WriteTimeout       time.Duration `mapstructure:"server_write_timeout"`
	IdleTimeout        time.Duration `mapstructure:"server_idle_timeout"`
	NATSPublishTimeout time.Duration `mapstructure:"nats_publish_timeout"`

	// Limits
	MaxWebhookBodySize int64 `mapstructure:"max_webhook_body_size"`

	// QBO Config
	QBOClientID     string `mapstructure:"qbo_client_id"`
	QBOClientSecret string `mapstructure:"qbo_client_secret"`
	QBOIsProduction bool   `mapstructure:"qbo_is_production"`
	QBOMinorVersion string `mapstructure:"qbo_minor_version"`

	// CDC (Change Data Capture) Config
	CDCEnabled      bool          `mapstructure:"cdc_enabled"`
	CDCSyncInterval time.Duration `mapstructure:"cdc_sync_interval"`

	// AI/Vector Config
	PineconeIndex       string  `mapstructure:"pinecone_index"`
	EmbeddingModel      string  `mapstructure:"embedding_model"`
	EmbeddingDimensions int     `mapstructure:"embedding_dimensions"`
	AIThreshold         float64 `mapstructure:"ai_threshold"`

	// Security
	// We read this as a string first (base64) then decode it
	EncryptionKeyString string `mapstructure:"encryption_key"`
	EncryptionKey       []byte `mapstructure:"-"`

	// NATS Config
	NATS NATSConfig `mapstructure:"nats"`

	// Agents Config
	Agents []agent.AgentConfig `mapstructure:"agents"`
}

type NATSConfig struct {
	URL      string                   `mapstructure:"url"`
	Services map[string]ServiceConfig `mapstructure:"streams"` // Mapping "streams" key to Services map
}

type ServiceConfig struct {
	StreamName      string                     `mapstructure:"stream_name"`
	SubjectTemplate string                     `mapstructure:"subject_template"`
	JetStream       JetStreamConfig            `mapstructure:"jetstream"`
	Components      map[string]ComponentConfig `mapstructure:"components"`
}

type ComponentConfig struct {
	StreamName string          `mapstructure:"stream_name"`
	JetStream  JetStreamConfig `mapstructure:"jetstream"`
}

type JetStreamConfig struct {
	Replicas    int           `mapstructure:"replicas"`
	MaxAge      time.Duration `mapstructure:"max_age"`
	Subjects    []string      `mapstructure:"subjects"`
	DenyDelete  bool          `mapstructure:"deny_delete"`
	DenyPurge   bool          `mapstructure:"deny_purge"`
	AllowRollup bool          `mapstructure:"allow_rollup"`
	AllowDirect bool          `mapstructure:"allow_direct"`
}

// Load reads defaults.yaml and overrides with ENV variables
func Load() (*Config, error) {
	v := viper.New()

	// 1. Tell Viper where to look
	v.SetConfigName("defaults")
	v.SetConfigType("yaml")
	v.AddConfigPath("./internal/config")
	v.AddConfigPath("./go/internal/config")
	v.AddConfigPath(".")

	// 2. Setup Environment Variable Overrides
	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind legacy environment variables explicitly
	_ = v.BindEnv("port", "PORT")
	_ = v.BindEnv("database_url", "DATABASE_URL")
	_ = v.BindEnv("nats.url", "NATS_URL")
	_ = v.BindEnv("db_min_conns", "DB_MIN_CONNS")
	_ = v.BindEnv("db_max_conns", "DB_MAX_CONNS")
	_ = v.BindEnv("server_read_timeout", "SERVER_READ_TIMEOUT")
	_ = v.BindEnv("server_write_timeout", "SERVER_WRITE_TIMEOUT")
	_ = v.BindEnv("server_idle_timeout", "SERVER_IDLE_TIMEOUT")
	_ = v.BindEnv("nats_publish_timeout", "NATS_PUBLISH_TIMEOUT")
	_ = v.BindEnv("max_webhook_body_size", "MAX_WEBHOOK_BODY_SIZE")
	_ = v.BindEnv("qbo_client_id", "QBO_CLIENT_ID")
	_ = v.BindEnv("qbo_client_secret", "QBO_CLIENT_SECRET")
	_ = v.BindEnv("qbo_is_production", "QBO_IS_PRODUCTION")
	_ = v.BindEnv("qbo_minor_version", "QBO_MINOR_VERSION")
	_ = v.BindEnv("cdc_enabled", "CDC_ENABLED")
	_ = v.BindEnv("cdc_sync_interval", "CDC_SYNC_INTERVAL")
	_ = v.BindEnv("pinecone_index", "PINECONE_INDEX")
	_ = v.BindEnv("embedding_model", "EMBEDDING_MODEL")
	_ = v.BindEnv("embedding_dimensions", "EMBEDDING_DIMENSIONS")
	_ = v.BindEnv("ai_threshold", "AI_THRESHOLD")
	_ = v.BindEnv("encryption_key", "ENCRYPTION_KEY")

	// Set defaults corresponding to the old getEnv fallbacks
	v.SetDefault("port", "8080")
	v.SetDefault("db_min_conns", 10)
	v.SetDefault("db_max_conns", 50)
	v.SetDefault("server_read_timeout", 5*time.Second)
	v.SetDefault("server_write_timeout", 10*time.Second)
	v.SetDefault("server_idle_timeout", 120*time.Second)
	v.SetDefault("nats_publish_timeout", 5*time.Second)
	v.SetDefault("max_webhook_body_size", 1<<20)
	v.SetDefault("qbo_is_production", false)
	v.SetDefault("cdc_enabled", true)
	v.SetDefault("cdc_sync_interval", 1*time.Hour)
	v.SetDefault("pinecone_index", "toro-ai")
	v.SetDefault("embedding_model", "text-embedding-3-small")
	v.SetDefault("embedding_dimensions", 1536)
	v.SetDefault("ai_threshold", 0.75)

	// 3. Actually read the file from disk
	if err := v.ReadInConfig(); err != nil {
		// It's okay if config file is missing IF we have all needed envs,
		// but for NATS streams we likely need the file.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("found config file but failed to read: %w", err)
		}
		// Log or proceed? We proceed and rely on valid env vars / defaults.
	}

	// 4. Unmarshal into our strict Go struct
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config into struct: %w", err)
	}

	// Validation and post-processing

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	if c.NATS.URL == "" {
		// Try to fallback to legacy NatsURL field if we were to support it,
		// but we mapped NATS_URL to nats.url so it should be there.
		return nil, fmt.Errorf("NATS_URL is required")
	}

	// Load encryption key
	if c.EncryptionKeyString == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required")
	}

	// Decode base64 encryption key
	encryptionKey, err := base64.StdEncoding.DecodeString(c.EncryptionKeyString)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be base64-encoded: %w", err)
	}

	if len(encryptionKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes when decoded (got %d bytes)", len(encryptionKey))
	}

	c.EncryptionKey = encryptionKey

	// Validate configuration
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &c, nil
}

// Validate checks configuration for correctness
func (c Config) Validate() error {
	// Connection pool validation
	if c.DBMinConns < 1 || c.DBMinConns > 100 {
		return fmt.Errorf("DB_MIN_CONNS must be between 1 and 100, got %d", c.DBMinConns)
	}
	if c.DBMaxConns < c.DBMinConns {
		return fmt.Errorf("DB_MAX_CONNS (%d) must be >= DB_MIN_CONNS (%d)", c.DBMaxConns, c.DBMinConns)
	}
	if c.DBMaxConns > 500 {
		return fmt.Errorf("DB_MAX_CONNS too large (%d), max 500", c.DBMaxConns)
	}

	// Timeout validation
	if c.ReadTimeout < time.Second || c.ReadTimeout > 60*time.Second {
		return fmt.Errorf("READ_TIMEOUT must be between 1s and 60s, got %v", c.ReadTimeout)
	}
	if c.WriteTimeout < time.Second || c.WriteTimeout > 60*time.Second {
		return fmt.Errorf("WRITE_TIMEOUT must be between 1s and 60s, got %v", c.WriteTimeout)
	}

	// Body size validation
	if c.MaxWebhookBodySize < 1024 || c.MaxWebhookBodySize > 10<<20 {
		return fmt.Errorf("MAX_WEBHOOK_BODY_SIZE must be between 1KB and 10MB, got %d", c.MaxWebhookBodySize)
	}

	return nil
}
