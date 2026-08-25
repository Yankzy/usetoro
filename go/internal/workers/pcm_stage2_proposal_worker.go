package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	accountingservice "github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

// PCMStage2ProposalWorker is the final action in the PCM DAG. It persists an
// immutable proposal revision; it never posts or reconciles anything.
type PCMStage2ProposalWorker struct {
	db     *database.Queries
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.Store == nil || deps.DBPool == nil {
			return nil, nil
		}
		return &PCMStage2ProposalWorker{db: deps.Store.Queries, pool: deps.DBPool, logger: deps.Logger.With("worker", "pcm_stage2_proposal")}, nil
	})
}

func (w *PCMStage2ProposalWorker) Init(context.Context) error { return nil }
func (w *PCMStage2ProposalWorker) Stop()                      {}
func (w *PCMStage2ProposalWorker) Subscriptions() []SubscriptionConfig {
	return makeActionSubscription("pcm_stage2_proposal")
}

func (w *PCMStage2ProposalWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	req, err := parseActionRequest(msg)
	if err != nil {
		return err
	}
	output, err := w.buildOutput(ctx, req)
	if err != nil {
		return err
	}
	hash, err := output.Hash()
	if err != nil {
		return fmt.Errorf("validate stage-2 output: %w", err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	stagingID, err := scanUUID(req.NodeID)
	if err != nil {
		return err
	}
	reviewStatus := "NOT_REVIEWED"
	if output.Outcome == accountingservice.Stage2ProposedTreatment || output.Outcome == accountingservice.Stage2HumanReview {
		reviewStatus = "PENDING"
	}
	if _, err := w.db.PersistStage2Proposal(ctx, database.PersistStage2ProposalParams{
		ProposalHash: hash, Proposal: encoded, Outcome: output.Outcome,
		ReviewStatus: reviewStatus, StagingTransactionID: stagingID,
	}); err != nil {
		return fmt.Errorf("persist stage-2 proposal: %w", err)
	}

	return respondJSON(msg, ActionResponse{
		Property:   "stage2_outcome",
		Candidates: []ase.ProbabilityCandidate{{Value: output.Outcome, Confidence: 1, Reasoning: output.ReviewReason}},
		PayloadUpdates: map[string]interface{}{
			"stage2_output": output,
			"stage2_hash":   hash,
		},
	}, w.logger)
}

func (w *PCMStage2ProposalWorker) buildOutput(ctx context.Context, req ActionRequest) (accountingservice.Stage2Output, error) {
	intent := topStage2Intent(req.Candidates)
	lineID := stringValue(req.Payload, "bank_statement_line_id")
	stagingID := stringValue(req.Payload, "staging_transaction_id")
	if stagingID == "" {
		stagingID = req.NodeID
	}
	bankAccountID := stringValue(req.Payload, "bank_account_id")
	bankCode := stringValue(req.Payload, "account_code")
	operationDate := stringValue(req.Payload, "operation_date")
	direction := stringValue(req.Payload, "cash_direction")
	amount := stringValue(req.Payload, "raw_amount")
	currency := stringValue(req.Payload, "currency")
	description := stringValue(req.Payload, "description")
	if description == "" {
		description = stringValue(req.Payload, "raw_description")
	}

	output := accountingservice.Stage2Output{
		SchemaVersion: accountingservice.Stage2SchemaVersion,
		Context: accountingservice.Stage2WorkflowContext{
			EntityID: stringValue(req.Payload, "entity_id"), RealmID: req.RealmID,
			WorkflowID: stringValue(req.Payload, "workflow_id"), WorkflowTraceID: stringValue(req.Payload, "workflow_trace_id"),
			SessionID: stringValue(req.Payload, "session_id"), SourceDocumentIDs: stringSliceValue(req.Payload["source_document_ids"]),
			StatementPeriodKey:      stringValue(req.Payload, "statement_period_key"),
			StatementOpeningBalance: stringValue(req.Payload, "statement_opening_balance"),
			StatementClosingBalance: stringValue(req.Payload, "statement_closing_balance"),
		},
		BankLine: accountingservice.Stage2BankLine{
			StagingTransactionID: stagingID, BankStatementLineID: lineID,
			BankAccountID: bankAccountID, BankLedgerAccountCode: bankCode,
			OperationDate: operationDate, Direction: direction, Amount: amount,
			Currency: currency, Description: description,
		},
		Classification: accountingservice.Stage2Classification{Intent: intent.Value, Confidence: intent.Confidence, Reasoning: intent.Reasoning},
	}

	if strings.HasPrefix(intent.Value, "HOLD_") {
		output.Outcome = accountingservice.Stage2HumanReview
		output.ReviewReason = "classification requires human evidence: " + intent.Value
		return output, nil
	}
	if lineID == "" || operationDate == "" || amount == "" || currency == "" {
		output.Outcome = accountingservice.Stage2HoldUnreliable
		output.ReviewReason = "canonical bank line, operation date, exact amount, and currency are required"
		return output, nil
	}

	bankAccount, err := w.resolveAccountByCode(ctx, req.RealmID, bankCode)
	if err != nil {
		output.Outcome = accountingservice.Stage2HoldBankConfig
		output.ReviewReason = fmt.Sprintf("bank ledger account %q is missing or ambiguous", bankCode)
		return output, nil
	}
	counterpart, err := w.resolveCounterpartAccount(ctx, req, intent.Value)
	if err != nil {
		output.Outcome = accountingservice.Stage2HoldAccountConfig
		output.ReviewReason = err.Error()
		return output, nil
	}

	normalizedAmount, err := accountingservice.NewReconciliationMoney(amount)
	if err != nil {
		output.Outcome = accountingservice.Stage2HoldUnreliable
		output.ReviewReason = err.Error()
		return output, nil
	}
	label := strings.TrimSpace(description)
	if label == "" {
		label = intent.Value
	}
	debitCounterpart, creditCounterpart := normalizedAmount.String(), "0.0000"
	debitBank, creditBank := "0.0000", normalizedAmount.String()
	if direction == "INFLOW" {
		debitCounterpart, creditCounterpart = "0.0000", normalizedAmount.String()
		debitBank, creditBank = normalizedAmount.String(), "0.0000"
	}
	output.Outcome = accountingservice.Stage2ProposedTreatment
	output.Treatment = &accountingservice.ProposedAccountingTreatment{
		EntryDate: operationDate, Currency: currency, Label: label,
		Lines: []accountingservice.ProposedJournalLine{
			{LineIndex: 0, AccountID: counterpart.id, AccountCode: counterpart.code, Label: label, Debit: debitCounterpart, Credit: creditCounterpart},
			{LineIndex: 1, AccountID: bankAccount.id, AccountCode: bankAccount.code, Label: label, Debit: debitBank, Credit: creditBank},
		},
	}
	return output, nil
}

type resolvedStage2Account struct{ id, code string }

func (w *PCMStage2ProposalWorker) resolveAccountByCode(ctx context.Context, realmID, code string) (resolvedStage2Account, error) {
	if realmID == "" || code == "" {
		return resolvedStage2Account{}, pgx.ErrNoRows
	}
	var result resolvedStage2Account
	err := w.pool.QueryRow(ctx, `SELECT id::text, account_code FROM shadow_erp.accounts
		WHERE realm_id = $1 AND account_code = $2 AND active = TRUE AND deleted_at IS NULL`, realmID, code).Scan(&result.id, &result.code)
	return result, err
}

func (w *PCMStage2ProposalWorker) resolveCounterpartAccount(ctx context.Context, req ActionRequest, intent string) (resolvedStage2Account, error) {
	var result resolvedStage2Account
	err := w.pool.QueryRow(ctx, `SELECT a.id::text, a.account_code
		FROM fignode.staging_transactions t
		JOIN shadow_erp.accounts a ON a.id = COALESCE(t.override_account_id, t.predicted_account_id)
		WHERE t.id = $1 AND a.realm_id = $2 AND a.active = TRUE AND a.deleted_at IS NULL`, req.NodeID, req.RealmID).Scan(&result.id, &result.code)
	if err == nil && result.code != "" {
		return result, nil
	}
	err = w.pool.QueryRow(ctx, `SELECT a.id::text, a.account_code
		FROM shadow_erp.stage2_treatment_account_mappings m
		JOIN shadow_erp.accounts a ON a.id = m.account_id
		WHERE m.realm_id = $1 AND m.intent = $2 AND a.active = TRUE AND a.deleted_at IS NULL`, req.RealmID, intent).Scan(&result.id, &result.code)
	if err != nil {
		return resolvedStage2Account{}, fmt.Errorf("classification %s has no unique active realm-owned account mapping", intent)
	}
	return result, nil
}

func topStage2Intent(candidates map[string][]ase.ProbabilityCandidate) ase.ProbabilityCandidate {
	for _, key := range []string{"bank_transaction_intent", "macro_classifier", "macro_class"} {
		if values := candidates[key]; len(values) > 0 {
			return values[0]
		}
	}
	return ase.ProbabilityCandidate{Value: "HOLD_UNCLASSIFIED", Confidence: 1, Reasoning: "no bank transaction intent was supplied"}
}

func stringValue(payload map[string]interface{}, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceValue(value interface{}) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func scanUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", value, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
