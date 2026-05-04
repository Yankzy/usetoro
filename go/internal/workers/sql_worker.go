package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

// allowedQueries acts as the strict gatekeeper.
// Every single query the frontend can execute MUST be defined here.
// Always use parameterized inputs ($1, $2) and strictly define SELECT columns (no *).
var allowedQueries = map[string]string{
	"get_bank_accounts":        "SELECT name FROM shadow_erp.accounts WHERE active = true AND account_type IN ('Bank', 'Credit Card');",
	"get_credit_card_accounts": "SELECT name FROM shadow_erp.accounts WHERE active = true AND account_type IN ('Credit Card');",
	"get_checking_accounts":    "SELECT name FROM shadow_erp.accounts WHERE active = true AND account_type IN ('Bank');",
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

// SQLWorker provides an isolated environment for executing pre-approved SQL queries.
type SQLWorker struct {
	pool   *pgxpool.Pool
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
}

func NewSQLWorker(pool *pgxpool.Pool, nc *nats.Conn, logger *slog.Logger, cfg *config.Config) (*SQLWorker, error) {
	return &SQLWorker{
		pool:   pool,
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

	// 4. SECURITY: Query Allowlist Lookup
	sqlQuery, exists := allowedQueries[payload.QueryID]
	if !exists {
		w.logger.Warn("security violation: rejected unknown query_id", "query_id", payload.QueryID, "subject", msg.Subject)

		result := SQLWorkerResult{Success: false, Error: "unauthorized query intent"}
		w.sendResult(msg, result, convID, payload.ReturnSubject)

		msg.Term() // Malicious or outdated client, do not retry
		return nil
	}

	// 5. Execute pre-approved query with arguments
	result := w.executeQuery(ctx, sqlQuery, payload.Args)

	// 6. Publish Result
	if err := w.sendResult(msg, result, convID, payload.ReturnSubject); err != nil {
		return err // sendResult handles Nak
	}

	msg.Ack()
	return nil
}

func (w *SQLWorker) sendResult(msg *nats.Msg, result SQLWorkerResult, convID string, returnSubject string) error {
	if convID != "" {
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
	}
	return nil
}

func (w *SQLWorker) executeQuery(ctx context.Context, query string, args []any) SQLWorkerResult {
	var result SQLWorkerResult

	// SECURITY: Defense-in-depth to guarantee read-only behavior regardless of the allowlist
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		w.logger.Error("failed to begin read-only transaction", "error", err)
		result.Success = false
		result.Error = fmt.Sprintf("database error: %v", err)
		return result
	}
	defer tx.Rollback(ctx)

	// Pass args safely to the Postgres driver
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		w.logger.Error("sql query execution failed", "error", err)
		result.Success = false
		result.Error = err.Error()
		return result
	}
	defer rows.Close()

	fieldDescriptions := rows.FieldDescriptions()
	cols := make([]string, len(fieldDescriptions))
	for i, fd := range fieldDescriptions {
		cols[i] = string(fd.Name)
	}

	var allRows []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			w.logger.Error("failed to get row values", "error", err)
			continue
		}

		m := make(map[string]any)
		for i, colName := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				m[colName] = string(b)
			} else {
				m[colName] = val
			}
		}
		allRows = append(allRows, m)
	}

	if err := tx.Commit(ctx); err != nil {
		w.logger.Warn("failed to commit read-only transaction", "error", err)
	}

	result.Success = true
	if len(allRows) > 0 {
		b, _ := json.Marshal(allRows)
		result.Data = b
	} else {
		result.Data = []byte(`[]`)
	}

	return result
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
