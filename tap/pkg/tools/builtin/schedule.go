package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// ScheduleTool sets background timers (one-shot or recurring cron) that send
// INFORM envelopes to the calling agent's inbox when they fire. This lets
// agents sleep and wake themselves up without blocking the LLM loop.
type ScheduleTool struct {
	Bus      core.EventBus
	AgentDID string
	Logger   *slog.Logger

	mu     sync.Mutex
	timers map[string]*timerEntry
}

// timerEntry tracks a single active schedule — either a one-shot timer or a cron job.
type timerEntry struct {
	timer *time.Timer // non-nil for one-shot
	cron  *cron.Cron  // non-nil for recurring
}

func (t *ScheduleTool) Name() string { return "Schedule" }
func (t *ScheduleTool) Description() string {
	return "Set a background timer (one-shot or recurring cron). When the timer fires, you will receive an INFORM message " +
		"with the provided prompt. Use this to schedule follow-ups, polling checks, or delayed actions without blocking " +
		"execution. Specify exactly one of 'duration_seconds' (one-shot) or 'cron_expression' (recurring). " +
		"For recurring cron jobs, optionally set 'max_iterations' to limit how many times it fires."
}

func (t *ScheduleTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"duration_seconds": {
				"type": "integer",
				"description": "Number of seconds to wait before firing a one-shot timer. Must be between 1 and 86400 (24 hours). Mutually exclusive with cron_expression."
			},
			"cron_expression": {
				"type": "string",
				"description": "A standard 5-field cron expression for recurring schedules (minute hour day-of-month month day-of-week). Example: '*/5 * * * *' for every 5 minutes. Mutually exclusive with duration_seconds."
			},
			"max_iterations": {
				"type": "integer",
				"description": "Optional. Maximum number of times the cron schedule will fire before stopping. Only applicable with cron_expression. Defaults to unlimited."
			},
			"prompt": {
				"type": "string",
				"description": "The message to include in the INFORM notification when the timer fires. Should describe what to do when waking up, e.g. 'Check if the build has completed'."
			}
		},
		"required": ["prompt"]
	}`)
}

func (t *ScheduleTool) Call(ctx context.Context, input map[string]any) (string, error) {
	// --- Parse prompt ---
	prompt, ok := input["prompt"].(string)
	if !ok || prompt == "" {
		return "", fmt.Errorf("prompt must be a non-empty string")
	}

	hasDuration := input["duration_seconds"] != nil
	hasCron := input["cron_expression"] != nil

	if hasDuration && hasCron {
		return "", fmt.Errorf("specify exactly one of duration_seconds or cron_expression, not both")
	}
	if !hasDuration && !hasCron {
		return "", fmt.Errorf("specify exactly one of duration_seconds or cron_expression")
	}

	convID, _ := ctx.Value(tools.ConversationIDKey{}).(string)
	if convID == "" {
		convID = uuid.New().String()
	}

	if hasDuration {
		return t.scheduleOneShot(input, prompt, convID)
	}
	return t.scheduleCron(input, prompt, convID)
}

// scheduleOneShot sets a one-shot timer using time.AfterFunc.
func (t *ScheduleTool) scheduleOneShot(input map[string]any, prompt, convID string) (string, error) {
	durationSec, err := parseIntParam(input, "duration_seconds")
	if err != nil {
		return "", err
	}
	if durationSec < 1 || durationSec > 86400 {
		return "", fmt.Errorf("duration_seconds must be between 1 and 86400, got %d", durationSec)
	}

	timerID := uuid.New().String()
	duration := time.Duration(durationSec) * time.Second

	timer := time.AfterFunc(duration, func() {
		t.fireAndPublish(timerID, prompt, convID)

		t.mu.Lock()
		delete(t.timers, timerID)
		t.mu.Unlock()
	})

	t.trackEntry(timerID, &timerEntry{timer: timer})

	return fmt.Sprintf("Timer %s set for %d seconds. You will receive an INFORM message with your prompt when it fires.", timerID, durationSec), nil
}

// scheduleCron sets a recurring cron schedule.
func (t *ScheduleTool) scheduleCron(input map[string]any, prompt, convID string) (string, error) {
	cronExpr, ok := input["cron_expression"].(string)
	if !ok || cronExpr == "" {
		return "", fmt.Errorf("cron_expression must be a non-empty string")
	}

	maxIter := 0 // 0 = unlimited
	if input["max_iterations"] != nil {
		n, err := parseIntParam(input, "max_iterations")
		if err != nil {
			return "", fmt.Errorf("max_iterations: %w", err)
		}
		if n < 1 {
			return "", fmt.Errorf("max_iterations must be >= 1, got %d", n)
		}
		maxIter = n
	}

	// Validate the cron expression before starting.
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(cronExpr); err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
	}

	timerID := uuid.New().String()
	c := cron.New(cron.WithParser(parser))

	var iterMu sync.Mutex
	iterCount := 0

	_, err := c.AddFunc(cronExpr, func() {
		iterMu.Lock()
		iterCount++
		current := iterCount
		iterMu.Unlock()

		t.fireAndPublish(timerID, prompt, convID)

		if maxIter > 0 && current >= maxIter {
			// Stop the cron after max iterations.
			c.Stop()
			t.mu.Lock()
			delete(t.timers, timerID)
			t.mu.Unlock()
			if t.Logger != nil {
				t.Logger.Info("schedule: cron reached max iterations", "timer_id", timerID, "max", maxIter)
			}
		}
	})
	if err != nil {
		return "", fmt.Errorf("failed to schedule cron: %w", err)
	}

	c.Start()
	t.trackEntry(timerID, &timerEntry{cron: c})

	desc := fmt.Sprintf("Cron schedule %s created with expression %q.", timerID, cronExpr)
	if maxIter > 0 {
		desc += fmt.Sprintf(" Will fire at most %d time(s).", maxIter)
	}
	desc += " You will receive an INFORM message each time it triggers."
	return desc, nil
}

// fireAndPublish constructs and publishes the wake-up envelope.
func (t *ScheduleTool) fireAndPublish(timerID, prompt, convID string) {
	inboxSubject := core.BuildAgentInbox(t.AgentDID)

	body := map[string]string{
		"timer_id": timerID,
		"prompt":   prompt,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		if t.Logger != nil {
			t.Logger.Error("schedule: failed to marshal timer body", "error", err, "timer_id", timerID)
		}
		return
	}

	env := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now().UTC(),
		SenderDID:      t.AgentDID,
		ReceiverDID:    t.AgentDID,
		Performative:   core.INFORM,
		ConversationID: convID,
		Body:           bodyBytes,
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		if t.Logger != nil {
			t.Logger.Error("schedule: failed to marshal envelope", "error", err, "timer_id", timerID)
		}
		return
	}

	if err := t.Bus.Publish(inboxSubject, envBytes); err != nil {
		if t.Logger != nil {
			t.Logger.Error("schedule: failed to publish wake-up", "error", err, "timer_id", timerID, "subject", inboxSubject)
		}
		return
	}

	if t.Logger != nil {
		t.Logger.Info("schedule: timer fired", "timer_id", timerID, "subject", inboxSubject)
	}
}

// trackEntry stores a timer/cron entry in the active map.
func (t *ScheduleTool) trackEntry(id string, entry *timerEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timers == nil {
		t.timers = make(map[string]*timerEntry)
	}
	t.timers[id] = entry
}

// CancelTimer stops a previously scheduled timer or cron job by ID.
// Returns true if the entry was found and stopped.
func (t *ScheduleTool) CancelTimer(timerID string) bool {
	t.mu.Lock()
	entry, ok := t.timers[timerID]
	if !ok {
		t.mu.Unlock()
		return false
	}
	delete(t.timers, timerID)
	t.mu.Unlock()

	if entry.timer != nil {
		return entry.timer.Stop()
	}
	if entry.cron != nil {
		entry.cron.Stop()
		return true
	}
	return false
}

// ActiveTimers returns the number of currently pending timers/cron jobs.
func (t *ScheduleTool) ActiveTimers() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.timers)
}

// parseIntParam extracts an integer from the input map, handling both float64
// (standard JSON unmarshaling) and json.Number representations.
func parseIntParam(input map[string]any, key string) (int, error) {
	raw, ok := input[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be a number, got %T", key, raw)
	}
}

func init() {
	Register("Schedule", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &ScheduleTool{
			Bus:      env.Bus,
			AgentDID: env.Config.DID,
			Logger:   logger,
		}
	})
}
