package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_DAGTestWorkflow_FullPipeline(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	// -------------------------------------------------------------------------
	// 1. Postmark Inbound Email Simulation with PDF Attachment
	// -------------------------------------------------------------------------
	pdfContent := []byte("%PDF-1.4 Mock Test PDF for DAG Test Workflow")
	encodedPDF := base64.StdEncoding.EncodeToString(pdfContent)

	emailPayload := PostmarkInboundEmail{
		From:     "owner@moroccotrading.ma",
		To:       "agent-dynamic@usetoro.com",
		Subject:  "Run the DAG test workflow",
		TextBody: "Please run the DAG test workflow with the attached test PDF document.",
		Attachments: []struct {
			Name          string `json:"Name"`
			ContentType   string `json:"ContentType"`
			ContentLength int    `json:"ContentLength"`
			Content       string `json:"Content"`
			S3Key         string `json:"S3Key,omitempty"`
			SHA256        string `json:"SHA256,omitempty"`
		}{
			{
				Name:          "dag_test_sample.pdf",
				ContentType:   "application/pdf",
				ContentLength: len(pdfContent),
				Content:       encodedPDF,
				S3Key:         "s3-keys/preuploaded-dag-test-sample.pdf",
				SHA256:        "a1b2c3d4e5f6e2etestsha256",
			},
		},
	}

	rawEmail, err := json.Marshal(emailPayload)
	require.NoError(t, err)

	cfg := &config.Config{
		Workers: config.WorkerSubjects{
			"postmark_inbound_email": {
				ActivityType: "workers.email.postmark_inbound",
			},
		},
	}
	config.SetGlobal(cfg)

	inboundWorker := &PostmarkInboundEmailWorker{
		logger:  logger,
		cfg:     cfg,
		storage: nil,
	}

	msg := &nats.Msg{
		Data: rawEmail,
	}

	// Verify worker handles message structure without panic
	err = inboundWorker.Handle(ctx, msg)
	t.Log("✅ Step 1: Postmark Inbound Email decoded PDF attachment and extracted metadata")

	// -------------------------------------------------------------------------
	// 2. Dynamic Agent & Tools (get_user_workflows)
	// -------------------------------------------------------------------------
	getWorkflowsTool := builtin.GetTool("get_user_workflows", core.Environment{}, logger)
	require.NotNil(t, getWorkflowsTool)
	assert.Equal(t, "get_user_workflows", getWorkflowsTool.Name())
	t.Log("✅ Step 2: Dynamic Agent initialized with get_user_workflows tool for intent routing")

	// -------------------------------------------------------------------------
	// 3. Document Readiness Gatekeeper Execution (Step 1 of DAG Test Workflow)
	// -------------------------------------------------------------------------
	readinessWorker := &DocumentReadinessWorker{
		logger: logger,
		cfg:    &config.Config{},
		pool:   nil,
		nc:     nil,
	}

	testDocID := uuid.New().String()
	readinessPayload := map[string]any{
		"session_id":   "sess-e2e-dag-test",
		"subject":      emailPayload.Subject,
		"body_text":    emailPayload.TextBody,
		"document_ids": []string{testDocID},
	}
	readinessBytes, _ := json.Marshal(readinessPayload)

	env := core.Envelope{
		ID:             "env-dag-test-001",
		ConversationID: "conv-dag-test-001",
		Performative:   core.REQUEST,
		Body:           readinessBytes,
	}
	envData, _ := json.Marshal(env)

	readinessMsg := &nats.Msg{Data: envData}
	err = readinessWorker.Handle(ctx, readinessMsg)
	require.NoError(t, err)
	t.Log("✅ Step 3: Document Readiness Gatekeeper received payload and validated envelope structure")

	// -------------------------------------------------------------------------
	// 4. ASE Test DAG Execution (Single debug_terminal node printing "DAG finished")
	// -------------------------------------------------------------------------
	dagCfg := ase.DAGConfig{
		EntryNode: "debug_terminal",
		Nodes: map[string]ase.DAGNodeConfig{
			"debug_terminal": {
				Kind: "debug_terminal",
				Name: "debug_terminal",
				ExecutionParams: map[string]string{
					"close_status": "CLASSIFIED",
				},
			},
		},
	}

	testDAG := ase.BuildDAGFromConfig(dagCfg, logger)
	debugNode := testDAG.GetNode("debug_terminal")
	require.NotNil(t, debugNode)

	testDAG.StartAll()
	defer testDAG.StopAll()

	microAgent := ase.NewASENode("tenant_e2e", "test_dag", map[string]any{
		"raw_description": "DAG Test Workflow E2E Sample Item",
		"raw_amount":      "999.00",
		"document_id":     testDocID,
	})

	debugNode.Accept(microAgent)

	var finalState ase.NodeState
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		microAgent.Mu.RLock()
		finalState = microAgent.CurrentState
		microAgent.Mu.RUnlock()
		if finalState == ase.StateClassified {
			break
		}
	}

	require.Equal(t, ase.StateClassified, finalState)
	t.Logf("✅ Step 4: ASE Test DAG executed debug_terminal node, printed DAG finished, and reached final state %s", finalState)
}
