package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// TelegramWebhookWorker receives inbound messages from the Telegram Bot API
// and routes them to the general agent ingress.
type TelegramWebhookWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &TelegramWebhookWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *TelegramWebhookWorker) Init(ctx context.Context) error { return nil }

func (w *TelegramWebhookWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("TelegramWebhookWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("TelegramWebhookWorker: failed to derive inbox", "activity_type", activityType, "error", err)
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

// telegramUpdate matches the Telegram Bot API Update structure.
type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64            `json:"message_id"`
	From      telegramUser     `json:"from"`
	Chat      telegramChat     `json:"chat"`
	Text      string           `json:"text"`
	Date      int64            `json:"date"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

func (w *TelegramWebhookWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("telegram: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var update telegramUpdate
	if err := json.Unmarshal(msg.Data, &update); err != nil {
		w.logger.Error("telegram: failed to unmarshal update", "error", err)
		msg.Term()
		return nil
	}

	if update.Message == nil || update.Message.Text == "" {
		w.logger.Debug("telegram: skipping non-text update", "update_id", update.UpdateID)
		msg.Ack()
		return nil
	}

	tm := update.Message
	fromHandle := formatTelegramUser(tm.From)
	toHandle := fmt.Sprintf("bot:%d", tm.Chat.ID)

	// Entity resolution attempts by chat ID (best-effort)
	var entityID pgtype.UUID

	// Persist to conversations
	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   entityID,
		Source:     "telegram",
		ExternalID: fmt.Sprintf("tg-%d", update.UpdateID),
		FromHandle: fromHandle,
		ToHandle:   toHandle,
		BodyText:   pgtype.Text{String: tm.Text, Valid: true},
	})

	w.logger.Info("telegram: saved inbound message", "update_id", update.UpdateID, "from", fromHandle)

	// Route to general agent ingress
	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("telegram: failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      tm.Text,
		"entity_id":   entityID,
		"from_handle": fromHandle,
		"to_handle":   toHandle,
		"source":      "telegram",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("telegram: failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("telegram: routed to general agent ingress", "subject", subject)
	msg.Ack()
	return nil
}

func formatTelegramUser(u telegramUser) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return u.FirstName
}
