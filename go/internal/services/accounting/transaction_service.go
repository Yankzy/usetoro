package accounting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/jackc/pgx/v5/pgtype"
)

// TransactionRepository defines the data access methods needed for idempotency and audit logging
type TransactionRepository interface {
	GetProposedTransactionByValues(ctx context.Context, arg database.GetProposedTransactionByValuesParams) (database.ShadowErpProposedTransaction, error)
	CreateProposedTransaction(ctx context.Context, arg database.CreateProposedTransactionParams) (database.ShadowErpProposedTransaction, error)
	UpdateProposedTransactionSyncStatus(ctx context.Context, arg database.UpdateProposedTransactionSyncStatusParams) error
	GetVendorByERPID(ctx context.Context, arg database.GetVendorByERPIDParams) (database.ShadowErpVendor, error)
	GetAccountByERPID(ctx context.Context, arg database.GetAccountByERPIDParams) (database.ShadowErpAccount, error)
	GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error)
	GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error)
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
) *TransactionService {
	return &TransactionService{
		logger:         logger,
		repo:           repo,
		entityResolver: resolver,
		coaMapper:      coa,
		factory:        factory,
		ruleEngine:     ruleEngine,
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
	if input.Description == "" && input.AccountHint == "" {
		return nil, fmt.Errorf("description or AccountHint is required to resolve an account")
	}

	txnDate := input.TxnDate
	if txnDate.IsZero() {
		txnDate = time.Now()
	}

	// 0. Use Rule Engine to deterministically match
	var ruleResult *RuleResult
	if s.ruleEngine != nil {
		txForRule := quickbooks.Transaction{
			EntityID:    input.RealmID,
			Description: input.Description,
			Amount:      input.Amount,
			Date:        txnDate,
			Time:        txnDate,
			Vendor:      input.Vendor,
			Customer:    input.Customer,
			Memo:        input.Memo,
			MCC:         input.MCC,
			InvoiceText: input.InvoiceText,
		}
		rr, err := s.ruleEngine.EvaluateTransaction(ctx, txForRule)
		if err != nil {
			s.logger.Warn("Rule engine evaluation failed", "error", err)
		} else if rr != nil {
			ruleResult = rr
			s.logger.Info("Rule engine matched, bypassing AI resolution")

			if rr.TargetAccountID.Valid {
				if acct, err := s.repo.GetAccountByID(ctx, rr.TargetAccountID); err == nil {
					input.AccountHint = acct.ErpID
				}
			}
			if rr.TargetVendorID.Valid {
				if vendor, err := s.repo.GetVendorByID(ctx, rr.TargetVendorID); err == nil {
					input.VendorHint = vendor.ErpID
				}
			}
		}
	}

	// 1. Resolve Vendor (skip if hint provided)
	vendorID := input.VendorHint
	if vendorID == "" && s.entityResolver != nil && input.Description != "" {
		match, err := s.entityResolver.ResolveEntity(ctx, input.RealmID, "vendor", input.Description)
		if err != nil {
			s.logger.Warn("Vendor resolution failed, proceeding without vendor", "error", err)
		} else if match != nil {
			vendorID = match.ID
			s.logger.Debug("Resolved vendor",
				"vendor_id", vendorID,
				"vendor_name", match.Name,
				"source", match.Source,
				"score", match.Score,
			)
		}
	}

	// Bills require a vendor
	if !input.Paid && vendorID == "" {
		return nil, fmt.Errorf("vendor required for Bill (unpaid expense) but could not be resolved from %q", input.Description)
	}

	// 2. Resolve Account (skip if hint provided)
	accountID := input.AccountHint
	if accountID == "" && s.coaMapper != nil {
		matches, err := s.coaMapper.MapDescriptionToAccount(ctx, input.RealmID, input.Description, 1)
		if err != nil {
			return nil, fmt.Errorf("account resolution failed: %w", err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no matching account found for description %q", input.Description)
		}
		accountID = matches[0].AccountID
		s.logger.Debug("Resolved account",
			"account_id", accountID,
			"account_name", matches[0].Name,
			"score", matches[0].Score,
		)
	}
	if accountID == "" {
		return nil, fmt.Errorf("account is required but could not be resolved; provide AccountHint or configure CoAMapper")
	}

	// 3. Resolve the ERP Provider (or use override for testing)
	var provider *erp.Provider = providerOverride
	if provider == nil {
		var err error
		provider, err = s.factory.GetProviderForRealm(ctx, "quickbooks_online", input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get ERP provider: %w", err)
		}
	}

	var vendorPgUUID pgtype.UUID
	var accountPgUUID pgtype.UUID

	if s.repo != nil {
		if vendorID != "" {
			v, err := s.repo.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{RealmID: input.RealmID, ErpID: vendorID})
			if err == nil {
				vendorPgUUID = v.ID
			}
		}
		if accountID != "" {
			a, err := s.repo.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{RealmID: input.RealmID, ErpID: accountID})
			if err == nil {
				accountPgUUID = a.ID
			}
		}
	}

	amountNum := pgtype.Numeric{}
	amountNum.Scan(fmt.Sprintf("%.2f", input.Amount))

	if s.repo != nil {
		existing, err := s.repo.GetProposedTransactionByValues(ctx, database.GetProposedTransactionByValuesParams{
			RealmID:           input.RealmID,
			PredictedVendorID: vendorPgUUID,
			RawDate:           pgtype.Date{Time: txnDate, Valid: true},
			RawAmount:         amountNum,
		})
		if err == nil && existing.SyncStatus.String == "SYNCED" {
			s.logger.Info("⏭️ Idempotency check passed: Transaction already synced", "erp_id", existing.ErpTransactionID.String)
			return &erp.PostedExpense{
				ERPEntityID: existing.ErpTransactionID.String,
				EntityType:  existing.SourceType,
			}, nil
		}
	}

	sourceType := "Bill"
	if input.Paid {
		sourceType = "Purchase"
	}

	var proposedTxID pgtype.UUID
	if s.repo != nil {
		proposed, err := s.repo.CreateProposedTransaction(ctx, database.CreateProposedTransactionParams{
			RealmID:            input.RealmID,
			SourceType:         sourceType,
			RawAmount:          amountNum,
			RawDate:            pgtype.Date{Time: txnDate, Valid: true},
			RawDescription:     pgtype.Text{String: input.Description, Valid: input.Description != ""},
			PredictedVendorID:  vendorPgUUID,
			PredictedAccountID: accountPgUUID,
			ConfidenceScore:    pgtype.Numeric{Valid: false},
			AiReasoning:        pgtype.Text{String: "AI Match", Valid: true},
			SyncStatus:         pgtype.Text{String: "PENDING", Valid: true},
		})
		if err != nil {
			s.logger.Warn("Failed to create pending proposed transaction", "error", err)
		} else {
			proposedTxID = proposed.ID
		}
	}

	// Persist rule audit log now that proposed_transactions row exists.
	if s.ruleEngine != nil && ruleResult != nil && proposedTxID.Valid {
		s.ruleEngine.PersistAuditLog(ctx, input.RealmID, proposedTxID, ruleResult)
	}

	input.AccountHint = accountID
	input.VendorHint = vendorID

	// 4. Create Purchase (paid) or Bill (unpaid) via the ERP adapter
	created, err := provider.PostExpense(ctx, input)

	if s.repo != nil && proposedTxID.Valid {
		status := "SYNCED"
		errMsg := ""
		if err != nil {
			status = "ERROR"
			errMsg = err.Error()
		}
		var createdID string
		if created != nil {
			createdID = created.ERPEntityID
		}
		updErr := s.repo.UpdateProposedTransactionSyncStatus(ctx, database.UpdateProposedTransactionSyncStatusParams{
			ID:               proposedTxID,
			SyncStatus:       pgtype.Text{String: status, Valid: true},
			ErpTransactionID: pgtype.Text{String: createdID, Valid: createdID != ""},
			ErrorMessage:     pgtype.Text{String: errMsg, Valid: errMsg != ""},
		})
		if updErr != nil {
			s.logger.Warn("Failed to update proposed transaction status", "error", updErr)
		}
	}

	if err != nil {
		if errors.Is(err, erp.ErrNotFound) {
			return nil, fmt.Errorf("erp.ErrNotFound: tenant database might need CDC sync to fetch correct account/vendor IDs: %w", err)
		}
		return nil, err
	}

	s.logger.Info("✅ Created Expense in ERP",
		"realm_id", input.RealmID,
		"entity_id", created.ERPEntityID,
		"entity_type", created.EntityType,
		"amount", input.Amount,
		"vendor_id", vendorID,
		"account_id", accountID,
	)

	return created, nil
}
