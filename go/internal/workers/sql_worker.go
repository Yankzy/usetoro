package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

// SQLWorkerPayload defines the expected JSON payload for the SQL worker.
type SQLWorkerPayload struct {
	Query         string `json:"query"`
	ReturnSubject string `json:"return_subject,omitempty"`
}

// SQLWorkerResult represents the payload published back to the ReturnSubject.
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

// SQLWorker provides an isolated environment for executing raw SQL queries
// directly against the Postgres database on behalf of the TAP orchestrator
// or CLI tooling. It processes FIPA Envelopes containing SQLWorkerPayload.
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

// Init runs any necessary setup logic before the worker begins processing messages.
// It is called once by the WorkerManager during application startup.
func (w *SQLWorker) Init(ctx context.Context) error {
	return nil
}

// Subscriptions dictates exactly which NATS JetStream subjects this worker will
// listen to. It dynamically derives the inbox subject (e.g. worker.inbox.sql.execute)
// from the activity type configured in defaults.yaml.
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

// Handle is the core event loop invoked per-message by the JetStream FIPA network.
// It enforces the following pipeline:
// 1. Poison pill guard (terminating repeatedly failing messages).
// 2. TAP Envelope unwrapping (handling pure FIPA format).
// 3. Robust JSON unmarshaling using core.UnmarshalTaskPayload.
// 4. Raw SQL Execution.
// 5. Result Publishing (to Orchestrator, CLI return subjects, or both).
// 6. Explicit JetStream Acknowledgment (Ack/Nak/Term).
func (w *SQLWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// 1. Poison pill guard: If a message crashes repeatedly, terminate it.
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
		return nil // Malformed non-retryable message
	}

	if payload.Query == "" {
		w.logger.Warn("missing query in payload", "subject", msg.Subject)
		msg.Term()
		return nil // Invalid request, do not retry
	}

	// 4. Execute Query
	result := w.executeQuery(ctx, payload.Query)

	// 5. Publish Result
	if convID != "" {
		if err := w.publishCompletionProof(payload.Query, result, convID); err != nil {
			w.logger.Error("failed to publish completion proof", "error", err)
			msg.Nak() // Transient error, redeliver
			return err
		}
	}

	if payload.ReturnSubject != "" {
		resBytes, err := json.Marshal(result)
		if err != nil {
			w.logger.Error("failed to marshal sql result", "error", err)
			msg.Term() // Marshal error is likely not transient
			return nil
		}

		if err := w.nc.Publish(payload.ReturnSubject, resBytes); err != nil {
			w.logger.Error("failed to publish sql result", "error", err, "return_subject", payload.ReturnSubject)
			msg.Nak() // Transient publish error, redeliver
			return err
		}
	}

	msg.Ack() // We are completely done, explicitly ack the JetStream message
	return nil
}

// executeQuery performs a raw SQL read or write against the database pool,
// mapping the variable number of columns dynamically into a generic []map[string]any
// representation suitable for downstream JSON serialization.
func (w *SQLWorker) executeQuery(ctx context.Context, query string) SQLWorkerResult {
	var result SQLWorkerResult
	rows, err := w.pool.Query(ctx, query)
	if err != nil {
		w.logger.Error("sql query execution failed", "error", err, "query", query)
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
			// Handle byte slices (often used for strings in some drivers/scenarios)
			if b, ok := val.([]byte); ok {
				m[colName] = string(b)
			} else {
				m[colName] = val
			}
		}
		allRows = append(allRows, m)
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

// publishCompletionProof wraps the SQLWorkerResult in a FIPA Fignode "Proof"
// envelope and publishes it back to the OrchestratorInbox, preserving the
// original conversation ID (cid) so the workflow state machine can advance.
func (w *SQLWorker) publishCompletionProof(query string, result SQLWorkerResult, cid string) error {
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
