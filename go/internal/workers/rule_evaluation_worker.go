package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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
	GetAccountByID(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorByID(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerByID(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
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
	if realmID == "" {
		w.logger.Info("No realm_id for session, skipping rule evaluation", "session_id", payload.SessionID)
		return w.sendCompletion(env, payload.SessionID, 0, 0)
	}

	// 3. Fetch pending transactions
	txns, err := w.store.GetPendingStagingTransactions(ctx, sessionUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch pending staging txns: %w", err)
	}

	matchesFound := 0
	ruleGroupMatches := make(map[int32]int)
	ruleGroupNames := make(map[int32]string)

	// 4. Process through Rule Engine
	for _, stx := range txns {
		ruleTx, err := w.mapStagingToRuleTransaction(stx, realmID)
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
			ruleGroupID := *result.MatchedRuleGroupID
			ruleGroupMatches[ruleGroupID]++
			ruleGroupNames[ruleGroupID] = result.Explanation.GroupName

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
				newStatus = "CLASSIFIED"
			}

			ruleID := pgtype.Int4{Int32: *result.MatchedRuleGroupID, Valid: true}

			// Resolve names from matched entity/account UUIDs.
			var accountName, vendorName, customerName, merchantName pgtype.Text
			if predictedAccountID.Valid {
				if acct, err := w.store.GetAccountByID(ctx, predictedAccountID); err == nil {
					accountName = pgtype.Text{String: acct.Name, Valid: true}
				}
			}
			if vendorID.Valid {
				if vendor, err := w.store.GetVendorByID(ctx, vendorID); err == nil {
					vendorName = pgtype.Text{String: vendor.DisplayName, Valid: true}
					merchantName = vendorName
				}
			}
			if customerID.Valid {
				if cust, err := w.store.GetCustomerByID(ctx, customerID); err == nil {
					customerName = pgtype.Text{String: cust.DisplayName, Valid: true}
					merchantName = customerName
				}
			}

			// Derive confidence from match quality.
			condCount := len(result.Explanation.ConditionExp)
			confidence := 0.75
			if condCount >= 3 {
				confidence = 0.95
			} else if condCount == 2 {
				confidence = 0.85
			}
			var confScore pgtype.Numeric
			_ = confScore.Scan(fmt.Sprintf("%.2f", confidence))

			// Determine cash_direction from the transaction.
			cashDir := "OUTFLOW"
			if ruleTx.Direction == ruleEngine.Inflow {
				cashDir = "INFLOW"
			}

			// Build cumulative reasoning.
			ruleReasoning := "[Rule Engine]: Matched CPA Rule: " + result.Explanation.HumanReadableReason()
			priorReasoning := ""
			if stx.AiReasoning.Valid && stx.AiReasoning.String != "" {
				priorReasoning = stx.AiReasoning.String
			}
			fullReasoning := ruleReasoning
			if priorReasoning != "" {
				fullReasoning = priorReasoning + "\n" + ruleReasoning
			}

			if err := w.store.UpdateStagingTransactionWithRule(ctx, database.UpdateStagingTransactionWithRuleParams{
				ID:                    stx.ID,
				RuleGroupID:           ruleID,
				PredictedAccountID:    predictedAccountID,
				PredictedVendorID:     vendorID,
				PredictedCustomerID:   customerID,
				Status:                pgtype.Text{String: newStatus, Valid: true},
				AiReasoning:           pgtype.Text{String: fullReasoning, Valid: true},
				CashDirection:         pgtype.Text{String: cashDir, Valid: true},
				PredictedAccountName:  accountName,
				PredictedVendorName:   vendorName,
				PredictedCustomerName: customerName,
				ConfidenceScore:       confScore,
				MerchantName:          merchantName,
			}); err != nil {
				w.logger.Error("failed to update matched staging txn", "txn_id", stx.ID, "error", err)
			}

			w.ruleEngine.PersistAuditLog(ctx, realmID, stx.ID, result)
		}
	}

	// Log counts per matched rule (sorted for determinism)
	var matchedRuleIDs []int
	for id := range ruleGroupMatches {
		matchedRuleIDs = append(matchedRuleIDs, int(id))
	}
	sort.Ints(matchedRuleIDs)

	for _, id := range matchedRuleIDs {
		ruleGroupID := int32(id)
		w.logger.Info("rule matched transactions",
			"session_id", payload.SessionID,
			"rule_group_id", ruleGroupID,
			"rule_name", ruleGroupNames[ruleGroupID],
			"matches", ruleGroupMatches[ruleGroupID],
		)
	}

	w.logger.Info("completed rule evaluation",
		"session_id", payload.SessionID,
		"processed", len(txns),
		"matches", matchesFound,
	)

	// 5. Signal completion with session metadata only.
	return w.sendCompletion(env, payload.SessionID, len(txns), matchesFound)
}

// sendCompletion publishes a completion proof to the orchestrator inbox.
func (w *RuleEvaluationWorker) sendCompletion(env core.Envelope, sessionID string, processed, matches int) error {
	if env.ConversationID == "" || w.queue == nil {
		return nil
	}

	proofData := map[string]interface{}{
		"session_id": sessionID,
		"processed":  processed,
		"matches":    matches,
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

	js, err := w.queue.JetStream()
	if err != nil {
		w.logger.Warn("failed to get JetStream context", "error", err, "cid", env.ConversationID)
		return nil
	}
	if _, err := js.Publish(workflows.OrchestratorInbox, replyBytes); err != nil {
		w.logger.Warn("failed to publish completion inform", "error", err, "cid", env.ConversationID)
	} else {
		w.logger.Info("signaling rule engine completion", "cid", env.ConversationID)
	}

	return nil
}

func (w *RuleEvaluationWorker) mapStagingToRuleTransaction(stx database.FignodeStagingTransaction, realmID string) (ruleEngine.Transaction, error) {
	amtStr := strings.ReplaceAll(stx.RawAmount, ",", "")
	amtStr = strings.ReplaceAll(amtStr, "$", "")
	amtStr = strings.TrimSpace(amtStr)
	amt, err := strconv.ParseFloat(amtStr, 64)
	if err != nil {
		return ruleEngine.Transaction{}, err
	}

	var direction ruleEngine.CashDirection
	if stx.CashDirection.Valid && stx.CashDirection.String == "INFLOW" {
		direction = ruleEngine.Inflow
	} else {
		direction = ruleEngine.Outflow
	}

	// Ensure amount is absolute for engine evaluation (thresholds, matching, etc.)
	if amt < 0 {
		amt = -amt
	}

	vendorName := stx.RawDescription.String
	if stx.MerchantName.Valid && stx.MerchantName.String != "" {
		vendorName = stx.MerchantName.String
	}

	return ruleEngine.Transaction{
		ID:          fmt.Sprintf("%v", stx.ID.Bytes),
		EntityID:    realmID,
		Amount:      amt,
		Direction:   direction,
		Date:        stx.ParsedDate.Time,
		Description: stx.RawDescription.String,
		Vendor:      vendorName,
		Customer:    vendorName,
		Category:    stx.Category.String,
	}, nil
}
