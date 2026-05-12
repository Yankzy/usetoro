package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// TransactionRepository defines the data access methods needed for idempotency and audit logging
type TransactionRepository interface {
	GetOrCreateSystemSession(ctx context.Context, realmID pgtype.Text) (pgtype.UUID, error)
	GetProposedTransactionByValues(ctx context.Context, arg database.GetProposedTransactionByValuesParams) (database.FignodeStagingTransaction, error)
	CreateProposedTransaction(ctx context.Context, arg database.CreateProposedTransactionParams) (database.FignodeStagingTransaction, error)
	UpdateProposedTransactionSyncStatus(ctx context.Context, arg database.UpdateProposedTransactionSyncStatusParams) error
	GetVendorByERPID(ctx context.Context, arg database.GetVendorByERPIDParams) (database.ShadowErpVendor, error)
	GetAccountByERPID(ctx context.Context, arg database.GetAccountByERPIDParams) (database.ShadowErpAccount, error)
	GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error)
	GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error)
	GetUnifiedTransactions(ctx context.Context, realmID string) ([]database.GetUnifiedTransactionsRow, error)
}

// It uses EntityResolver and CoAMapper to resolve vendor and account
// references before delegating to the ERP Factory.
type TransactionService struct {
	logger         *slog.Logger
	repo           TransactionRepository
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	factory        erp.ProviderFactory
	ruleEngine     *RuleEngineService
	nc             *nats.Conn // Used to publish async events
	eventSubject   string     // e.g. toro.erp.events.*
}

// NewTransactionService creates a new TransactionService.
// entityResolver and coaMapper may be nil when AI infrastructure is unavailable;
// in that case, AccountHint and VendorHint on ExpenseInput become required.
func NewTransactionService(
	logger *slog.Logger,
	repo TransactionRepository,
	resolver *ai.EntityResolver,
	coa *ai.CoAMapper,
	factory erp.ProviderFactory,
	ruleEngine *RuleEngineService,
	nc *nats.Conn,
	eventSubject string,
) *TransactionService {
	return &TransactionService{
		logger:         logger,
		repo:           repo,
		entityResolver: resolver,
		coaMapper:      coa,
		factory:        factory,
		ruleEngine:     ruleEngine,
		nc:             nc,
		eventSubject:   eventSubject,
	}
}

// PostExpense resolves the vendor and account via AI, then creates either a
// Purchase (paid=true) or Bill (paid=false) in the tenant's ERP.
func (s *TransactionService) PostExpense(ctx context.Context, input erp.ExpenseInput) (*erp.PostedExpense, error) {
	return s.postExpense(ctx, input, nil)
}

// postExpense is the internal implementation, accepting optional mock overrides for
// ERP Provider (nil means use the real Provider from the factory).
func (s *TransactionService) postExpense(
	ctx context.Context,
	input erp.ExpenseInput,
	providerOverride *erp.Provider,
) (*erp.PostedExpense, error) {
	if input.RealmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	txnDate := input.TxnDate
	if txnDate.IsZero() {
		txnDate = time.Now()
	}

	amountStr := fmt.Sprintf("%.2f", input.Amount)

	sourceType := "Bill"
	if input.Paid {
		sourceType = "Purchase"
	}

	if s.repo != nil {
		var realmID pgtype.Text
		realmID.Scan(input.RealmID)

		// Basic Idempotency check with local DB
		existing, err := s.repo.GetProposedTransactionByValues(ctx, database.GetProposedTransactionByValuesParams{
			RealmID:   realmID,
			RawDate:   pgtype.Date{Time: txnDate, Valid: true},
			RawAmount: amountStr,
		})

		if err == nil && (existing.Status == "SYNCED" || existing.Status == "PENDING_CLASSIFICATION") {
			s.logger.Info("⏭️ Idempotency check passed: Transaction already syncing/synced")
			return &erp.PostedExpense{
				ERPEntityID: existing.ErpTransactionID.String,
				EntityType:  existing.SourceType,
			}, nil
		}

		// Resolve the per-realm SYSTEM session that owns non-CSV staged rows.
		systemSessionID, err := s.repo.GetOrCreateSystemSession(ctx, realmID)
		if err != nil {
			s.logger.Warn("Failed to resolve system session for realm", "error", err, "realm_id", input.RealmID)
			return nil, err
		}

		proposed, err := s.repo.CreateProposedTransaction(ctx, database.CreateProposedTransactionParams{
			SessionID:       systemSessionID,
			SourceType:      sourceType,
			RawAmount:       amountStr,
			RawDate:         pgtype.Date{Time: txnDate, Valid: true},
			RawDescription:  pgtype.Text{String: input.Description, Valid: input.Description != ""},
			ConfidenceScore: pgtype.Numeric{Valid: false},
			AiReasoning:     pgtype.Text{Valid: false},
			Status:          "PENDING_CLASSIFICATION",
		})

		if err != nil {
			s.logger.Warn("Failed to create pending proposed transaction", "error", err)
			return nil, err
		}

		// Save any provided hints to the rule result/audit log if necessary, or pass through metadata.
		// For now, we return the local UUID as the ERPEntityID while it is pending sync.
		var idBytes [16]byte = proposed.ID.Bytes
		idStr := fmt.Sprintf("%x-%x-%x-%x-%x", idBytes[0:4], idBytes[4:6], idBytes[6:8], idBytes[8:10], idBytes[10:16])

		return &erp.PostedExpense{
			ERPEntityID: idStr, // Return local UUID since ERP didn't create it yet
			EntityType:  sourceType,
		}, nil
	}

	return nil, fmt.Errorf("database transaction repository is required for async expense posting")
}

// QueueRecategorization accepts user input to change a transaction's account or vendor
// and publishes an async NATS event to the ERPEventWorker rather than blocking the UI.
func (s *TransactionService) QueueRecategorization(ctx context.Context, realmID, erpEntityID, entityType, newAccountID, newVendorID string) error {
	if s.nc == nil {
		return fmt.Errorf("NATS connection not configured for TransactionService")
	}

	payload := connectors.RecategorizePayload{
		ERPEntityID:  erpEntityID,
		EntityType:   entityType,
		NewAccountID: newAccountID,
		NewVendorID:  newVendorID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal RecategorizePayload: %w", err)
	}

	event := connectors.ERPEvent{
		Type:    connectors.EventRecategorizeTransaction,
		RealmID: realmID,
		Payload: payloadBytes,
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal ERPEvent: %w", err)
	}

	// Publish to the specific .recategorize subject
	subject := strings.Replace(s.eventSubject, "*", "recategorize", 1)
	if !strings.Contains(subject, "recategorize") {
		subject = subject + ".recategorize"
	}

	if err := s.nc.Publish(subject, eventBytes); err != nil {
		return fmt.Errorf("failed to publish recategorization event: %w", err)
	}

	s.logger.Info("Queued transaction recategorization via NATS", "erp_entity_id", erpEntityID, "subject", subject)
	return nil
}

// FetchUnifiedTransactions retrieves all unified transactions (Bills and Invoices) for a single realm from the shadow_erp mirroring database.
func (s *TransactionService) FetchUnifiedTransactions(ctx context.Context, realmID string, statusFilter *string) ([]erp.Transaction, error) {
	if realmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	rows, err := s.repo.GetUnifiedTransactions(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get unified transactions: %w", err)
	}

	var results []erp.Transaction
	for _, r := range rows {
		amount := 0.0
		if r.TotalAmount.Valid {
			if v, err := r.TotalAmount.Float64Value(); err == nil {
				amount = v.Float64
			}
		}

		txnDate := time.Time{}
		if r.TxnDate.Valid {
			txnDate = r.TxnDate.Time
		}

		name := ""
		if r.EntityName.Valid {
			name = r.EntityName.String
		}

		var entityID string
		if r.EntityID.Valid {
			bytes := r.EntityID.Bytes
			entityID = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
		}

		txn := erp.Transaction{
			ToroID:      fmt.Sprintf("%x-%x-%x-%x-%x", r.ID.Bytes[0:4], r.ID.Bytes[4:6], r.ID.Bytes[6:8], r.ID.Bytes[8:10], r.ID.Bytes[10:16]),
			ExternalID:  r.ErpID,
			Amount:      amount,
			VendorName:  name,
			VendorID:    entityID,
			Date:        txnDate,
			SourceType:  r.SourceType,
			Description: r.DocNumber.String, // Using doc_number roughly as description if memo isn't joined
		}

		results = append(results, txn)
	}

	// TODO: Apply optional status filtering manually or using another DB query if needed
	// (e.g., checking proposed_transactions for 'RECONCILED' state)

	return results, nil
}
