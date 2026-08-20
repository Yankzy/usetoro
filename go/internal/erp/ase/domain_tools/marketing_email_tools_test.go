package domain_tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/stretchr/testify/require"
)

func TestEmailMarketingTool_BuildAgents(t *testing.T) {
	tool := &EmailMarketingTool{}
	
	payload := map[string]interface{}{
		"entity_id": "test-entity-123",
		"email":     "test@example.com",
		"name":      "Test User",
	}
	payloadBytes, _ := json.Marshal(payload)
	
	task := core.TaskDefinition{
		Payload: payloadBytes,
	}
	taskBytes, _ := json.Marshal(task)
	
	env := core.Envelope{
		Body: taskBytes,
	}
	
	deps := ToolDependencies{}
	
	nodes, err := tool.BuildAgents(context.Background(), env, "marketing_email", deps)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	
	node := nodes[0]
	require.Equal(t, "test-entity-123", node.TenantID)
	require.Equal(t, "marketing_email", node.DagName)
	require.Equal(t, "test@example.com", node.Payload["email"])
}

func TestMarketingStore_PersistReadyForSync(t *testing.T) {
	// Set up a test server to mock the Mailpool API
	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		
		err := json.NewDecoder(r.Body).Decode(&receivedPayload)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		MailpoolEndpoint: server.URL,
		MailpoolAPIKey:   "test-api-key",
	}

	store := NewMarketingStore(nil, cfg)

	// Create a node with COLD_OUTREACH intent
	node := ase.NewASENode("tenant-123", "marketing_email", map[string]interface{}{
		"email": "lead@company.com",
	})
	node.Candidates = map[string][]ase.ProbabilityCandidate{
		"macro_intent": {
			{Value: "COLD_OUTREACH", Confidence: 0.99, Reasoning: "Test reasoning"},
		},
	}

	err := store.PersistReadyForSync(context.Background(), node)
	require.NoError(t, err)

	require.NotNil(t, receivedPayload)
	toHeader := receivedPayload["to"].([]interface{})
	require.Len(t, toHeader, 1)
	toObj := toHeader[0].(map[string]interface{})
	require.Equal(t, "lead@company.com", toObj["email"])
}

func TestEmailMarketingTool_Registration(t *testing.T) {
	tool1 := Get("email_marketing")
	require.NotNil(t, tool1)
	require.IsType(t, &EmailMarketingTool{}, tool1)
}
