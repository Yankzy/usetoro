package ase

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestLRUCaching(t *testing.T) {
	// Initialize with nil DB and Redis
	err := InitConfig(nil, nil, nil)
	assert.NoError(t, err)
	
	// Initially it should return nil because no DB
	cfg := GetConfig("test-tenant", "", "test-dag")
	assert.Nil(t, cfg)
	
	// Manually inject something into the cache
	customCfg := &ASEConfig{
		HyperParameters: HyperParameters{
			ConfidenceThreshold: 0.99,
		},
	}
	configCache.Add("tenant_test-tenant_test-dag", customCfg)
	
	// Now GetConfig should return from cache
	cachedCfg := GetConfig("test-tenant", "", "test-dag")
	assert.NotNil(t, cachedCfg)
	assert.Equal(t, float64(0.99), cachedCfg.HyperParameters.ConfidenceThreshold)
}

func TestConfigLoadedCallbackAssignment(t *testing.T) {
	callbackFired := false
	
	SetOnConfigLoaded(func(key string, cfg *ASEConfig) {
		callbackFired = true
	})

	assert.NotNil(t, onConfigLoaded)
	
	// Trigger it manually to verify it executes the closure
	onConfigLoaded("test", nil)
	assert.True(t, callbackFired)
}
