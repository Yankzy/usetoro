package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestAseBridgeWorker_DispatchToGeneralAgent(t *testing.T) {
	natsURLs := []string{
		"nats://localhost:4222",
		"nats://container-nats-1-1:4222",
		"nats://nats-1:4222",
	}
	var nc *nats.Conn
	var err error
	for _, url := range natsURLs {
		nc, err = nats.Connect(url, nats.Timeout(500*time.Millisecond))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Skip("NATS server not reachable, skipping dispatch test")
	}
	defer nc.Close()

	w := &AseBridgeWorker{
		logger: slog.Default(),
		nc:     nc,
	}

	ingressSubject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	assert.NoError(t, err)

	// Subscribe to the ingress subject to intercept the dispatch message
	msgChan := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe(ingressSubject, func(msg *nats.Msg) {
		msgChan <- msg
	})
	assert.NoError(t, err)
	defer sub.Unsubscribe()

	// Construct a dummy ASENode
	node := ase.NewASENode("tenant-123", "realm-456", "default-dag", "Test transaction description", "INFLOW", "100.00")
	node.NodeID = "node-abc"
	node.HoldReason = "ambiguous counterparty"
	node.CurrentState = ase.StateHoldAmbiguous

	w.dispatchToGeneralAgent(context.Background(), node, "session-xyz")

	select {
	case msg := <-msgChan:
		var payload map[string]interface{}
		err := json.Unmarshal(msg.Data, &payload)
		assert.NoError(t, err)
		assert.Equal(t, "tenant-123", payload["entity_id"])
		assert.Equal(t, "session-xyz", payload["session_id"])
		assert.Equal(t, "system", payload["source"])
		assert.Equal(t, "ase-engine:node-abc", payload["from_handle"])
		assert.Equal(t, "general-agent", payload["to_handle"])
		assert.Contains(t, payload["prompt"], "SYSTEM ALERT: A transaction (ID: node-abc)")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatched alert message")
	}
}
