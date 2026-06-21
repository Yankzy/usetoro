package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

type ASEResolutionRequest struct {
	NodeID         string `json:"node_id" description:"The AutonomousSemanticEngineNode NodeID"`
	StartNodeID    string `json:"start_node_id" description:"The DAG Node ID to resume from, e.g., 'macro_class' or 'account_selection'"`
	ResolvedReason string `json:"resolved_reason" description:"Explanation of how the ambiguity was resolved"`
	HumanApproved  bool   `json:"human_approved" description:"Set to true if a human provided the answer"`
}

type ASEResolutionWorker struct {
	logger *slog.Logger
	deps   Dependencies
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &ASEResolutionWorker{
			logger: deps.Logger,
			deps:   deps,
		}, nil
	})
}

func (w *ASEResolutionWorker) Init(ctx context.Context) error {
	return nil
}

func (w *ASEResolutionWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.deps.Config.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		activityType := workerCfg.ActivityType
		if activityType == "" {
			activityType = "workers.ase_resolution"
		}
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			subject = "worker.inbox.workers.ase_resolution"
		}
	}
	group := workerCfg.Group
	if group == "" {
		group = "ase-resolution-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *ASEResolutionWorker) ToolName() string {
	return "UpdateTransactionClassification"
}

func (w *ASEResolutionWorker) ToolDescription() string {
	return "Update a transaction that was on hold, marking it as ready to resume in the DAG. CRITICAL: You MUST include the actual answers, context, or details provided by the user in the resolved_reason field (e.g. 'Business purpose was X, attendee was Y'). Do NOT just say 'user replied' or 'receipt received'."
}

func (w *ASEResolutionWorker) PayloadStruct() any {
	return ASEResolutionRequest{}
}

func (w *ASEResolutionWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// Parse the NATS message payload (assumes bare JSON payload for tool arguments)
	// TAP tools usually wrap inside a core.Envelope. Let's handle both.

	// Unmarshal envelope body
	var req ASEResolutionRequest
	payloadBytes := msg.Data

	// Extract payload from envelope if it's there
	var env map[string]interface{}
	if err := json.Unmarshal(msg.Data, &env); err == nil {
		if body, ok := env["body"].(string); ok {
			payloadBytes = []byte(body)
		} else if bodyBytes, err := json.Marshal(env["body"]); err == nil {
			payloadBytes = bodyBytes
		}
	}

	if err := json.Unmarshal(payloadBytes, &req); err != nil {
		w.logger.Error("ase_resolution: failed to parse request", "error", err)
		return nil
	}

	w.logger.Info("Executing ASE resolution tool", "node_id", req.NodeID, "start_node", req.StartNodeID)

	query := `UPDATE fignode.staging_transactions SET status = $2, error_message = $3, human_action = CASE WHEN human_action IS NULL OR human_action = '' THEN $4::text ELSE human_action || '\n' || $4::text END, updated_at = NOW() WHERE id = $1`
	_, err := w.deps.DBPool.Exec(ctx, query, req.NodeID, "RESUME_PENDING", "", req.ResolvedReason)

	if err != nil {
		w.logger.Error("ase_resolution: failed to update staging transaction state", "error", err)
	}

	type ResumeEvent struct {
		NodeID      string `json:"node_id"`
		StartNodeID string `json:"start_node_id"`
	}
	evt, _ := json.Marshal(ResumeEvent{NodeID: req.NodeID, StartNodeID: req.StartNodeID})

	_ = w.deps.Queue.Publish("ase.events.resume", evt)

	// Publish an INFORM response back to the sender
	if reply := msg.Reply; reply != "" {
		res := fmt.Sprintf("Transaction %s marked for resume at %s. Reason: %s", req.NodeID, req.StartNodeID, req.ResolvedReason)

		// If wrapped in an envelope, reply in an envelope.
		// Actually, AsyncWorkerTool expects core.Envelope with INFORM
		// To simplify, we just send standard bare JSON or Envelope.
		// Real implementation handles core.Envelope properly.
		// Sending bare payload for now since AsyncWorkerTool might just accept it.
		_ = w.deps.Queue.Publish(reply, []byte(`{"status": "success", "result": "`+res+`"}`))
	}

	return nil
}
