package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Setup: ensure no env vars interfere
	os.Unsetenv("PORT")
	os.Unsetenv("AI_THRESHOLD")

	// Required fields
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	dummyKey := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	os.Setenv("ENCRYPTION_KEY", dummyKey)
	os.Setenv("NATS_URL", "nats://test:4222")

	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("ENCRYPTION_KEY")
		os.Unsetenv("NATS_URL")
	}()

	cfg, _, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "nats://test:4222", cfg.NATS.URL)
	assert.InDelta(t, 0.75, cfg.AIThreshold, 1e-9, "AIThreshold should default to 0.75")
	assert.Equal(t, 1, cfg.RuleEngine.TargetRank)
	assert.Equal(t, 1, cfg.RuleEngine.MinUsageCount)
}

func TestGlobalConfig(t *testing.T) {
	cfg1 := &Config{Port: "8081"}
	SetGlobal(cfg1)
	assert.Equal(t, "8081", GetGlobal().Port)

	cfg2 := &Config{Port: "8082"}
	SetGlobal(cfg2)
	assert.Equal(t, "8082", GetGlobal().Port)
}

func TestLoad_AIThresholdEnvOverride(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	dummyKey := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	os.Setenv("ENCRYPTION_KEY", dummyKey)
	os.Setenv("NATS_URL", "nats://test:4222")
	os.Setenv("AI_THRESHOLD", "0.85")

	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("ENCRYPTION_KEY")
		os.Unsetenv("NATS_URL")
		os.Unsetenv("AI_THRESHOLD")
	}()

	cfg, _, err := Load()
	require.NoError(t, err)
	assert.InDelta(t, 0.85, cfg.AIThreshold, 1e-9)
}

func TestLoad_EnvOverrides(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	dummyKey := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	os.Setenv("ENCRYPTION_KEY", dummyKey)

	os.Setenv("PORT", "9090")
	os.Setenv("APP_DB_MIN_CONNS", "20") // Testing prefix
	os.Setenv("DB_MAX_CONNS", "100")    // Testing legacy explicit bind
	os.Setenv("NATS_URL", "nats://overridden:4222")

	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("ENCRYPTION_KEY")
		os.Unsetenv("PORT")
		os.Unsetenv("APP_DB_MIN_CONNS")
		os.Unsetenv("DB_MAX_CONNS")
		os.Unsetenv("NATS_URL")
	}()

	cfg, _, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "9090", cfg.Port)
	assert.Equal(t, 20, cfg.DBMinConns)
	assert.Equal(t, 100, cfg.DBMaxConns)
	assert.Equal(t, "nats://overridden:4222", cfg.NATS.URL)
}

func TestLoad_Streams(t *testing.T) {
	// This test checks if defaults.yml is loaded
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	dummyKey := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	os.Setenv("ENCRYPTION_KEY", dummyKey)
	os.Setenv("NATS_URL", "nats://localhost:4222")

	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("ENCRYPTION_KEY")
		os.Unsetenv("NATS_URL")
	}()

	cfg, _, err := Load()
	require.NoError(t, err)

	// Check if services are loaded from defaults.yml
	if len(cfg.NATS.Services) > 0 {
		sync, ok := cfg.NATS.Services["sync"]
		assert.True(t, ok, "Expected 'sync' service to be present")
		if ok {
			assert.Equal(t, "SYNC", sync.StreamName)
			assert.Contains(t, sync.JetStream.Subjects, "cmd.sync.>", "Expected 'cmd.sync.>' subject")

			// Check component
			qbo, ok := sync.Components["qbo"]
			assert.True(t, ok, "Expected 'qbo' component inside sync")
			if ok {
				assert.Equal(t, "QBO_EVENTS", qbo.StreamName, "Expected QBO to have its own stream name")
				assert.Contains(t, qbo.JetStream.Subjects, "qbo.>", "Expected 'qbo.>' subject in component")
			}
		}
	} else {
		// Failing the test if defaults.yml isn't loaded
		t.Fatal("defaults.yml was not loaded or contained no services")
	}
}
