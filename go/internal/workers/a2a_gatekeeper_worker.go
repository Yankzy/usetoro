package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// A2AGatekeeperWorker serves as the inbound gateway for Agent-to-Agent (A2A)
// communications over NATS. It evaluates incoming TAP core.Envelopes, executes
// Pre-Authorization locks against the Sender's Micrion wallet, and dispatches
// the workload to the Gatekeeper's LLM session.
type A2AGatekeeperWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &A2AGatekeeperWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

// Init prepares the worker for execution.
func (w *A2AGatekeeperWorker) Init(ctx context.Context) error {
	return nil
}

// Subscriptions defines the NATS subjects this worker listens to.
// By default, it subscribes to the Gatekeeper's designated inbox.
func (w *A2AGatekeeperWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.a2a_gatekeeper"
	}

	subject, _ := core.BuildWorkerInboxFromActivity(activityType)

	group := workerCfg.Group
	if group == "" {
		group = "a2a_gatekeeper"
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

// Handle processes an inbound NATS message representing an A2A interaction.
// It verifies the sender's identity, ensures they have sufficient Micrion funds
// to pay for the interaction, and invokes the Gatekeeper LLM.
func (w *A2AGatekeeperWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// Parse the TAP core envelope
	var envelope core.Envelope
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		w.logger.Error("A2AGatekeeperWorker: malformed envelope", "error", err)
		msg.Term()
		return nil
	}

	w.logger.Info("A2AGatekeeperWorker: received envelope",
		"id", envelope.ID,
		"sender", envelope.SenderDID,
		"performative", envelope.Performative,
	)

	// In Phase 1, we only handle FIPA performatives for A2A negotiation.
	if envelope.Performative != core.PROPOSE && envelope.Performative != core.CFP {
		w.logger.Warn("A2AGatekeeperWorker: unsupported performative", "performative", envelope.Performative)
		msg.Ack()
		return nil
	}

	// 1. Cross-Tenant Pre-Authorization Check
	// Extract the Sender's Tenant UUID from the DID (e.g. did:toro:<uuid>)
	parts := strings.Split(envelope.SenderDID, ":")
	if len(parts) < 3 {
		w.logger.Error("A2AGatekeeperWorker: invalid sender DID", "did", envelope.SenderDID)
		msg.Term()
		return nil
	}
	senderTenantID, err := uuid.Parse(parts[2])
	if err != nil {
		w.logger.Error("A2AGatekeeperWorker: sender DID does not contain valid UUID", "did", envelope.SenderDID)
		msg.Term()
		return nil
	}

	senderWallet, err := w.db.GetWallet(ctx, pgtype.UUID{Bytes: senderTenantID, Valid: true})
	if err != nil {
		w.logger.Error("A2AGatekeeperWorker: failed to get sender wallet", "error", err, "tenant_id", senderTenantID)
		msg.Nak()
		return err
	}

	// Verify the Sender has a positive balance.
	// As per spec: "there should be no predefined token limit, companies have a wallet that their micrions are deducted from"
	if senderWallet.TotalPurchasedMicrions <= senderWallet.TotalBurnedMicrions {
		w.logger.Warn("A2AGatekeeperWorker: sender has insufficient micrions", "tenant_id", senderTenantID)

		// Reply with REFUSE due to insufficient funds
		w.sendReply(ctx, envelope.SenderDID, envelope.ConversationID, core.REFUSE, map[string]string{
			"reason": "insufficient_micrions",
		})
		msg.Ack()
		return nil
	}

	w.logger.Info("A2AGatekeeperWorker: pre-authorization successful", "tenant_id", senderTenantID)

	// 2. Evaluate Pitch (Placeholder for Phase 2 LLM Integration)
	// Here the LLM will be invoked, its costs logged with payer_tenant_id = senderTenantID,
	// and a cross-tenant burn executed via w.db.LogBulkBurn with metadata pointing to the beneficiary.

	// 3. Dispatch the Reply
	// Simulating a successful negotiation acceptance for Phase 1 routing validation.
	err = w.sendReply(ctx, envelope.SenderDID, envelope.ConversationID, core.ACCEPT_PROPOSAL, map[string]string{
		"message": "The gatekeeper has accepted the proposal.",
	})

	if err != nil {
		w.logger.Error("A2AGatekeeperWorker: failed to send reply", "error", err)
		msg.Nak()
		return err
	}

	msg.Ack()
	return nil
}

// sendReply constructs a standard TAP Envelope and dispatches it back to the sender
// directly over NATS, representing the Gatekeeper's response.
func (w *A2AGatekeeperWorker) sendReply(ctx context.Context, toDID, conversationID string, perf core.Performative, body interface{}) error {
	replyEnv, err := core.NewEnvelope(
		uuid.New().String(),
		"did:toro:gatekeeper", // Placeholder for actual Gatekeeper DID
		toDID,
		conversationID,
		perf,
		body,
	)
	if err != nil {
		return err
	}

	replyBytes, err := json.Marshal(replyEnv)
	if err != nil {
		return err
	}

	// Route directly to Sender's Inbox over NATS
	inbox := core.BuildAgentInbox(toDID)
	return w.nc.Publish(inbox, replyBytes)
}
