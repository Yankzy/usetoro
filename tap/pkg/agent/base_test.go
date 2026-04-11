package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

// --- Mock DBTX that returns "not found" for any query ---

type mockDBTX struct{}

func (m *mockDBTX) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), errors.New("mock: no db")
}
func (m *mockDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("mock: no db")
}
func (m *mockDBTX) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	return &mockRow{}
}

type mockRow struct{}

func (r *mockRow) Scan(_ ...interface{}) error {
	return errors.New("mock: workflow not found")
}

// --- Mock EventBus ---

type mockEventBus struct {
	mu                sync.Mutex
	messages          map[string][][]byte
	subscribedSubjects []string
	publishErr        error
	subscribeErr      error
}

func newMockBus() *mockEventBus {
	return &mockEventBus{messages: make(map[string][][]byte)}
}

func (b *mockEventBus) Publish(subject string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.publishErr != nil {
		return b.publishErr
	}
	b.messages[subject] = append(b.messages[subject], data)
	return nil
}

func (b *mockEventBus) RequestWithContext(_ context.Context, _ string, _ []byte) (*nats.Msg, error) {
	return nil, errors.New("mock: not implemented")
}

func (b *mockEventBus) QueueSubscribe(subj string, _ string, _ nats.MsgHandler, _ ...nats.SubOpt) (*nats.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribeErr != nil {
		return nil, b.subscribeErr
	}
	b.subscribedSubjects = append(b.subscribedSubjects, subj)
	return &nats.Subscription{}, nil
}

func (b *mockEventBus) published(subject string) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.messages[subject]
}

func (b *mockEventBus) subscriptions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.subscribedSubjects...)
}

// --- helpers ---

func newTestAgent(bus core.EventBus) *BaseAgent {
	return &BaseAgent{
		Logger: slog.Default(),
		Bus:    bus,
		Cfg: core.AgentConfig{
			DID:          "did:toro:test-agent",
			ActivityType: "agents.test.activity",
			// QueueGroup and DurableName are derived from DID by NewBaseAgent().
			// In tests we set them manually so we don't need a real KeyPair.
			QueueGroup:  "did-toro-test-agent-group",
			DurableName: "did-toro-test-agent-durable",
		},
	}
}

func newTestDB() *database.Queries {
	return database.New(&mockDBTX{})
}

// --- Tests ---

func TestExecuteGlobalWorkflow_HappyPath(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/status", "value": "DONE"}`),
		}, nil
	}

	onCompleteCalled := false
	onComplete := func(nextState []byte) error {
		onCompleteCalled = true
		if !strings.Contains(string(nextState), `"status":"DONE"`) {
			t.Errorf("onComplete received unexpected state: %s", string(nextState))
		}
		return nil
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		WorkflowConfig{},
		llmCallback,
		onComplete,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !onCompleteCalled {
		t.Fatal("onComplete was never called")
	}
}

func TestExecuteGlobalWorkflow_RBACRejection(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	wfCfg := WorkflowConfig{
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				"did:toro:test-agent": {"/status"},
			},
		},
	}

	// LLM tries to write to /admin_notes which RBAC denies
	attempt := 0
	llmCallback := func(faults []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		attempt++
		if attempt > 1 && len(faults) > 0 {
			// On retry, still produce the same bad patch to exhaust retries
			return []json.RawMessage{
				[]byte(`{"op": "add", "path": "/admin_notes", "value": "hacked"}`),
			}, nil
		}
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/admin_notes", "value": "hacked"}`),
		}, nil
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		wfCfg,
		llmCallback,
		nil,
	)

	if err == nil {
		t.Fatal("expected circuit breaker error, got nil")
	}
	if !strings.Contains(err.Error(), "circuit breaker tripped") {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt != 3 {
		t.Fatalf("expected 3 LLM attempts, got %d", attempt)
	}
}

func TestExecuteGlobalWorkflow_RBACRetryRecovery(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	wfCfg := WorkflowConfig{
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				"did:toro:test-agent": {"/status"},
			},
		},
	}

	// LLM self-corrects on the second attempt after receiving faults
	attempt := 0
	llmCallback := func(faults []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		attempt++
		if attempt == 1 {
			// First attempt: bad path
			return []json.RawMessage{
				[]byte(`{"op": "add", "path": "/admin_notes", "value": "bad"}`),
			}, nil
		}
		// Second attempt: corrected path
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/status", "value": "CORRECTED"}`),
		}, nil
	}

	onCompleteCalled := false
	onComplete := func(nextState []byte) error {
		onCompleteCalled = true
		return nil
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		wfCfg,
		llmCallback,
		onComplete,
	)

	if err != nil {
		t.Fatalf("expected success after self-correction, got: %v", err)
	}
	if attempt != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempt)
	}
	if !onCompleteCalled {
		t.Fatal("onComplete was not called")
	}
}

func TestExecuteGlobalWorkflow_SchemaRejection(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	wfCfg := WorkflowConfig{
		SchemaString: `{
			"type": "object",
			"properties": {
				"status": {"type": "string"}
			},
			"additionalProperties": false
		}`,
	}

	// LLM always writes a rogue field that violates schema
	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/rogue_field", "value": "123"}`),
		}, nil
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		wfCfg,
		llmCallback,
		nil,
	)

	if err == nil {
		t.Fatal("expected circuit breaker error for schema violation")
	}
	if !strings.Contains(err.Error(), "circuit breaker tripped") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteGlobalWorkflow_LLMCallbackError(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return nil, fmt.Errorf("insufficient funds")
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		WorkflowConfig{},
		llmCallback,
		nil,
	)

	if err == nil {
		t.Fatal("expected error from LLM callback")
	}
	if !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteGlobalWorkflow_OnCompleteError(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/status", "value": "OK"}`),
		}, nil
	}

	onComplete := func(_ []byte) error {
		return fmt.Errorf("downstream publish failed")
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		WorkflowConfig{},
		llmCallback,
		onComplete,
	)

	if err == nil {
		t.Fatal("expected onComplete error to propagate")
	}
	if !strings.Contains(err.Error(), "downstream publish failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteGlobalWorkflow_PublishesToJetStream(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/status", "value": "DONE"}`),
		}, nil
	}

	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		WorkflowConfig{},
		llmCallback,
		nil,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify something was published to workflow.trace.*
	found := false
	bus.mu.Lock()
	for subject := range bus.messages {
		if strings.HasPrefix(subject, "workflow.trace.") {
			found = true
			break
		}
	}
	bus.mu.Unlock()

	if !found {
		t.Fatal("expected a message published to workflow.trace.* but found none")
	}
}

func TestExecuteGlobalWorkflow_NilOnComplete(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	db := newTestDB()

	llmCallback := func(_ []redux.DomainFault, _ uint64, _ []byte) ([]json.RawMessage, error) {
		return []json.RawMessage{
			[]byte(`{"op": "add", "path": "/status", "value": "OK"}`),
		}, nil
	}

	// nil onComplete should not panic
	err := agent.ExecuteGlobalWorkflow(
		context.Background(),
		db,
		pgtype.UUID{},
		WorkflowConfig{},
		llmCallback,
		nil,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBaseAgent_Start_Success(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	// QueueGroup/DurableName are derived from DID in NewBaseAgent();
	// here they are pre-set in newTestAgent() to avoid needing a real KeyPair.

	err := agent.Start()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify Almanac registration was published
	published := bus.published(core.SubjectAlmanacRegister)
	if len(published) != 1 {
		t.Errorf("expected 1 published message to %s, got %d", core.SubjectAlmanacRegister, len(published))
	}

	// Verify the registration payload contains activity_type
	var reg map[string]interface{}
	if err := json.Unmarshal(published[0], &reg); err != nil {
		t.Fatalf("could not unmarshal registration payload: %v", err)
	}
	if reg["did"] != "did:toro:test-agent" {
		t.Errorf("unexpected DID in registration: %v", reg["did"])
	}
}

func TestBaseAgent_Start_RegistrationError(t *testing.T) {
	bus := newMockBus()
	bus.publishErr = errors.New("publish failed")
	agent := newTestAgent(bus)

	err := agent.Start()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Error format changed: now includes DID not AgentType
	if !strings.Contains(err.Error(), "failed to register with almanac") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBaseAgent_Start_TaskQueueSubscription(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	agent.Cfg.TaskQueue = "tasks.accounting.cleanup.mapping"

	err := agent.Start()
	if err != nil {
		t.Fatalf("expected no error with task queue set, got: %v", err)
	}

	subs := bus.subscriptions()
	// Expect: 1 inbox sub + 1 task_queue sub = 2
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions (inbox + task_queue), got %d: %v", len(subs), subs)
	}

	inboxFound := false
	taskQueueFound := false
	for _, s := range subs {
		if strings.Contains(s, ".inbox") {
			inboxFound = true
		}
		if s == "tasks.accounting.cleanup.mapping" {
			taskQueueFound = true
		}
	}
	if !inboxFound {
		t.Errorf("expected private inbox subscription, got: %v", subs)
	}
	if !taskQueueFound {
		t.Errorf("expected task_queue subscription, got: %v", subs)
	}
}

func TestBaseAgent_Start_NoTaskQueue(t *testing.T) {
	bus := newMockBus()
	agent := newTestAgent(bus)
	// TaskQueue intentionally left empty

	err := agent.Start()
	if err != nil {
		t.Fatalf("expected no error when TaskQueue is empty, got: %v", err)
	}

	subs := bus.subscriptions()
	// Only the private inbox subscription should be present
	if len(subs) != 1 {
		t.Fatalf("expected only 1 subscription (inbox), got %d: %v", len(subs), subs)
	}
	if !strings.Contains(subs[0], ".inbox") {
		t.Errorf("expected inbox subscription, got: %v", subs[0])
	}
}

