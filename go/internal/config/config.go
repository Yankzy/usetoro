package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration variables from the environment.
// GO CONCEPT: Centralized Config
// Instead of calling os.Getenv() all over the code, we load it once into a struct.
// Benefit: We can validation all inputs at startup (Fail Fast) and pass 'cfg' around cleanly.
type Config struct {
	Port        string
	DatabaseURL string
	NatsURL     string

	// Infrastructure Knobs
	DBMinConns int
	DBMaxConns int

	// Timeouts
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	NATSPublishTimeout time.Duration

	// Limits
	MaxWebhookBodySize int64

	// Security
	EncryptionKey []byte
}

// Load returns the application configuration sourced from environment variables.
// It returns an error if critical environment variables are missing.
func Load() (Config, error) {
	cfg := Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		NatsURL:     os.Getenv("NATS_URL"),

		// Defaults suitable for a small production pod, but configurable.
		DBMinConns: getEnvInt("DB_MIN_CONNS", 10),
		DBMaxConns: getEnvInt("DB_MAX_CONNS", 50),

		// Timeouts
		ReadTimeout:        getEnvDuration("SERVER_READ_TIMEOUT", 5*time.Second),
		WriteTimeout:       getEnvDuration("SERVER_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:        getEnvDuration("SERVER_IDLE_TIMEOUT", 120*time.Second),
		NATSPublishTimeout: getEnvDuration("NATS_PUBLISH_TIMEOUT", 5*time.Second),

		// Limits
		MaxWebhookBodySize: getEnvInt64("MAX_WEBHOOK_BODY_SIZE", 1<<20), // 1 MiB default

		// Security - EncryptionKey will be loaded and validated below
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	if cfg.NatsURL == "" {
		// We explicitly do NOT default to localhost for NATS in production-ready code.
		// It must be provided.
		return Config{}, fmt.Errorf("NATS_URL is required")
	}

	// Load encryption key
	encryptionKeyStr := os.Getenv("ENCRYPTION_KEY")
	if encryptionKeyStr == "" {
		return Config{}, fmt.Errorf("ENCRYPTION_KEY is required")
	}

	// Decode base64 encryption key
	encryptionKey, err := base64.StdEncoding.DecodeString(encryptionKeyStr)
	if err != nil {
		return Config{}, fmt.Errorf("ENCRYPTION_KEY must be base64-encoded: %w", err)
	}

	if len(encryptionKey) != 32 {
		return Config{}, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes when decoded (got %d bytes)", len(encryptionKey))
	}

	cfg.EncryptionKey = encryptionKey

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
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

func getEnv(key, fallback string) string {
	if v, exists := os.LookupEnv(key); exists {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, exists := os.LookupEnv(key); exists {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v, exists := os.LookupEnv(key); exists {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v, exists := os.LookupEnv(key); exists {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}
