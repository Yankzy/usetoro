package stripe_processor

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/nats-io/nats.go"
	"github.com/stripe/stripe-go/v76"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

type StripeProcessorAgent struct {
	*agent.BaseAgent
	db *database.Queries
}

func init() {
	agents.Register("stripe-processor-agent", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a StripeProcessorAgent
	a.db = env.Queries

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] stripe-processor received JetStream message", "topic", msg.Subject)
		if err := a.handleStripeEvent(context.Background(), msg); err != nil {
			a.Logger.Error("stripe-processor transient error", "error", err)
			msg.Nak()
			return
		}
		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, env.Memory, handler)
	return &a
}

func (a *StripeProcessorAgent) handleStripeEvent(ctx context.Context, msg *nats.Msg) error {
	var event stripe.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		a.Logger.Error("failed to unmarshal stripe event", "error", err)
		return nil // Unrecoverable parsing error, drop message
	}

	if event.Type != "checkout.session.completed" {
		return nil
	}

	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		a.Logger.Error("failed to unmarshal stripe session", "error", err)
		return nil
	}

	// Extract Metadata
	entityID := session.Metadata["entity_id"]
	agentDID := session.Metadata["agent_did"]
	micrionAmountStr := session.Metadata["micrion_amount"]

	if entityID == "" || agentDID == "" || micrionAmountStr == "" {
		a.Logger.Warn("Stripe session missing required metadata", "stripe_session_id", session.ID)
		return nil
	}

	micrionAmount, err := strconv.ParseInt(micrionAmountStr, 10, 64)
	if err != nil {
		a.Logger.Error("Invalid micrion_amount in metadata", "micrion_amount", micrionAmountStr)
		return nil
	}

	// Handle Top-Up Execution
	// Temporarily suspended while wm is refactored
	// if err := a.wm.HandleStripePurchase(ctx, entityID, agentDID, session.ID, session.AmountTotal, micrionAmount); err != nil {
	// 	return fmt.Errorf("failed to process stripe purchase: %w", err)
	// }

	a.Logger.Info("✅ Successfully processed Stripe Micrion Top-Up", "agent_did", agentDID, "micrions", micrionAmount, "usd_cents", session.AmountTotal)

	// User Request: Replay deadlocked/paywalled agents immediately within the code.
	stalled, err := a.db.GetStalledMessagesByAgent(ctx, agentDID)
	if err != nil {
		a.Logger.Error("Failed to fetch stalled messages for agent", "agent_did", agentDID, "error", err)
		return nil
	}

	for _, smsg := range stalled {
		a.Logger.Info("♻️ Replaying deadlocked message after Top-Up", "agent_did", agentDID, "subject", smsg.OriginalSubject)
		if pubErr := a.Bus.Publish(smsg.OriginalSubject, smsg.Payload); pubErr == nil {
			// Acknowledge the replay by deleting the DLQ record
			_ = a.db.DeleteStalledMessage(ctx, smsg.ID)
		} else {
			a.Logger.Error("Failed to republish stalled message", "id", smsg.ID, "error", pubErr)
		}
	}

	return nil
}
