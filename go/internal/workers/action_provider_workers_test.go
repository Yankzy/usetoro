package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestActionProviderWorkers_Handle(t *testing.T) {
	// Connect to the local NATS server
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("Local NATS server is not running on defaults. skipping NATS-reliant worker tests.")
	}
	defer nc.Close()

	ctx := context.Background()
	logger := slog.Default()

	// 1. Test ReclassifyToDeMinimisExpenseAccountWorker
	w1 := &ReclassifyToDeMinimisExpenseAccountWorker{logger: logger}
	sub1List := w1.Subscriptions()
	assert.Len(t, sub1List, 1)
	assert.Equal(t, "worker.inbox.action.reclassify_to_de_minimis_expense_account", sub1List[0].Subject)

	// Subscribe to the action subject to receive the request
	actionSub, err := nc.SubscribeSync("worker.inbox.action.reclassify_to_de_minimis_expense_account")
	assert.NoError(t, err)
	defer actionSub.Unsubscribe()

	// Subscribe to the reply subject to verify response
	replyInbox := nats.NewInbox()
	replySub, err := nc.SubscribeSync(replyInbox)
	assert.NoError(t, err)
	defer replySub.Unsubscribe()

	req := ActionRequest{
		NodeID:   "test-node-1",
		RealmID:  "realm-1",
		Payload:  map[string]interface{}{"raw_amount": "100.00"},
		DagName:  "test-dag",
		TenantID: "tenant-1",
	}
	reqBytes, _ := json.Marshal(req)

	// Publish the request from our client
	err = nc.PublishRequest("worker.inbox.action.reclassify_to_de_minimis_expense_account", replyInbox, reqBytes)
	assert.NoError(t, err)

	// Receive it as the worker would
	msg, err := actionSub.NextMsg(2 * time.Second)
	assert.NoError(t, err)

	// Handle the message via the worker
	err = w1.Handle(ctx, msg)
	assert.NoError(t, err)

	// Fetch the reply and verify
	replyMsg, err := replySub.NextMsg(2 * time.Second)
	assert.NoError(t, err)

	var resp1 ActionResponse
	err = json.Unmarshal(replyMsg.Data, &resp1)
	assert.NoError(t, err)
	assert.Equal(t, "forced_expense_reclassification", resp1.Property)
	assert.Len(t, resp1.Candidates, 1)
	assert.Equal(t, "DE_MINIMIS_EXPENSE_TRIGGER", resp1.Candidates[0].Value)
	assert.Equal(t, "De Minimis Tools & Equipment", resp1.PayloadUpdates["category"])

	// 2. Test InterBankTransferCollapseWorker
	w2 := &InterBankTransferCollapseWorker{logger: logger}
	actionSub2, err := nc.SubscribeSync("worker.inbox.action.inter_bank_transfer_collapse")
	assert.NoError(t, err)
	defer actionSub2.Unsubscribe()

	err = nc.PublishRequest("worker.inbox.action.inter_bank_transfer_collapse", replyInbox, reqBytes)
	assert.NoError(t, err)

	msg2, err := actionSub2.NextMsg(2 * time.Second)
	assert.NoError(t, err)

	err = w2.Handle(ctx, msg2)
	assert.NoError(t, err)

	replyMsg2, err := replySub.NextMsg(2 * time.Second)
	assert.NoError(t, err)
	var resp2 ActionResponse
	err = json.Unmarshal(replyMsg2.Data, &resp2)
	assert.NoError(t, err)
	assert.Equal(t, "close_status", resp2.Property)
	assert.Equal(t, "CLASSIFIED", resp2.Candidates[0].Value)
}

func TestActionProviderWorkers_Subscriptions(t *testing.T) {
	w := &DbReceiptLookupWorker{cfg: &config.Config{}, logger: slog.Default()}
	subs := w.Subscriptions()
	assert.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.action.db_receipt_lookup", subs[0].Subject)
	assert.Equal(t, "action_provider_db_receipt_lookup", subs[0].Group)
}

func TestNatsRecoveryExecutor_ExecuteAction(t *testing.T) {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("Local NATS server is not running on defaults. skipping NATS-reliant recovery executor tests.")
	}
	defer nc.Close()

	ctx := context.Background()
	logger := slog.Default()

	executor := &NatsRecoveryExecutor{
		nc:     nc,
		logger: logger,
	}

	node := ase.NewASENode("tenant-1", "realm-1", "dag-test", map[string]interface{}{"raw_amount": "50.00"})
	node.NodeID = "node-test-recovery"
	node.SetLogger(logger)

	// Set up a mock action worker subscribing to NATS
	actionSub, err := nc.SubscribeSync("worker.inbox.action.custom_recovery_action")
	assert.NoError(t, err)
	defer actionSub.Unsubscribe()

	// Run recovery execution in background
	errChan := make(chan error, 1)
	resChan := make(chan bool, 1)
	go func() {
		ok, err := executor.ExecuteAction(ctx, "custom_recovery_action", node)
		errChan <- err
		resChan <- ok
	}()

	// Mock the action provider responding over NATS
	msg, err := actionSub.NextMsg(2 * time.Second)
	assert.NoError(t, err)

	resp := ActionResponse{
		Property: "compliance_decision",
		Candidates: []ase.ProbabilityCandidate{
			{Value: "COMPLIANT_OUTFLOW", Confidence: 1.0, Reasoning: "Found compliant documentation."},
		},
		PayloadUpdates: map[string]interface{}{
			"category": "Office Supplies",
		},
		ContextUpdates: []string{"Document matched successfully."},
	}
	respBytes, _ := json.Marshal(resp)
	err = msg.Respond(respBytes)
	assert.NoError(t, err)

	// Await the executor response
	ok := <-resChan
	err = <-errChan
	assert.NoError(t, err)
	assert.True(t, ok)

	// Verify updates applied to node
	node.Mu.RLock()
	assert.Equal(t, "Office Supplies", node.Payload["category"])
	assert.Contains(t, node.ContextUpdates, "Document matched successfully.")
	node.Mu.RUnlock()
}
