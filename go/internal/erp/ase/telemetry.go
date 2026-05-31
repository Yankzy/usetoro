package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// TelemetryEventType categorizes ASE telemetry events.
type TelemetryEventType string

const (
	EventStateTransition TelemetryEventType = "state_transition"
	EventHoldTriggered   TelemetryEventType = "hold_triggered"
	EventContextRequest  TelemetryEventType = "context_request"
	EventCollapseReady   TelemetryEventType = "collapse_ready"
	EventGuardrailBlock  TelemetryEventType = "guardrail_block"
)

// TelemetryPayload is the standardized NATS message body for ASE telemetry events.
type TelemetryPayload struct {
	EventType   TelemetryEventType `json:"event_type"`
	NodeID      string             `json:"node_id"`
	TenantID    string             `json:"tenant_id"`
	FromState   string             `json:"from_state,omitempty"`
	ToState     string             `json:"to_state,omitempty"`
	HoldReason  string             `json:"hold_reason,omitempty"`
	Entropy     float64            `json:"entropy"`
	Confidence  float64            `json:"confidence,omitempty"`
	Description string             `json:"description,omitempty"`
	Probes      int                `json:"lifetime_probes"`
	Timestamp   time.Time          `json:"timestamp"`
}

// TelemetryPublisher emits ASE lifecycle events to NATS for observability
// and virtual workforce coordination (e.g., requesting human input via Slack).
type TelemetryPublisher struct {
	nc     *nats.Conn
	db     *database.Queries
	logger *slog.Logger
}

// NewTelemetryPublisher creates a new telemetry publisher.
func NewTelemetryPublisher(nc *nats.Conn, db *database.Queries, logger *slog.Logger) *TelemetryPublisher {
	return &TelemetryPublisher{
		nc:     nc,
		db:     db,
		logger: logger.With("component", "ase.telemetry"),
	}
}

// subjectForEvent returns the NATS subject for a given event type.
func (tp *TelemetryPublisher) subjectForEvent(eventType TelemetryEventType) string {
	return fmt.Sprintf("ase.telemetry.%s", string(eventType))
}

// PublishStateTransition emits an event when a node changes state.
func (tp *TelemetryPublisher) PublishStateTransition(node *AutonomousSemanticEngineNode, oldState, newState NodeState) {


	payload := TelemetryPayload{
		EventType:  EventStateTransition,
		NodeID:     node.NodeID,
		TenantID:   node.TenantID,
		FromState:  string(oldState),
		ToState:    string(newState),
		Entropy:    node.GetEntropy(),
		Confidence: node.GetConfidence(),
		Probes:     node.GetProbes(),
		Timestamp:  time.Now().UTC(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		tp.logger.Error("failed to marshal telemetry payload", "error", err)
		return
	}

	subject := tp.subjectForEvent(EventStateTransition)
	if err := tp.nc.Publish(subject, data); err != nil {
		tp.logger.Error("failed to publish telemetry", "subject", subject, "error", err)
		return
	}

	tp.logger.Info("telemetry published",
		"subject", subject,
		"node_id", node.NodeID,
		"from", string(oldState),
		"to", string(newState),
		"entropy", payload.Entropy,
	)
}

// PublishHold emits an event when a node enters a HOLD state, requesting
// missing context from the virtual workforce (e.g., Slack notification).
func (tp *TelemetryPublisher) PublishHold(node *AutonomousSemanticEngineNode) {
	desc := formatContextRequest(node)

	payload := TelemetryPayload{
		EventType:   EventHoldTriggered,
		NodeID:      node.NodeID,
		TenantID:    node.TenantID,
		ToState:     string(node.GetState()),
		HoldReason:  node.GetHoldReason(),
		Entropy:     node.GetEntropy(),
		Probes:      node.GetProbes(),
		Description: desc,
		Timestamp:   time.Now().UTC(),
	}

	data, _ := json.Marshal(payload)

	// Publish to the hold-specific subject.
	subject := tp.subjectForEvent(EventHoldTriggered)
	_ = tp.nc.Publish(subject, data)

	// Also publish to context_request for virtual workforce routing.
	ctxSubject := tp.subjectForEvent(EventContextRequest)
	_ = tp.nc.Publish(ctxSubject, data)

	// Proactively request context via Omni Chat Worker
	tp.requestContextViaOmniChat(node, desc)

	tp.logger.Warn("ase node entered HOLD state",
		"node_id", node.NodeID,
		"hold_reason", node.GetHoldReason(),
		"entropy", payload.Entropy,
		"subject", subject,
	)
}

func (tp *TelemetryPublisher) requestContextViaOmniChat(node *AutonomousSemanticEngineNode, bodyText string) {
	if tp.db == nil {
		tp.logger.Warn("cannot send omni chat request: db is nil")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	company, err := tp.db.GetCompanyInfo(ctx, node.TenantID)
	if err != nil {
		tp.logger.Warn("could not find company info to route context request", "tenant_id", node.TenantID, "error", err)
		return
	}

	toEmail := company.Email.String
	if toEmail == "" {
		tp.logger.Warn("company info has no email to route context request", "tenant_id", node.TenantID)
		return
	}

	outProof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
	}

	dataMap := map[string]any{
		"body_text":   bodyText,
		"source":      "email",
		"from_handle": "Toro Virtual Employee",
		"to_handle":   toEmail,
		"subject":     "Missing Context for Transaction",
		"entity_id":   node.TenantID,
	}

	dataBytes, _ := json.Marshal(dataMap)
	outProof.Data = dataBytes
	proofBytes, _ := json.Marshal(outProof)

	outEnv := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:ase",
		ReceiverDID:  "did:toro:worker:omni_chat",
		Performative: core.INFORM,
		Body:         proofBytes,
	}

	outBytes, _ := json.Marshal(outEnv)
	if err := tp.nc.Publish("proof.outgoing.chat", outBytes); err != nil {
		tp.logger.Error("failed to publish context request to omni_chat", "error", err)
	} else {
		tp.logger.Info("dispatched context request via omni_chat", "to", toEmail)
	}
}

// PublishGuardrailBlock emits an event when the State Collapse Guardrail
// prevents a node from transitioning to READY_FOR_SYNC due to low confidence.
func (tp *TelemetryPublisher) PublishGuardrailBlock(node *AutonomousSemanticEngineNode, reason string) {


	payload := TelemetryPayload{
		EventType:   EventGuardrailBlock,
		NodeID:      node.NodeID,
		TenantID:    node.TenantID,
		HoldReason:  reason,
		Entropy:     node.GetEntropy(),
		Confidence:  node.GetConfidence(),
		Probes:      node.GetProbes(),
		Description: fmt.Sprintf("Guardrail blocked transition to READY_FOR_SYNC: unified confidence %.4f < 0.98 threshold. Requires human review or additional LLM probes.", node.GetConfidence()),
		Timestamp:   time.Now().UTC(),
	}

	data, _ := json.Marshal(payload)

	subject := tp.subjectForEvent(EventGuardrailBlock)
	_ = tp.nc.Publish(subject, data)

	tp.logger.Warn("guardrail blocked sync",
		"node_id", node.NodeID,
		"confidence", node.GetConfidence(),
	)
}

// PublishCollapseReady emits an event when a node successfully reaches READY_FOR_SYNC.
func (tp *TelemetryPublisher) PublishCollapseReady(node *AutonomousSemanticEngineNode) {


	payload := TelemetryPayload{
		EventType:  EventCollapseReady,
		NodeID:     node.NodeID,
		TenantID:   node.TenantID,
		Entropy:    node.GetEntropy(),
		Confidence: node.GetConfidence(),
		Probes:     node.GetProbes(),
		Timestamp:  time.Now().UTC(),
	}

	data, _ := json.Marshal(payload)

	subject := tp.subjectForEvent(EventCollapseReady)
	_ = tp.nc.Publish(subject, data)

	tp.logger.Info("ase node ready for sync",
		"node_id", node.NodeID,
		"confidence", node.GetConfidence(),
	)
}

// formatContextRequest builds a human-readable description of what context
// is missing, suitable for routing to Slack or other virtual workforce channels.
func formatContextRequest(node *AutonomousSemanticEngineNode) string {
	switch node.GetState() {
	case StateHoldAmbiguous:
		return fmt.Sprintf(
			"Transaction '%s' cannot be classified with confidence above 0.98. "+
				"Current entropy: %.4f. Top candidate confidence below threshold. "+
				"Human accountant review required. Raw description: '%s', Amount: '%s', Direction: '%s'.",
			node.NodeID, node.GetEntropy(), node.RawDescription, node.RawAmount, node.CashDirection,
		)
	case StateHoldMissingCtx:
		return fmt.Sprintf(
			"Transaction '%s' is missing context for classification. "+
				"Reason: %s. Raw description: '%s', Amount: '%s', Direction: '%s'. "+
				"Requesting additional context from the virtual workforce.",
			node.NodeID, node.GetHoldReason(), node.RawDescription, node.RawAmount, node.CashDirection,
		)
	default:
		return fmt.Sprintf(
			"Transaction '%s' entered HOLD state: %s. Reason: %s.",
			node.NodeID, string(node.GetState()), node.GetHoldReason(),
		)
	}
}

// BuildOnStateChange creates a state change callback that publishes telemetry
// events for hold states and guardrail blocks.
func (tp *TelemetryPublisher) BuildOnStateChange() func(node *AutonomousSemanticEngineNode, oldState, newState NodeState) {
	return func(node *AutonomousSemanticEngineNode, oldState, newState NodeState) {
		tp.PublishStateTransition(node, oldState, newState)

		switch newState {
		case StateHoldAmbiguous, StateHoldMissingCtx:
			tp.PublishHold(node)
		case StateReadyForSync:
			tp.PublishCollapseReady(node)
		}
	}
}
