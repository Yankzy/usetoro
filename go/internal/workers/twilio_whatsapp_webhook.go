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

// TwilioWhatsAppWebhookWorker receives inbound WhatsApp messages from Twilio
// webhooks and routes them to the general agent ingress.
type TwilioWhatsAppWebhookWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &TwilioWhatsAppWebhookWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *TwilioWhatsAppWebhookWorker) Init(ctx context.Context) error { return nil }

func (w *TwilioWhatsAppWebhookWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("TwilioWhatsAppWebhookWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("TwilioWhatsAppWebhookWorker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (w *TwilioWhatsAppWebhookWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("twilio(wa): poison pill exceeded retries", "subject", msg.Subject)
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
		w.logger.Error("twilio(wa): failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.Body == "" || payload.From == "" {
		w.logger.Error("twilio(wa): missing required fields (From, Body)")
		msg.Ack()
		return nil
	}

	// Resolve entity by the toro WhatsApp number
	var entityID pgtype.UUID
	if payload.To != "" {
		var err error
		entityID, err = w.db.GetEntityIDByEmail(ctx, payload.To)
		if err != nil {
			w.logger.Warn("twilio(wa): could not resolve entity", "handle", payload.To, "error", err)
		}
	}

	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   entityID,
		Source:     "whatsapp",
		ExternalID: payload.MessageSid,
		FromHandle: payload.From,
		ToHandle:   payload.To,
		BodyText:   pgtype.Text{String: payload.Body, Valid: true},
	})

	w.logger.Info("twilio(wa): saved inbound WhatsApp", "message_sid", payload.MessageSid, "from", payload.From)

	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("twilio(wa): failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      payload.Body,
		"entity_id":   entityID,
		"from_handle": payload.From,
		"to_handle":   payload.To,
		"source":      "whatsapp",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("twilio(wa): failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("twilio(wa): routed to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}
