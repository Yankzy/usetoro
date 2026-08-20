package builtin_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTriggerBus struct {
	mu               sync.Mutex
	publishedSubject string
	publishedData    []byte
}

func (m *mockTriggerBus) Publish(subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedSubject = subject
	m.publishedData = data
	return nil
}

func (m *mockTriggerBus) PublishCore(subject string, data []byte) error { return nil }
func (m *mockTriggerBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return nil, nil
}
func (m *mockTriggerBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func TestTriggerWorkflowTool_Metadata(t *testing.T) {
	tool := builtin.NewTriggerWorkflowTool(nil, nil)
	require.NotNil(t, tool)

	assert.Equal(t, "trigger_workflow", tool.Name())
	assert.NotEmpty(t, tool.Description())

	schema := tool.InputSchema()
	require.NotEmpty(t, schema)

	var schemaMap map[string]any
	err := json.Unmarshal(schema, &schemaMap)
	require.NoError(t, err)
	assert.Equal(t, "object", schemaMap["type"])
}

func TestTriggerWorkflowTool_Call(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	bus := &mockTriggerBus{}
	tool := builtin.NewTriggerWorkflowTool(bus, logger)

	input := map[string]any{
		"trigger_topic": "events.accounting.1.test_dag",
		"workflow_name": "DAG Test Workflow",
		"document_ids":  []any{"doc-123", "doc-456"},
		"subject":       "Run the DAG test workflow",
		"body_text":     "Please run the test workflow",
	}

	result, err := tool.Call(context.Background(), input)
	require.NoError(t, err)
	assert.Contains(t, result, "Workflow triggered successfully on topic 'events.accounting.1.test_dag'")

	assert.Equal(t, "events.accounting.1.test_dag", bus.publishedSubject)
	require.NotEmpty(t, bus.publishedData)

	var publishedMap map[string]any
	err = json.Unmarshal(bus.publishedData, &publishedMap)
	require.NoError(t, err)
	assert.Equal(t, "Run the DAG test workflow", publishedMap["subject"])
	assert.Equal(t, []any{"doc-123", "doc-456"}, publishedMap["document_ids"])
}
