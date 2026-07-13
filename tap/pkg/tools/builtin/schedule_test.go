package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
)

// mockEventBus is a minimal EventBus for testing that captures published messages.
type mockEventBus struct {
	mu       sync.Mutex
	messages []publishedMsg
}

type publishedMsg struct {
	Subject string
	Data    []byte
}

func (m *mockEventBus) Publish(subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, publishedMsg{Subject: subject, Data: data})
	return nil
}

func (m *mockEventBus) PublishCore(string, []byte) error { return nil }
func (m *mockEventBus) RequestWithContext(context.Context, string, []byte) (*nats.Msg, error) {
	return nil, nil
}
func (m *mockEventBus) QueueSubscribe(string, string, nats.MsgHandler, ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func (m *mockEventBus) getMessages() []publishedMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]publishedMsg, len(m.messages))
	copy(cp, m.messages)
	return cp
}

// --- Metadata Tests ---

func TestScheduleTool_Name(t *testing.T) {
	st := &builtin.ScheduleTool{}
	if st.Name() != "Schedule" {
		t.Errorf("Name() = %q, want %q", st.Name(), "Schedule")
	}
}

func TestScheduleTool_Description(t *testing.T) {
	st := &builtin.ScheduleTool{}
	desc := st.Description()
	if desc == "" {
		t.Fatal("Description() returned empty string")
	}
}

func TestScheduleTool_InputSchema(t *testing.T) {
	st := &builtin.ScheduleTool{}

	var schema map[string]any
	if err := json.Unmarshal(st.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("InputSchema missing 'properties'")
	}

	for _, field := range []string{"duration_seconds", "cron_expression", "max_iterations", "prompt"} {
		if _, ok := props[field]; !ok {
			t.Errorf("InputSchema missing %q property", field)
		}
	}

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("InputSchema missing 'required' array")
	}
	foundPrompt := false
	for _, r := range required {
		if s, ok := r.(string); ok && s == "prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Error("'prompt' not in required fields")
	}
}

// --- Validation Tests ---

func TestScheduleTool_NeitherDurationNorCron(t *testing.T) {
	st := &builtin.ScheduleTool{}
	_, err := st.Call(context.Background(), map[string]any{
		"prompt": "check status",
	})
	if err == nil {
		t.Error("expected error when neither duration_seconds nor cron_expression provided")
	}
}

func TestScheduleTool_BothDurationAndCron(t *testing.T) {
	st := &builtin.ScheduleTool{}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(10),
		"cron_expression":  "*/5 * * * *",
		"prompt":           "check status",
	})
	if err == nil {
		t.Error("expected error when both duration_seconds and cron_expression provided")
	}
}

func TestScheduleTool_DurationTooLow(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:test"}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(0),
		"prompt":           "check status",
	})
	if err == nil {
		t.Error("expected error for duration_seconds < 1")
	}
}

func TestScheduleTool_DurationTooHigh(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:test"}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(86401),
		"prompt":           "check status",
	})
	if err == nil {
		t.Error("expected error for duration_seconds > 86400")
	}
}

func TestScheduleTool_MissingPrompt(t *testing.T) {
	st := &builtin.ScheduleTool{}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(10),
	})
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestScheduleTool_EmptyPrompt(t *testing.T) {
	st := &builtin.ScheduleTool{}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(10),
		"prompt":           "",
	})
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestScheduleTool_InvalidDurationType(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:test"}
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": "not-a-number",
		"prompt":           "check status",
	})
	if err == nil {
		t.Error("expected error for non-numeric duration_seconds")
	}
}

func TestScheduleTool_InvalidCronExpression(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:test"}
	_, err := st.Call(context.Background(), map[string]any{
		"cron_expression": "not-a-cron",
		"prompt":          "check status",
	})
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestScheduleTool_MaxIterationsTooLow(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:test"}
	_, err := st.Call(context.Background(), map[string]any{
		"cron_expression": "*/1 * * * *",
		"max_iterations":  float64(0),
		"prompt":          "check status",
	})
	if err == nil {
		t.Error("expected error for max_iterations < 1")
	}
}

// --- One-Shot Functional Tests ---

func TestScheduleTool_OneShotTimerFires(t *testing.T) {
	bus := &mockEventBus{}
	agentDID := "did:toro:scheduler-test"
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: agentDID}

	result, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(1),
		"prompt":           "Check if the build completed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	if st.ActiveTimers() != 1 {
		t.Errorf("ActiveTimers() = %d, want 1", st.ActiveTimers())
	}

	// Wait for the timer to fire.
	time.Sleep(1500 * time.Millisecond)

	msgs := bus.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}

	expectedSubject := core.BuildAgentInbox(agentDID)
	if msgs[0].Subject != expectedSubject {
		t.Errorf("published to %q, want %q", msgs[0].Subject, expectedSubject)
	}

	var env core.Envelope
	if err := json.Unmarshal(msgs[0].Data, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if env.Performative != core.INFORM {
		t.Errorf("Performative = %q, want %q", env.Performative, core.INFORM)
	}
	if env.SenderDID != agentDID {
		t.Errorf("SenderDID = %q, want %q", env.SenderDID, agentDID)
	}
	if env.ReceiverDID != agentDID {
		t.Errorf("ReceiverDID = %q, want %q", env.ReceiverDID, agentDID)
	}

	var body map[string]string
	if err := json.Unmarshal(env.Body, &body); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	if body["prompt"] != "Check if the build completed" {
		t.Errorf("body prompt = %q, want %q", body["prompt"], "Check if the build completed")
	}
	if body["timer_id"] == "" {
		t.Error("body missing timer_id")
	}

	if st.ActiveTimers() != 0 {
		t.Errorf("ActiveTimers() = %d after firing, want 0", st.ActiveTimers())
	}
}

func TestScheduleTool_CancelOneShot(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:cancel-test"}

	result, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(5),
		"prompt":           "This should never fire",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Extract timer ID from the result string: "Timer <uuid> set for..."
	var timerID string
	if n, _ := fmt.Sscanf(result, "Timer %s set", &timerID); n != 1 {
		t.Fatalf("could not parse timer ID from result: %s", result)
	}

	if st.ActiveTimers() != 1 {
		t.Errorf("ActiveTimers() = %d, want 1", st.ActiveTimers())
	}

	stopped := st.CancelTimer(timerID)
	if !stopped {
		t.Error("CancelTimer returned false, expected true")
	}
	if st.ActiveTimers() != 0 {
		t.Errorf("ActiveTimers() = %d after cancel, want 0", st.ActiveTimers())
	}

	time.Sleep(200 * time.Millisecond)
	msgs := bus.getMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cancel, got %d", len(msgs))
	}
}

func TestScheduleTool_CancelNonexistent(t *testing.T) {
	st := &builtin.ScheduleTool{}
	stopped := st.CancelTimer("nonexistent-id")
	if stopped {
		t.Error("CancelTimer returned true for nonexistent timer")
	}
}

func TestScheduleTool_MultipleOneShot(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:multi-test"}

	for i := 0; i < 3; i++ {
		_, err := st.Call(context.Background(), map[string]any{
			"duration_seconds": float64(1),
			"prompt":           fmt.Sprintf("timer %d", i),
		})
		if err != nil {
			t.Fatalf("timer %d: unexpected error: %v", i, err)
		}
	}

	if st.ActiveTimers() != 3 {
		t.Errorf("ActiveTimers() = %d, want 3", st.ActiveTimers())
	}

	time.Sleep(1500 * time.Millisecond)

	msgs := bus.getMessages()
	if len(msgs) != 3 {
		t.Errorf("expected 3 published messages, got %d", len(msgs))
	}
	if st.ActiveTimers() != 0 {
		t.Errorf("ActiveTimers() = %d after all fired, want 0", st.ActiveTimers())
	}
}

func TestScheduleTool_ReturnsImmediately(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:instant-test"}

	start := time.Now()
	_, err := st.Call(context.Background(), map[string]any{
		"duration_seconds": float64(60),
		"prompt":           "far future",
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call() must return instantly (well under 100ms), not block for 60s.
	if elapsed > 100*time.Millisecond {
		t.Errorf("Call() took %v, expected < 100ms (non-blocking)", elapsed)
	}

	// Clean up the pending timer.
	st.CancelTimer("")
}

// --- Cron Functional Tests ---

func TestScheduleTool_CronCreates(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:cron-test"}

	result, err := st.Call(context.Background(), map[string]any{
		"cron_expression": "*/5 * * * *",
		"prompt":          "poll deployment status",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Cron schedule") {
		t.Errorf("result missing 'Cron schedule': %s", result)
	}
	if !strings.Contains(result, "*/5 * * * *") {
		t.Errorf("result missing cron expression: %s", result)
	}

	if st.ActiveTimers() != 1 {
		t.Errorf("ActiveTimers() = %d, want 1", st.ActiveTimers())
	}

	// Clean up — stop the cron so it doesn't keep running.
	// Extract timer ID from result: "Cron schedule <uuid> created..."
	var cronID string
	if n, _ := fmt.Sscanf(result, "Cron schedule %s created", &cronID); n == 1 {
		st.CancelTimer(cronID)
	}
}

func TestScheduleTool_CronWithMaxIterations(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:cron-max-test"}

	result, err := st.Call(context.Background(), map[string]any{
		"cron_expression": "*/5 * * * *",
		"max_iterations":  float64(3),
		"prompt":          "health check",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "at most 3 time(s)") {
		t.Errorf("result missing max iterations info: %s", result)
	}

	// Clean up.
	var cronID string
	if n, _ := fmt.Sscanf(result, "Cron schedule %s created", &cronID); n == 1 {
		st.CancelTimer(cronID)
	}
}

func TestScheduleTool_CancelCron(t *testing.T) {
	bus := &mockEventBus{}
	st := &builtin.ScheduleTool{Bus: bus, AgentDID: "did:toro:cron-cancel-test"}

	result, err := st.Call(context.Background(), map[string]any{
		"cron_expression": "*/1 * * * *",
		"prompt":          "should be cancelled",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cronID string
	if n, _ := fmt.Sscanf(result, "Cron schedule %s created", &cronID); n != 1 {
		t.Fatalf("could not parse cron ID from result: %s", result)
	}

	stopped := st.CancelTimer(cronID)
	if !stopped {
		t.Error("CancelTimer returned false for cron, expected true")
	}
	if st.ActiveTimers() != 0 {
		t.Errorf("ActiveTimers() = %d after cancel, want 0", st.ActiveTimers())
	}
}
