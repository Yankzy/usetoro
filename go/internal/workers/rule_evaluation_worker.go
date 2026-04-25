package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
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

type RuleEvaluationStore interface {
	GetPendingStagingTransactions(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	UpdateStagingTransactionWithRule(context.Context, database.UpdateStagingTransactionWithRuleParams) error
}

type RuleEvaluationEngine interface {
	EvaluateTransaction(context.Context, ruleEngine.Transaction) (*accounting.RuleResult, error)
	PersistAuditLog(context.Context, string, pgtype.UUID, *accounting.RuleResult)
}

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

	// 2. Fetch pending transactions
	txns, err := w.store.GetPendingStagingTransactions(ctx, sessionUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch pending staging txns: %w", err)
	}

	matchesFound := 0

	// 3. Process through Rule Engine
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

		// 4. Update Database on Match
		if result != nil && result.MatchedRuleGroupID != nil {
			matchesFound++

			// By default, assume 1-to-1 allocation target for the primary account
			var predictedAccountID pgtype.UUID
			if len(result.Allocations) > 0 {
				predictedAccountID = result.Allocations[0].AccountID
			}

			// Assign Vendor vs Customer based on cash direction
			var vendorID, customerID pgtype.UUID
			if ruleTx.Direction == ruleEngine.Outflow {
				vendorID = result.TargetEntityID
			} else {
				customerID = result.TargetEntityID
			}

			// Determine if it needs human review or can bypass directly to approved
			newStatus := "READY_FOR_REVIEW"
			if !result.RequiresReview {
				newStatus = "SWIPED_APPROVED"
			}

			// Instantiate the pgtype.Int4 struct directly instead of using Scan()
			ruleID := pgtype.Int4{
				Int32: *result.MatchedRuleGroupID,
				Valid: true,
			}

			updateParams := database.UpdateStagingTransactionWithRuleParams{
				ID:                  stx.ID,
				RuleGroupID:         ruleID,
				PredictedAccountID:  predictedAccountID,
				PredictedVendorID:   vendorID,
				PredictedCustomerID: customerID,
				Status:              newStatus,
				AiReasoning:         pgtype.Text{String: "Matched CPA Rule: " + result.Explanation.HumanReadableReason(), Valid: true},
			}

			if err := w.store.UpdateStagingTransactionWithRule(ctx, updateParams); err != nil {
				w.logger.Error("failed to update matched staging txn", "txn_id", stx.ID, "error", err)
			}

			// Persist the audit log for compliance
			w.ruleEngine.PersistAuditLog(ctx, stx.RealmID.String, stx.ID, result)
		}
	}

	w.logger.Info("completed rule evaluation", "session_id", payload.SessionID, "processed", len(txns), "matches", matchesFound)

	// 5. Completion signaling (Advancing Orchestrator Flow)
	// We return an INFORM back to the orchestrator to trigger the AI Categorization worker for the remaining unmatched rows
	if env.SenderDID != "" && env.ConversationID != "" && w.queue != nil {
		replyEnv, envErr := core.NewEnvelope(
			uuid.New().String(),
			"worker.rule_evaluation",
			"workflows.OrchestratorInbox",
			env.ConversationID,
			core.INFORM,
			env.Body,
		)
		if envErr != nil {
			w.logger.Warn("failed to build completion envelope", "error", envErr, "cid", env.ConversationID)
		} else {
			replyBytes, err := json.Marshal(replyEnv)
			if err != nil {
				w.logger.Warn("failed to serialize completion envelope", "error", err, "cid", env.ConversationID)
			} else if err := w.queue.Publish(replyEnv.ReceiverDID, replyBytes); err != nil {
				w.logger.Warn("failed to publish completion inform", "error", err, "cid", env.ConversationID, "subject", replyEnv.ReceiverDID)
			} else {
				w.logger.Info("signaling workflow completion", "cid", env.ConversationID, "subject", replyEnv.ReceiverDID)
			}
		}
	}

	return nil
}

func (w *RuleEvaluationWorker) mapStagingToRuleTransaction(stx database.FignodeStagingTransaction) (ruleEngine.Transaction, error) {
	// Parse Amount and Direction
	amtStr := strings.ReplaceAll(stx.RawAmount, ",", "")
	amt, err := strconv.ParseFloat(amtStr, 64)
	if err != nil {
		return ruleEngine.Transaction{}, err
	}

	direction := ruleEngine.Outflow
	if amt > 0 {
		direction = ruleEngine.Inflow
	} else {
		amt = -amt // Rule engine uses absolute amounts
	}

	// Prefer Plaid's cleaned merchant name if available, fallback to raw description
	vendorName := stx.RawDescription.String
	if stx.MerchantName.Valid && stx.MerchantName.String != "" {
		vendorName = stx.MerchantName.String
	}

	return ruleEngine.Transaction{
		ID:          fmt.Sprintf("%v", stx.ID.Bytes),
		EntityID:    stx.RealmID.String,
		Amount:      amt,
		Direction:   direction,
		Date:        stx.RawDate.Time,
		Description: stx.RawDescription.String,
		Vendor:      vendorName,
		Customer:    vendorName, // Let the rule condition dictate which field it checks
		Category:    stx.PlaidCategory.String,
		// TODO: PASS THE BANK ACCOUNT NAME, NOT THE ACCOUNT ID
		SourceAccount: stx.BankAccountID.String, // 🚨 NEW: Pass the bank account name to the engine
	}, nil
}
