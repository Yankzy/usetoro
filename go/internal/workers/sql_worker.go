package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

type SQLWorker struct {
	pool   *pgxpool.Pool
	db     database.Querier // Add this field
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
}

// SQLWorkerPayload defines the highly restricted JSON payload from the Wails frontend.
type SQLWorkerPayload struct {
	QueryID       string `json:"query_id"`
	Args          []any  `json:"args,omitempty"`
	ReturnSubject string `json:"return_subject,omitempty"`
}

// SQLWorkerResult represents the payload published back to the orchestrator or frontend.
type SQLWorkerResult struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewSQLWorker(deps.DBPool, deps.Queue, deps.Logger, deps.Config)
	})
}

func NewSQLWorker(pool *pgxpool.Pool, nc *nats.Conn, logger *slog.Logger, cfg *config.Config) (*SQLWorker, error) {
	return &SQLWorker{
		pool:   pool,
		db:     database.New(pool),
		nc:     nc,
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (w *SQLWorker) Init(ctx context.Context) error {
	return nil
}

func (w *SQLWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("sql worker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("sql worker: failed to derive inbox", "activity_type", activityType, "error", err)
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

func (w *SQLWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("SQL worker received message", "subject", msg.Subject, "len", len(msg.Data))

	// 1. Poison pill guard
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject, "worker", "SQLWorker")
		msg.Term()
		return nil
	}

	// 2. TAP Envelope Unwrapping
	data := msg.Data
	var env core.Envelope
	convID := ""
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		if env.Performative != core.REQUEST {
			w.logger.Warn("sql worker: dropping message, perf mismatch", "perf_val", env.Performative)
			msg.Term()
			return nil
		}
		data = env.Body
		convID = env.ConversationID
	}

	// 3. Parse payload
	var payload SQLWorkerPayload
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		w.logger.Error("failed to unmarshal sql worker payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.QueryID == "" {
		w.logger.Warn("missing query_id in payload", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// 4. SECURITY: Dispatch with Read-Only transaction enforcement
	w.logger.Info("Executing SQL query via sqlc", "query_id", payload.QueryID)

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		w.logger.Error("failed to begin read-only transaction", "error", err)
		return w.sendResult(msg, SQLWorkerResult{Success: false, Error: "database error"}, convID, payload.ReturnSubject)
	}
	defer tx.Rollback(ctx)

	q := database.New(tx)
	var result SQLWorkerResult

	switch payload.QueryID {
	case "get_bank_accounts":
		res, err := q.GetBankAccounts(ctx)
		result = w.wrapResult(res, err)
	case "get_credit_card_accounts":
		res, err := q.GetCreditCardAccounts(ctx)
		result = w.wrapResult(res, err)
	case "get_checking_accounts":
		res, err := q.GetCheckingAccounts(ctx)
		result = w.wrapResult(res, err)
	default:
		w.logger.Warn("security violation: rejected unknown query_id", "query_id", payload.QueryID)
		result = SQLWorkerResult{Success: false, Error: "unauthorized query intent"}
	}

	// 5. Publish Result
	if err := w.sendResult(msg, result, convID, payload.ReturnSubject); err != nil {
		return err
	}

	msg.Ack()
	return nil
}

func (w *SQLWorker) wrapResult(names []string, err error) SQLWorkerResult {
	if err != nil {
		return SQLWorkerResult{Success: false, Error: err.Error()}
	}

	// Maintain compatibility: frontend expects [ { "name": "..." }, ... ]
	rows := make([]map[string]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, map[string]string{"name": name})
	}

	data, _ := json.Marshal(rows)
	if len(rows) == 0 {
		data = []byte(`[]`)
	}

	return SQLWorkerResult{Success: true, Data: data}
}

func (w *SQLWorker) sendResult(msg *nats.Msg, result SQLWorkerResult, convID string, returnSubject string) error {
	// Only publish completion proof to Orchestrator if it's part of a workflow (no explicit return subject)
	if convID != "" && returnSubject == "" {
		if err := w.publishCompletionProof(result, convID); err != nil {
			w.logger.Error("failed to publish completion proof", "error", err)
			msg.Nak()
			return err
		}
	}

	targetSubject := returnSubject
	if targetSubject == "" {
		targetSubject = msg.Reply
	}

	if targetSubject != "" {
		resBytes, err := json.Marshal(result)
		if err != nil {
			w.logger.Error("failed to marshal sql result", "error", err)
			msg.Term()
			return nil
		}

		if err := w.nc.Publish(targetSubject, resBytes); err != nil {
			w.logger.Error("failed to publish sql result", "error", err, "return_subject", targetSubject)
			msg.Nak()
			return err
		}
		w.logger.Info("Published SQL result to return subject", "target", targetSubject, "res_len", len(resBytes))
	}
	return nil
}


func (w *SQLWorker) publishCompletionProof(result SQLWorkerResult, cid string) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}

	proof := core.Proof{
		TaskID:    uuid.New().String(),
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      resultBytes,
	}

	env, err := core.NewEnvelope(uuid.New().String(), "did:toro:sql-worker", workflows.OrchestratorDID, cid, core.INFORM, proof)
	if err != nil {
		return err
	}

	final, err := json.Marshal(env)
	if err != nil {
		return err
	}

	js, err := w.nc.JetStream()
	if err != nil {
		return err
	}

	_, err = js.Publish(workflows.OrchestratorInbox, final)
	return err
}
