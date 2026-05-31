package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type SlackCommandWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.Config.SlackBotToken == "" && deps.Config.SlackClientID == "" {
			return nil, nil // Slack not configured
		}
		return &SlackCommandWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *SlackCommandWorker) Init(ctx context.Context) error { return nil }

func (w *SlackCommandWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("SlackCommandWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("SlackCommandWorker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (w *SlackCommandWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("slack command: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// Payload is application/x-www-form-urlencoded
	parsed, err := url.ParseQuery(string(msg.Data))
	if err != nil {
		w.logger.Error("slack command: failed to parse query", "error", err)
		msg.Term()
		return nil
	}

	teamID := parsed.Get("team_id")
	channelID := parsed.Get("channel_id")
	userID := parsed.Get("user_id")
	command := parsed.Get("command")
	text := parsed.Get("text")

	if teamID == "" || command == "" {
		w.logger.Error("slack command: missing required fields")
		msg.Term()
		return nil
	}

	tenantMap, err := w.db.GetSlackTenantMappingByTeamID(ctx, teamID)
	if err != nil {
		w.logger.Error("slack command: could not find tenant mapping for team_id", "team_id", teamID, "error", err)
		msg.Ack()
		return nil
	}

	fullPrompt := fmt.Sprintf("%s %s", command, text)
	fromHandle := fmt.Sprintf("slack-user:%s", userID)
	toHandle := fmt.Sprintf("slack-channel:%s", channelID)
	externalID := fmt.Sprintf("slack-cmd-%s", uuid.New().String())

	_ = w.db.SaveInboundConversation(ctx, database.SaveInboundConversationParams{
		EntityID:   tenantMap.TenantID,
		Source:     "slack",
		ExternalID: externalID,
		FromHandle: fromHandle,
		ToHandle:   toHandle,
		BodyText:   pgtype.Text{String: fullPrompt, Valid: true},
	})

	subject, err := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
	if err != nil {
		w.logger.Error("slack command: failed to derive general agent ingress subject", "error", err)
		msg.Ack()
		return nil
	}

	eventData := map[string]interface{}{
		"prompt":      fullPrompt,
		"entity_id":   tenantMap.TenantID,
		"from_handle": fromHandle,
		"to_handle":   toHandle,
		"source":      "slack",
	}
	eventBytes, _ := json.Marshal(eventData)
	if err := w.nc.Publish(subject, eventBytes); err != nil {
		w.logger.Error("slack command: failed to publish to general agent ingress", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("slack command: routed to general agent ingress", "command", command, "tenant_id", tenantMap.TenantID)
	msg.Ack()
	return nil
}
