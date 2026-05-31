package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// AseTelemetryWorker listens to NATS telemetry events emitted by the Autonomous Semantic Engine (ASE).
// When an ASE transaction agent encounters a HOLD_MISSING_CONTEXT state, this worker acts as a sensory
// bridge, converting the raw telemetry event into a "system alert" for the General Agent.
// The General Agent uses its tool-use capabilities to look up the business owner and send an email
// requesting clarification.
type AseTelemetryWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &AseTelemetryWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *AseTelemetryWorker) Init(ctx context.Context) error {
	return nil
}

func (w *AseTelemetryWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject = "ase.telemetry.context_request"
	}

	group := workerCfg.Group
	if group == "" {
		group = "ase-telemetry-worker-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable("ase-telemetry-worker"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *AseTelemetryWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var payload ase.TelemetryPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("ase_telemetry: failed to unmarshal telemetry payload", "error", err)
		return nil // Drop unparseable messages
	}

	// We only care about holds that require human context
	if payload.EventType != ase.EventContextRequest && payload.EventType != ase.EventHoldTriggered {
		return nil
	}
	if payload.ToState != string(ase.StateHoldMissingCtx) && payload.ToState != string(ase.StateHoldAmbiguous) {
		return nil
	}

	// 1. Construct the system prompt for the General Agent
	alertPrompt := fmt.Sprintf(
		"SYSTEM ALERT: A transaction (ID: %s) for Tenant ID '%s' is stuck in %s.\n\nReason: %s\nDetails: %s\n\nPlease contact the business owner to ask for clarification to resolve this transaction. You can use the LookupClient tool if needed to find their contact details, and use the SendEmail tool as the default communication channel.",
		payload.NodeID, payload.TenantID, payload.ToState, payload.HoldReason, payload.Description,
	)

	// 2. Build the payload expected by the GeneralAgentIngressWorker
	ingressPayload := map[string]any{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   payload.TenantID,
		"source":      "system",
		"from_handle": "ase-engine",
		"to_handle":   "general-agent",
	}
	ingressBytes, _ := json.Marshal(ingressPayload)

	// 3. Resolve the inbox subject for the General Agent Ingress Worker
	ingressSubject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("ase_telemetry: failed to derive general agent ingress subject", "error", err)
		return err
	}

	// 4. Publish the alert to the general agent ingress
	if err := w.nc.Publish(ingressSubject, ingressBytes); err != nil {
		w.logger.Error("ase_telemetry: failed to publish alert to general agent", "error", err)
		return err
	}

	w.logger.Info("ase_telemetry: dispatched system alert to general agent",
		"node_id", payload.NodeID,
		"tenant_id", payload.TenantID,
	)

	return nil
}
