package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/jackc/pgx/v5/pgtype"
)

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewRuleEvaluationWorker(
			deps.Store.Queries,
			deps.Logger,
			deps.Config,
			deps.Queue,
			deps.RuleEngine,
		), nil
	})
}

// RuleEvaluationStore is the minimal database interface required by this worker.
type RuleEvaluationStore interface {
	GetPendingStagingTransactions(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	UpdateStagingTransactionWithRule(context.Context, database.UpdateStagingTransactionWithRuleParams) error
	GetCleanupSession(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	// GetSessionRows with Status='ENRICHED' fetches rows that completed enrichment
	// but were not matched by the rule engine — these need AI categorization.
	GetSessionRows(context.Context, database.GetSessionRowsParams) ([]database.GetSessionRowsRow, error)
}

// RuleEvaluationEngine is the minimal rule-engine interface required by this worker.
type RuleEvaluationEngine interface {
	EvaluateTransaction(context.Context, ruleEngine.Transaction) (*accounting.RuleResult, error)
	PersistAuditLog(context.Context, string, pgtype.UUID, *accounting.RuleResult)
}

// ─── RuleEvaluationWorker ────────────────────────────────────────────────────

type RuleEvaluationWorker struct {
	store      RuleEvaluationStore
	logger     *slog.Logger
	cfg        *config.Config
	queue      *nats.Conn
	ruleEngine RuleEvaluationEngine
}

func NewRuleEvaluationWorker(store RuleEvaluationStore, logger *slog.Logger, cfg *config.Config, queue *nats.Conn, ruleEngine RuleEvaluationEngine) *RuleEvaluationWorker {
	return &RuleEvaluationWorker{
		store:      store,
		logger:     logger,
		cfg:        cfg,
		queue:      queue,
		ruleEngine: ruleEngine,
	}
}

func (w *RuleEvaluationWorker) Init(ctx context.Context) error {
	return nil
}

func (w *RuleEvaluationWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject, _ = core.BuildWorkerInboxFromActivity("workers.rule_evaluation")
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

func (w *RuleEvaluationWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// 1. Extract SessionID and Envelope data
	var env core.Envelope
	var payload struct {
		SessionID string `json:"session_id"`
	}

	data := msg.Data
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	if err := core.UnmarshalTaskPayload(data, &payload); err != nil || payload.SessionID == "" {
		w.logger.Warn("could not extract session_id, ignoring message", "subject", msg.Subject)
		return nil
	}

	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(payload.SessionID); err != nil {
		w.logger.Warn("invalid session_id UUID format", "session_id", payload.SessionID)
		return nil
	}

	w.logger.Info("evaluating rules for session", "session_id", payload.SessionID)

	// 2. Fetch the session to resolve realm_id.
	session, err := w.store.GetCleanupSession(ctx, sessionUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch session for rule evaluation: %w", err)
	}
	realmID := session.RealmID.String

	// 3. Fetch pending transactions
	txns, err := w.store.GetPendingStagingTransactions(ctx, sessionUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch pending staging txns: %w", err)
	}

	matchesFound := 0

	// 4. Process through Rule Engine
	for _, stx := range txns {
		ruleTx, err := w.mapStagingToRuleTransaction(stx)
		if err != nil {
			w.logger.Warn("failed to map staging txn", "txn_id", stx.ID, "error", err)
			continue
		}

		result, err := w.ruleEngine.EvaluateTransaction(ctx, ruleTx)
		if err != nil {
			w.logger.Warn("rule evaluation error", "txn_id", stx.ID, "error", err)
			continue
		}

		if result != nil && result.MatchedRuleGroupID != nil {
			matchesFound++

			var predictedAccountID pgtype.UUID
			if len(result.Allocations) > 0 {
				predictedAccountID = result.Allocations[0].AccountID
			}

			var vendorID, customerID pgtype.UUID
			if ruleTx.Direction == ruleEngine.Outflow {
				vendorID = result.TargetEntityID
			} else {
				customerID = result.TargetEntityID
			}

			newStatus := "READY_FOR_REVIEW"
			if !result.RequiresReview {
				newStatus = "SWIPED_APPROVED"
			}

			ruleID := pgtype.Int4{Int32: *result.MatchedRuleGroupID, Valid: true}

			if err := w.store.UpdateStagingTransactionWithRule(ctx, database.UpdateStagingTransactionWithRuleParams{
				ID:                  stx.ID,
				RuleGroupID:         ruleID,
				PredictedAccountID:  predictedAccountID,
				PredictedVendorID:   vendorID,
				PredictedCustomerID: customerID,
				Status:              newStatus,
				AiReasoning:         pgtype.Text{String: "Matched CPA Rule: " + result.Explanation.HumanReadableReason(), Valid: true},
			}); err != nil {
				w.logger.Error("failed to update matched staging txn", "txn_id", stx.ID, "error", err)
			}

			w.ruleEngine.PersistAuditLog(ctx, realmID, stx.ID, result)
		}
	}

	w.logger.Info("completed rule evaluation",
		"session_id", payload.SessionID,
		"processed", len(txns),
		"matches", matchesFound,
	)

	// 5. Signal completion.
	//    Fetch the rows that still need AI categorization and include them in the
	//    proof body so the Orchestrator can thread them to the next step generically.
	//    The Orchestrator does not interpret these fields; it just passes the proof
	//    body through to whoever comes next.
	if env.SenderDID == "" || env.ConversationID == "" || w.queue == nil {
		return nil
	}

	proofData := map[string]interface{}{
		"session_id": payload.SessionID,
	}

	unclassifiedRows, err := w.store.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: sessionUUID,
		Status:    pgtype.Text{String: "ENRICHED", Valid: true},
	})
	if err != nil {
		w.logger.Warn("could not fetch unclassified rows for proof body, continuing without them", "error", err)
	} else {
		rows := make([]interface{}, 0, len(unclassifiedRows))
		for _, r := range unclassifiedRows {
			rowBytes, _ := json.Marshal(r)
			var rowMap map[string]interface{}
			if json.Unmarshal(rowBytes, &rowMap) == nil {
				rows = append(rows, rowMap)
			}
		}
		proofData["rows"] = rows
		w.logger.Info("rule evaluation: attaching unclassified rows to proof", "count", len(rows))
	}

	proofDataBytes, _ := json.Marshal(proofData)
	proof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      json.RawMessage(proofDataBytes),
	}

	replyEnv, envErr := core.NewEnvelope(
		uuid.New().String(),
		"worker.rule_evaluation",
		workflows.OrchestratorInbox,
		env.ConversationID,
		core.INFORM,
		proof,
	)
	if envErr != nil {
		w.logger.Warn("failed to build completion envelope", "error", envErr, "cid", env.ConversationID)
		return nil
	}

	replyBytes, err := json.Marshal(replyEnv)
	if err != nil {
		w.logger.Warn("failed to serialize completion envelope", "error", err, "cid", env.ConversationID)
		return nil
	}

	if err := w.queue.Publish(replyEnv.ReceiverDID, replyBytes); err != nil {
		w.logger.Warn("failed to publish completion inform", "error", err, "cid", env.ConversationID)
	} else {
		w.logger.Info("signaling rule engine completion", "cid", env.ConversationID, "subject", replyEnv.ReceiverDID)
	}

	return nil
}

func (w *RuleEvaluationWorker) mapStagingToRuleTransaction(stx database.FignodeStagingTransaction) (ruleEngine.Transaction, error) {
	amtStr := strings.ReplaceAll(stx.RawAmount, ",", "")
	amt, err := strconv.ParseFloat(amtStr, 64)
	if err != nil {
		return ruleEngine.Transaction{}, err
	}

	direction := ruleEngine.Outflow
	if amt > 0 {
		direction = ruleEngine.Inflow
	} else {
		amt = -amt
	}

	vendorName := stx.RawDescription.String
	if stx.MerchantName.Valid && stx.MerchantName.String != "" {
		vendorName = stx.MerchantName.String
	}

	return ruleEngine.Transaction{
		ID:          fmt.Sprintf("%v", stx.ID.Bytes),
		Amount:      amt,
		Direction:   direction,
		Date:        stx.RawDate.Time,
		Description: stx.RawDescription.String,
		Vendor:      vendorName,
		Customer:    vendorName,
		Category:    stx.Category.String,
	}, nil
}
