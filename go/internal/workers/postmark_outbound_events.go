package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

// PostmarkOutboundEvent represents the payload received from Postmark webhook
type PostmarkOutboundEvent struct {
	RecordType string `json:"RecordType"`
	MessageID  string `json:"MessageID"`
}

type PostmarkOutboundEventsWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PostmarkOutboundEventsWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *PostmarkOutboundEventsWorker) Init(ctx context.Context) error {
	return nil
}

func (w *PostmarkOutboundEventsWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("PostmarkOutboundEventsWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("PostmarkOutboundEventsWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
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

func (w *PostmarkOutboundEventsWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload PostmarkOutboundEvent
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("failed to unmarshal postmark outbound event payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.MessageID == "" {
		w.logger.Warn("postmark outbound event without MessageID", "record_type", payload.RecordType)
		msg.Ack()
		return nil
	}

	params := database.UpdateConversationDeliveryStatusParams{
		ExternalID: payload.MessageID,
	}

	switch payload.RecordType {
	case "Delivery":
		params.Delivered = msg.Data
	case "Bounce":
		params.Bounced = msg.Data
	case "SpamComplaint":
		params.Complained = msg.Data
	case "Open":
		params.Opened = msg.Data
	case "Click":
		params.Clicked = msg.Data
	default:
		w.logger.Warn("unknown postmark outbound record type", "record_type", payload.RecordType)
		msg.Ack()
		return nil
	}

	err := w.db.UpdateConversationDeliveryStatus(ctx, params)
	if err != nil {
		w.logger.Error("failed to update conversation delivery status", "error", err, "message_id", payload.MessageID, "record_type", payload.RecordType)
		msg.Nak()
		return err
	}

	w.logger.Info("successfully updated conversation delivery status", "message_id", payload.MessageID, "record_type", payload.RecordType)
	msg.Ack()
	return nil
}
