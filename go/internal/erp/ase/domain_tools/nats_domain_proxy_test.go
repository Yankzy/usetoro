package domain_tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainTools_RegistryFallback(t *testing.T) {
	// Built-in bookkeeping tool
	tool := Get("bookkeeping")
	require.NotNil(t, tool)
	_, isNatsProxy := tool.(*NatsDomainProxy)
	assert.False(t, isNatsProxy)

	// External domain tool fallback to NatsDomainProxy
	extTool := Get("insurance")
	require.NotNil(t, extTool)
	proxy, isNatsProxy := extTool.(*NatsDomainProxy)
	assert.True(t, isNatsProxy)
	assert.Equal(t, "insurance", proxy.DomainName)
}

func TestNatsDomainProxy_Integration(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("Skipping NATS integration test because local NATS server is not running")
	}
	defer nc.Close()

	domainName := "test_insurance"
	proxy := NewNatsDomainProxy(domainName)
	deps := ToolDependencies{
		NC: nc,
	}

	// 1. Mock Handler for domain.test_insurance.agents.build
	buildSub, err := nc.Subscribe( "domain.test_insurance.agents.build", func(msg *nats.Msg) {
		resp := map[string]any{
			"agents": []map[string]any{
				{
					"node_id":   "agent-1",
					"tenant_id": "tenant-1",
					"dag_name":  "test_dag",
				},
			},
		}
		respBytes, _ := json.Marshal(resp)
		_ = msg.Respond(respBytes)
	})
	require.NoError(t, err)
	defer buildSub.Unsubscribe()

	// 2. Test BuildAgents
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := core.Envelope{ID: "env-1"}
	agents, err := proxy.BuildAgents(ctx, env, "test_dag", deps)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "agent-1", agents[0].NodeID)

	// 3. Mock Handler for domain.test_insurance.alert.generate
	alertSub, err := nc.Subscribe("domain.test_insurance.alert.generate", func(msg *nats.Msg) {
		resp := map[string]any{
			"payload": map[string]any{
				"prompt": "Test alert prompt",
			},
		}
		respBytes, _ := json.Marshal(resp)
		_ = msg.Respond(respBytes)
	})
	require.NoError(t, err)
	defer alertSub.Unsubscribe()

	testNode := &ase.AutonomousSemanticEngineNode{NodeID: "agent-1"}
	alertPayload, err := proxy.GenerateAlertPayload(ctx, testNode, deps)
	require.NoError(t, err)
	assert.Equal(t, "Test alert prompt", alertPayload["prompt"])
}

func TestNatsDomainClassifier_Integration(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("Skipping NATS integration test because local NATS server is not running")
	}
	defer nc.Close()

	domainName := "test_insurance"
	classifier := NewNatsDomainClassifier(domainName, nc, 5*time.Second)

	// Mock Handler for domain.test_insurance.classify.generic
	classifySub, err := nc.Subscribe("domain.test_insurance.classify.generic", func(msg *nats.Msg) {
		resp := map[string]any{
			"results": map[string]any{
				"agent-1": map[string]any{
					"Property": "claim_decision",
					"Candidates": []map[string]any{
						{"value": "APPROVED", "probability": 0.99},
					},
				},
			},
		}
		respBytes, _ := json.Marshal(resp)
		_ = msg.Respond(respBytes)
	})
	require.NoError(t, err)
	defer classifySub.Unsubscribe()

	thinkFn := classifier.BuildGenericThinkFunc("test/prompt_key")
	batch := []*ase.AutonomousSemanticEngineNode{
		{NodeID: "agent-1"},
	}

	results, err := thinkFn(context.Background(), batch)
	require.NoError(t, err)
	require.Contains(t, results, "agent-1")
	assert.Equal(t, "claim_decision", results["agent-1"].Property)
	require.Len(t, results["agent-1"].Candidates, 1)
	assert.Equal(t, "APPROVED", results["agent-1"].Candidates[0].Value)
}

func TestNatsDomainStatePersister_Integration(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("Skipping NATS integration test because local NATS server is not running")
	}
	defer nc.Close()

	domainName := "test_insurance"
	persister := NewNatsDomainStatePersister(domainName, nc, nil, 5*time.Second)

	// Mock Handler for domain.test_insurance.state.persist_node
	persistSub, err := nc.Subscribe("domain.test_insurance.state.persist_node", func(msg *nats.Msg) {
		resp := map[string]any{"success": true}
		respBytes, _ := json.Marshal(resp)
		_ = msg.Respond(respBytes)
	})
	require.NoError(t, err)
	defer persistSub.Unsubscribe()

	testNode := &ase.AutonomousSemanticEngineNode{NodeID: "agent-1"}
	err = persister.PersistNode(context.Background(), testNode)
	assert.NoError(t, err)
}
