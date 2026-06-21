package workers

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

type SlackInteractivityWorker struct {
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
		return &SlackInteractivityWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *SlackInteractivityWorker) Init(ctx context.Context) error { return nil }

func (w *SlackInteractivityWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("SlackInteractivityWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("SlackInteractivityWorker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (w *SlackInteractivityWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("slack interactivity: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// Payload is application/x-www-form-urlencoded
	parsed, err := url.ParseQuery(string(msg.Data))
	if err != nil {
		w.logger.Error("slack interactivity: failed to parse query", "error", err)
		msg.Term()
		return nil
	}

	payloadJSON := parsed.Get("payload")
	if payloadJSON == "" {
		w.logger.Error("slack interactivity: missing payload field")
		msg.Term()
		return nil
	}

	w.logger.Info("slack interactivity: received payload (stub)", "payload", payloadJSON)

	// TODO: Parse the JSON, map the team_id to tenant_id, and process block actions or modal submissions

	msg.Ack()
	return nil
}
