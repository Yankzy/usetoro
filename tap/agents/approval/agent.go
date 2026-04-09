package approval

import (
	"context"
	"encoding/json"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type ApprovalPayload struct {
	SessionID string `json:"session_id"`
	RealmID   string `json:"realm_id"`
	RowID     string `json:"row_id"`
}

type ApprovalAgent struct {
	*agent.BaseAgent
}

func init() {
	agents.Register("approval-agent", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a ApprovalAgent

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] approval-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleApproval(context.Background(), msg); err != nil {
			a.Logger.Error("Transient error processing message, nacking", "error", err)
			msg.Nak()
			return
		}

		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, env.Memory, handler)
	return &a
}


func (a *ApprovalAgent) handleApproval(ctx context.Context, msg *nats.Msg) error {
	a.Logger.Info("📦 [DEBUG] parsing approval payload", "data_len", len(msg.Data))

	var payload ApprovalPayload

	// Try unwrapping if it's sent as a TAP Envelope
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err == nil && env.Performative != "" {
		var proof core.Proof
		if err := json.Unmarshal(env.Body, &proof); err == nil {
			// Extract payload from proof
			_ = json.Unmarshal(proof.Data, &payload)
		}
	} else {
		// Fallback for direct JSON payload
		_ = json.Unmarshal(msg.Data, &payload)
	}

	if payload.RealmID != "" {
		a.Logger.Info("🔄 Initiating QBO Remote Sync", 
			"session_id", payload.SessionID, 
			"realm_id", payload.RealmID,
			"row_id", payload.RowID)
			
		// TODO: Trigger actual QBO API sync or publish to QBO worker topic.
		// e.g., a.Bus.Publish("tasks.qbo.sync", syncData)
	} else {
		a.Logger.Info("📄 No QBO Realm ID found. Skipping sync. Ready for CSV download.", 
			"session_id", payload.SessionID,
			"row_id", payload.RowID)
			
		// CSV download requires no special DB preparation, leave data as is
	}

	a.Logger.Info("✅ Successfully processed approved transaction event!")
	return nil
}
