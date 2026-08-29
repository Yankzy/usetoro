package ase

import (
	"encoding/json"
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestLRUCaching(t *testing.T) {
	// Initialize with nil DB and Redis
	err := InitConfig(nil, nil, nil)
	assert.NoError(t, err)
	
	// Initially it should return nil because no DB
	cfg := GetConfig("test-user", "test-dag")
	assert.Nil(t, cfg)
	
	// Manually inject something into the cache
	customCfg := &ASEConfig{
		HyperParameters: HyperParameters{
			ConfidenceThreshold: 0.99,
		},
	}
	configCache.Add("test-dag_test-user", customCfg)
	
	// Now GetConfig should return from cache
	cachedCfg := GetConfig("test-user", "test-dag")
	assert.NotNil(t, cachedCfg)
	assert.Equal(t, float64(0.99), cachedCfg.HyperParameters.ConfidenceThreshold)
}

func TestConfigLoadedCallbackAssignment(t *testing.T) {
	callbackFired := false
	
	RegisterOnConfigLoaded(func(key string, cfg *ASEConfig) {
		callbackFired = true
	})

	assert.NotNil(t, onConfigLoaded)
	
	// Trigger it manually to verify it executes the closure
	onConfigLoaded[len(onConfigLoaded)-1]("test", nil)
	assert.True(t, callbackFired)
}

func TestDAGNodeConfig_UnmarshalJSON_HeterogeneousExecutionParameters(t *testing.T) {
	rawJSON := `{
		"kind": "action",
		"name": "journal_builder",
		"execution_parameters": {
			"action_provider": "pcge_journal_builder",
			"amount_scale": 2,
			"require_exact_bank_line_amount": true,
			"idempotency_key_fields": [
				"entity_id",
				"bank_account_id",
				"transaction_id"
			]
		}
	}`

	var node DAGNodeConfig
	err := json.Unmarshal([]byte(rawJSON), &node)
	assert.NoError(t, err)
	assert.Equal(t, "action", node.Kind)
	assert.Equal(t, "journal_builder", node.Name)
	assert.Equal(t, "pcge_journal_builder", node.ExecutionParams["action_provider"])
	assert.Equal(t, "2", node.ExecutionParams["amount_scale"])
	assert.Equal(t, "true", node.ExecutionParams["require_exact_bank_line_amount"])
	assert.Contains(t, node.ExecutionParams["idempotency_key_fields"], "entity_id")
}

