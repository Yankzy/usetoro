package v2_workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/internal/workers"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// V2PushWorker is the NATS entry point for the V2 QuickBooks parallel pipeline.
// It subscribes to the V2-specific subject, extracts session IDs, and delegates
// to the V2Orchestrator for the 3-phase AI-driven pipeline.
type V2PushWorker struct {
	logger       *slog.Logger
	cfg          *config.Config
	nc           *nats.Conn
	orchestrator *quickbooks.V2Orchestrator
	store        *store.Store
}

// NewV2PushWorker creates a new V2 push worker.
func NewV2PushWorker(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
	store *store.Store,
	pool *pgxpool.Pool,
	connector *connectors.QBOConnector,
	llm *ai.LLMClient,
) *V2PushWorker {
	orch := quickbooks.NewV2Orchestrator(
		logger.With("component", "v2_orchestrator"),
		store.Queries,
		pool,
		connector,
		llm,
	)
	return &V2PushWorker{
		logger:       logger,
		cfg:          cfg,
		nc:           nc,
		orchestrator: orch,
		store:        store,
	}
}

func (w *V2PushWorker) Init(ctx context.Context) error {
	return nil
}

func (w *V2PushWorker) Subscriptions() []workers.SubscriptionConfig {
	const (
		defaultSubject = "events.qbo_v2_parallel.sync"
		durableName    = "qbo-v2-parallel-consumer"
		groupName      = "qbo-v2-parallel-group"
	)

	return []workers.SubscriptionConfig{
		{
			Subject: defaultSubject,
			Group:   groupName,
			Options: []nats.SubOpt{
				nats.Durable(durableName),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// Handle processes a single V2 sync request for a session.
func (w *V2PushWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta != nil && meta.NumDelivered > 3 {
		w.logger.Error("v2_push worker: poison pill exceeded retries",
			"subject", msg.Subject, "delivered", meta.NumDelivered)
		msg.Term()
		return nil
	}

	pgSessionID, err := w.extractSessionID(msg.Data)
	if err != nil {
		w.logger.Warn("v2_push worker: could not extract session_id, ignoring",
			"error", err)
		return nil
	}

	w.logger.Info("v2_push worker: processing session",
		"session_id", pgSessionID)

	if err := w.orchestrator.Run(ctx, pgSessionID); err != nil {
		w.logger.Error("v2_push worker: orchestrator failed",
			"session_id", pgSessionID, "error", err)
		return fmt.Errorf("v2 orchestrator run: %w", err)
	}

	return nil
}

// extractSessionID pulls the session_id from the incoming NATS message.
func (w *V2PushWorker) extractSessionID(data []byte) (pgtype.UUID, error) {
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return pgtype.UUID{}, err
	}
	if payload.SessionID == "" {
		return pgtype.UUID{}, fmt.Errorf("no session_id in payload")
	}

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(payload.SessionID); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid session_id: %w", err)
	}
	return pgSessionID, nil
}

func init() {
	workers.RegisterFactory(func(deps workers.Dependencies) (workers.Worker, error) {
		if deps.QBOConnector == nil {
			deps.Logger.Warn("v2_push worker: QBOConnector not available, skipping")
			return nil, nil
		}
		if deps.LLMClient == nil {
			deps.Logger.Warn("v2_push worker: LLMClient not available, skipping")
			return nil, nil
		}
		return NewV2PushWorker(
			deps.Logger,
			deps.Config,
			deps.Queue,
			deps.Store,
			deps.DBPool,
			deps.QBOConnector,
			deps.LLMClient,
		), nil
	})
}
