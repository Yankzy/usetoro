package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
)

type DebugWorker struct {
	db     *database.Queries
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &DebugWorker{
			db:     deps.Store.Queries,
			pool:   deps.Store.Pool,
			logger: deps.Logger.With("worker", "debug"),
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *DebugWorker) Init(ctx context.Context) error {
	w.logger.Info("DebugWorker initialized")
	return nil
}

func (w *DebugWorker) Subscriptions() []SubscriptionConfig {
	subject := "worker.inbox.debug"
	if w.cfg != nil {
		_, workerCfg := w.cfg.Workers.GetForWorker(w)
		if workerCfg.Subject != "" {
			subject = workerCfg.Subject
		}
	}
	group := groupFromSubject(subject)

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

func (w *DebugWorker) Handle(ctx context.Context, msg *nats.Msg) error {

	w.logger.Info("HOT RELOAD IS WORKING NOW")

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("DebugWorker: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// Parse the payload
	var payload map[string]any
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("DebugWorker: failed to unmarshal payload", "error", err)
		return nil
	}
	w.logger.Info("DebugWorker", "DEBUG_PAYLOAD", payload)

	return nil
}

// Ensure DebugWorker satisfies the Worker interface at compile time.
var _ Worker = (*DebugWorker)(nil)
