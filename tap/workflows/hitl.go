package workflows

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tidwall/gjson"
)

const (
	// SuspensionKindHITL identifies a suspension that was triggered by a hitl.* step,
	// as opposed to a route-based ambiguity or an agent failure.
	SuspensionKindHITL = "hitl"

	// HITLPendingSubject is the NATS subject the Orchestrator publishes to when a
	// workflow is suspended at a hitl.* gate. The frontend subscribes to this to
	// render the review UI.
	HITLPendingSubject = "workflow.events.hitl_pending"

	// HITLPrefixActivity is the reserved activity_type prefix for built-in HITL steps.
	HITLPrefixActivity = "hitl."
)

// HITLPendingEvent is the payload published to HITLPendingSubject when the
// Orchestrator suspends at a human-review gate.
type HITLPendingEvent struct {
	InstanceID string `json:"instance_id"`
	EntityID   string `json:"entity_id"`
	StepID     string `json:"step_id"`
	Status     string `json:"status"` // always "hitl_pending"

	// Routing keys extracted from step outputs / trigger so the WS consumer
	// can deliver the event to the correct room.
	SessionID string `json:"session_id,omitempty"`
	RealmID   string `json:"realm_id,omitempty"`

	// Config fields copied from the YAML step.config block.
	Scope       string `json:"scope,omitempty"`         // "session" | "row"
	Prompt      string `json:"prompt,omitempty"`        // human-readable instruction
	AllowEdits  bool   `json:"allow_edits"`             // can the reviewer override AI output?
	RejectAction string `json:"reject_action,omitempty"` // "abort" | "retry_step:<id>"
	MessageType  string `json:"message_type,omitempty"`  // WS message type override (default: derived from activity type)

	// The collected outputs from every step completed before the gate.
	// Keys are step IDs, values are the raw proof JSON from each step.
	StepOutputs map[string]json.RawMessage `json:"step_outputs"`

	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339, derived from step timeout
	Timestamp string `json:"timestamp"`
}

// suspendForHITL is called by scheduleReadySteps when it encounters a step whose
// activity_type starts with "hitl.". It performs the suspend-and-notify
// lifecycle entirely inside the Orchestrator without dispatching to any agent or worker.
//
// Contract:
//   - Sets state.Suspended = true so that no further steps are dispatched.
//   - Marks the HITL step as "active" (it will be completed by handleWorkflowResume).
//   - Persists the updated state.
//   - Publishes a HITLPendingEvent so the frontend can render the review UI.
//
// NOTE: state persistence is the caller's responsibility — this method only mutates
// the in-memory state and publishes the event.
func (o *Orchestrator) suspendForHITL(step WorkflowStep, state *InstanceState, entityID pgtype.UUID) error {
	// --- 1. Mark suspension ---
	state.Suspended = true
	state.SuspensionKind = SuspensionKindHITL
	state.SuspensionStep = step.ID
	state.SuspensionReason = fmt.Sprintf("Awaiting human review at step '%s'", step.ID)
	// Keep SuspensionRoute at 0; it is unused for HITL suspensions.

	// Mark the step as active so handleWorkflowResume can find and complete it.
	state.ActiveSteps[step.ID] = true

	// --- 2. Extract config values from the YAML step.config block ---
	scope := stringFromConfig(step.Config, "scope")
	prompt := stringFromConfig(step.Config, "prompt")
	allowEdits := boolFromConfig(step.Config, "allow_edits")
	rejectAction := stringFromConfig(step.Config, "reject_action")
	if rejectAction == "" {
		rejectAction = "abort"
	}

	// --- 3. Bundle all step outputs collected so far ---
	stepOutputs := make(map[string]json.RawMessage, len(state.Variables))
	for k, v := range state.Variables {
		stepOutputs[k] = v
	}

	// --- 4. Extract session_id / realm_id from step outputs ---
	sessionID, realmID := extractRoutingKeys(state)

	// --- 5. Derive message_type from config or activity type suffix ---
	messageType := stringFromConfig(step.Config, "message_type")
	if messageType == "" {
		// Default: "hitl.review" -> "review", "hitl.approval" -> "approval"
		if suffix, ok := strings.CutPrefix(step.ActivityType, HITLPrefixActivity); ok {
			messageType = suffix
		}
	}

	// --- 6. Derive expiry from step timeout (best-effort) ---
	expiresAt := ""
	if step.Timeout != "" {
		if dur, err := time.ParseDuration(step.Timeout); err == nil {
			expiresAt = time.Now().UTC().Add(dur).Format(time.RFC3339)
		}
	}

	// --- 7. Build and publish the HITL pending event ---
	entityStr := ""
	if entityID.Valid {
		entityStr = uuid.UUID(entityID.Bytes).String()
	}

	instanceID := ""
	if len(state.InstancePath) > 0 {
		instanceID = state.InstancePath[len(state.InstancePath)-1]
	}

	evt := HITLPendingEvent{
		InstanceID:   instanceID,
		EntityID:     entityStr,
		StepID:       step.ID,
		Status:       "hitl_pending",
		SessionID:    sessionID,
		RealmID:      realmID,
		Scope:        scope,
		Prompt:       prompt,
		AllowEdits:   allowEdits,
		RejectAction: rejectAction,
		MessageType:  messageType,
		StepOutputs:  stepOutputs,
		ExpiresAt:    expiresAt,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("hitl: marshal pending event: %w", err)
	}
	if err := o.bus.Publish(HITLPendingSubject, data); err != nil {
		return fmt.Errorf("hitl: publish pending event: %w", err)
	}

	o.logger.Info("🧑‍💻 HITL gate reached — workflow suspended for human review",
		"instance_id", instanceID,
		"step_id", step.ID,
		"session_id", sessionID,
		"realm_id", realmID,
		"message_type", messageType,
		"scope", scope,
		"allow_edits", allowEdits,
		"expires_at", expiresAt,
	)

	return nil
}

// --- Config helpers --------------------------------------------------------

func stringFromConfig(cfg map[string]interface{}, key string) string {
	if cfg == nil {
		return ""
	}
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func boolFromConfig(cfg map[string]interface{}, key string) bool {
	if cfg == nil {
		return false
	}
	if v, ok := cfg[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// extractRoutingKeys pulls session_id and realm_id from state variables using
// the same priority order as reshapePayloadToSchema:
//  1. TRIGGER (the original workflow trigger payload)
//  2. Step outputs (in none-deterministic order; first found wins)
func extractRoutingKeys(state *InstanceState) (sessionID, realmID string) {
	// Priority 1: TRIGGER
	if raw, ok := state.Variables["TRIGGER"]; ok {
		unwrapped := string(unwrapStepPayload(raw))
		if v := gjson.Get(unwrapped, "session_id"); v.Exists() {
			sessionID = v.String()
		}
		if v := gjson.Get(unwrapped, "realm_id"); v.Exists() {
			realmID = v.String()
		}
	}

	// Priority 2: search step outputs (skip TRIGGER since we already checked it)
	for k, raw := range state.Variables {
		if k == "TRIGGER" {
			continue
		}
		unwrapped := string(unwrapStepPayload(raw))
		if sessionID == "" {
			if v := gjson.Get(unwrapped, "session_id"); v.Exists() {
				sessionID = v.String()
			}
		}
		if realmID == "" {
			if v := gjson.Get(unwrapped, "realm_id"); v.Exists() {
				realmID = v.String()
			}
		}
		if sessionID != "" && realmID != "" {
			break
		}
	}

	return
}
