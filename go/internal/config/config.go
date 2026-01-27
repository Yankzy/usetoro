package config

import (
	"os"

	"github.com/nats-io/nats.go"
)

// Config holds all configuration variables from the environment.
// GO CONCEPT: Centralized Config
// Instead of calling os.Getenv() all over the code, we load it once into a struct.
// Benefit: We can validation all inputs at startup (Fail Fast) and pass 'cfg' around cleanly.
type Config struct {
	Port        string
	DatabaseURL string
	NatsURL     string
}

// Load returns the application configuration sourced from environment variables.
func Load() Config {
	return Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		NatsURL:     getEnv("NATS_URL", nats.DefaultURL),
	}
}

func getEnv(key, fallback string) string {
	if v, exists := os.LookupEnv(key); exists {
		return v
	}
	return fallback
}
