package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// TwilioSMSWebhookWorker receives inbound SMS messages from Twilio webhooks
// and routes them to the general agent ingress for conversational processing.
type TwilioSMSWebhookWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &TwilioSMSWebhookWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *TwilioSMSWebhookWorker) Init(ctx context.Context) error { return nil }

func (w *TwilioSMSWebhookWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("TwilioSMSWebhookWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("TwilioSMSWebhookWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{
			nats.Durable(durableFromSubject(subject)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		},
	}}
}

func (w *TwilioSMSWebhookWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("twilio(sms): poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload struct {
		From       string `json:"From"`
		To         string `json:"To"`
		Body       string `json:"Body"`
		MessageSid string `json:"MessageSid"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("twilio(sms): failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.Body == "" || payload.From == "" {
		w.logger.Error("twilio(sms): missing required fields (From, Body)")
		msg.Ack()
		return nil
	}

	// Resolve entity by the toro phone number (To field)
	var entityID pgtype.UUID
	if payload.To != "" {
		var err error
		entityID, err = w.db.GetEntityIDByEmail(ctx, payload.To)
		if err != nil {
			w.logger.Warn("twilio(sms): could not resolve entity for phone", "phone", payload.To, "error", err)
		}
	}

	// Persist to conversations table
	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   entityID,
		Source:     "sms",
		ExternalID: payload.MessageSid,
		FromHandle: payload.From,
		ToHandle:   payload.To,
		BodyText:   pgtype.Text{String: payload.Body, Valid: true},
	})

	w.logger.Info("twilio(sms): saved inbound SMS", "message_sid", payload.MessageSid, "from", payload.From)

	// Route to general agent ingress
	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("twilio(sms): failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      payload.Body,
		"entity_id":   entityID,
		"from_handle": payload.From,
		"to_handle":   payload.To,
		"source":      "sms",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("twilio(sms): failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("twilio(sms): routed to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}
