package approval

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
)

type ApprovalPayload struct {
	SessionID string `json:"session_id"`
	RealmID   string `json:"realm_id"`
	RowID     string `json:"row_id"`
}

type ApprovalAgent struct {
	logger *slog.Logger
	bus    agent.EventBus
	cfg    agent.AgentConfig
	mem    agent.MemoryStore
	kp     *identity.KeyPair
	sub    *nats.Subscription
}

func NewAgent(logger *slog.Logger, bus agent.EventBus, cfg agent.AgentConfig, mem agent.MemoryStore) agent.Runnable {
	kp, _ := identity.GenerateKeyPair()
	cfg.DID = identity.CreateDID(kp.Public)
	return &ApprovalAgent{
		logger: logger,
		bus:    bus,
		cfg:    cfg,
		mem:    mem,
		kp:     kp,
	}
}

func (a *ApprovalAgent) Start() error {
	a.logger.Info("🤖 TAP Approval Agent Initializing...", "did", a.cfg.DID)

	// Register with Almanac
	inbox := core.BuildAgentInbox(a.cfg.DID)
	regPayload := map[string]interface{}{
		"did":       a.cfg.DID,
		"endpoints": []string{inbox},
		"capabilities": []map[string]interface{}{
			{"type": "accounting.approval"},
		},
		"expiry": time.Now().Add(24 * time.Hour).Unix(),
	}
	regBytes, _ := json.Marshal(regPayload)
	a.bus.Publish("almanac.register", regBytes)

	// Listen for the CPA approval event
	topic := "accounting.approved"
	sub, err := a.bus.QueueSubscribe(topic, "approval-group", func(msg *nats.Msg) {
		a.logger.Info("📡 [DEBUG] approval-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleApproval(context.Background(), msg); err != nil {
			a.logger.Error("Transient error processing message, nacking", "error", err)
			msg.Nak()
			return
		}

		msg.Ack()
	}, nats.Durable("approval-agent-durable"), nats.DeliverAll(), nats.AckExplicit())
	
	if err != nil {
		return err
	}
	a.sub = sub

	a.logger.Info("👂 Listening for CPA Approvals", "topic", topic)
	return nil
}

func (a *ApprovalAgent) Stop() error {
	if a.sub != nil {
		return nil // Simplification due to interface abstractions
	}
	return nil
}

func (a *ApprovalAgent) handleApproval(ctx context.Context, msg *nats.Msg) error {
	a.logger.Info("📦 [DEBUG] parsing approval payload", "data_len", len(msg.Data))

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
		a.logger.Info("🔄 Initiating QBO Remote Sync", 
			"session_id", payload.SessionID, 
			"realm_id", payload.RealmID,
			"row_id", payload.RowID)
			
		// TODO: Trigger actual QBO API sync or publish to QBO worker topic.
		// e.g., a.bus.Publish("tasks.qbo.sync", syncData)
	} else {
		a.logger.Info("📄 No QBO Realm ID found. Skipping sync. Ready for CSV download.", 
			"session_id", payload.SessionID,
			"row_id", payload.RowID)
			
		// CSV download requires no special DB preparation, leave data as is
	}

	a.logger.Info("✅ Successfully processed approved transaction event!")
	return nil
}
