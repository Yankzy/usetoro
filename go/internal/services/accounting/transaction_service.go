package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	quickbooks "github.com/Yankzy/usetoro/qbo"
	"github.com/jackc/pgx/v5/pgtype"
)

// TransactionRepository defines the data access methods needed for idempotency and audit logging
type TransactionRepository interface {
	GetProposedTransactionByValues(ctx context.Context, arg database.GetProposedTransactionByValuesParams) (database.ShadowErpProposedTransaction, error)
	CreateProposedTransaction(ctx context.Context, arg database.CreateProposedTransactionParams) (database.ShadowErpProposedTransaction, error)
	UpdateProposedTransactionSyncStatus(ctx context.Context, arg database.UpdateProposedTransactionSyncStatusParams) error
	GetVendorByQBOID(ctx context.Context, arg database.GetVendorByQBOIDParams) (database.ShadowErpVendor, error)
	GetAccountByQBOID(ctx context.Context, arg database.GetAccountByQBOIDParams) (database.ShadowErpAccount, error)
	GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error)
	GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error)
}

// QBOClientFn is a function that returns an authenticated QBO client for a given realm.
// This decouples the service from the connector layer and simplifies testing.
type QBOClientFn func(ctx context.Context, realmID string) (*quickbooks.Client, error)

// purchaseCreator is an internal interface satisfied by *quickbooks.Client.
// Extracted to enable unit testing without live HTTP.
type purchaseCreator interface {
	CreatePurchase(*quickbooks.Purchase) (*quickbooks.Purchase, error)
}

// billCreator is an internal interface satisfied by *quickbooks.Client.
type billCreator interface {
	CreateBill(*quickbooks.Bill) (*quickbooks.Bill, error)
}

// ExpenseInput represents the data needed to post an expense to QBO.
type ExpenseInput struct {
	RealmID     string
	Description string
	Amount      float64
	Paid        bool      // true → create Purchase (immediate, paid); false → create Bill (payable)
	TxnDate     time.Time // defaults to today if zero
	// Optional overrides — bypass AI resolution when provided.
	AccountHint string // QBO account ID (skips CoAMapper)
	VendorHint  string // QBO vendor ID (skips EntityResolver)
}

// PostedExpense is returned after successfully posting the expense to QBO.
type PostedExpense struct {
	QBOEntityID  string // ID of the created Purchase or Bill in QBO
	EntityType   string // "Purchase" or "Bill"
	AccountQBOID string
	VendorQBOID  string
	Amount       float64
}

// TransactionService orchestrates posting expenses to QuickBooks Online.
// It uses EntityResolver and CoAMapper to resolve vendor and account
// references before delegating to the QBO SDK.
type TransactionService struct {
	logger         *slog.Logger
	repo           TransactionRepository
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	clientFn       QBOClientFn
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
	clientFn QBOClientFn,
	ruleEngine *RuleEngineService,
) *TransactionService {
	return &TransactionService{
		logger:         logger,
		repo:           repo,
		entityResolver: resolver,
		coaMapper:      coa,
		clientFn:       clientFn,
		ruleEngine:     ruleEngine,
	}
}

// PostExpense resolves the vendor and account via AI, then creates either a
// Purchase (paid=true) or Bill (paid=false) in QBO.
func (s *TransactionService) PostExpense(ctx context.Context, input ExpenseInput) (*PostedExpense, error) {
	return s.postExpense(ctx, input, nil, nil)
}

// postExpense is the internal implementation, accepting optional mock overrides for
// purchaseCreator and billCreator (nil means use the real *quickbooks.Client from clientFn).
func (s *TransactionService) postExpense(
	ctx context.Context,
	input ExpenseInput,
	pcOverride purchaseCreator,
	bcOverride billCreator,
) (*PostedExpense, error) {
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
	if s.ruleEngine != nil {
		txForRule := quickbooks.Transaction{
			EntityID:    input.RealmID,
			Description: input.Description,
			Amount:      input.Amount,
			Date:        txnDate,
		}
		result, err := s.ruleEngine.EvaluateTransaction(ctx, txForRule)
		if err != nil {
			s.logger.Warn("Rule engine evaluation failed", "error", err)
		} else if result != nil {
			s.logger.Info("✅ Rule Engine matched! Bypassing AI resolution.")

			if result.TargetAccountID.Valid {
				if acct, err := s.repo.GetAccountByID(ctx, result.TargetAccountID); err == nil {
					input.AccountHint = acct.QboID
				}
			}
			if result.TargetVendorID.Valid {
				if vendor, err := s.repo.GetVendorByID(ctx, result.TargetVendorID); err == nil {
					input.VendorHint = vendor.QboID
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

	// 3. Resolve the QBO client (or use override for testing)
	var pc purchaseCreator = pcOverride
	var bc billCreator = bcOverride
	if pc == nil || bc == nil {
		client, err := s.clientFn(ctx, input.RealmID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO client: %w", err)
		}
		if pc == nil {
			pc = client
		}
		if bc == nil {
			bc = client
		}
	}

	amountStr := json.Number(fmt.Sprintf("%.2f", input.Amount))

	var vendorPgUUID pgtype.UUID
	var accountPgUUID pgtype.UUID

	if s.repo != nil {
		if vendorID != "" {
			v, err := s.repo.GetVendorByQBOID(ctx, database.GetVendorByQBOIDParams{RealmID: input.RealmID, QboID: vendorID})
			if err == nil {
				vendorPgUUID = v.ID
			}
		}
		if accountID != "" {
			a, err := s.repo.GetAccountByQBOID(ctx, database.GetAccountByQBOIDParams{RealmID: input.RealmID, QboID: accountID})
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
			s.logger.Info("⏭️ Idempotency check passed: Transaction already synced", "qbo_id", existing.QboTransactionID.String)
			return &PostedExpense{
				QBOEntityID:  existing.QboTransactionID.String,
				EntityType:   existing.SourceType,
				AccountQBOID: accountID,
				VendorQBOID:  vendorID,
				Amount:       input.Amount,
			}, nil
		}
	}

	result := &PostedExpense{
		AccountQBOID: accountID,
		VendorQBOID:  vendorID,
		Amount:       input.Amount,
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

	// 4. Create Purchase (paid) or Bill (unpaid)
	var qboErr error
	var createdID string

	if input.Paid {
		purchase := &quickbooks.Purchase{
			PaymentType: "Cash",
			AccountRef:  quickbooks.ReferenceType{Value: accountID},
			TxnDate:     quickbooks.Date{Time: txnDate},
			PrivateNote: input.Description,
			Line: []quickbooks.Line{
				{
					Amount:      amountStr,
					DetailType:  "AccountBasedExpenseLineDetail",
					Description: input.Description,
					AccountBasedExpenseLineDetail: quickbooks.AccountBasedExpenseLineDetail{
						AccountRef: quickbooks.ReferenceType{Value: accountID},
					},
				},
			},
		}
		if vendorID != "" {
			purchase.EntityRef = quickbooks.ReferenceType{Value: vendorID, Type: "Vendor"}
		}

		created, err := pc.CreatePurchase(purchase)
		if err != nil {
			qboErr = fmt.Errorf("failed to create Purchase in QBO: %w", err)
		} else {
			createdID = created.Id
			result.QBOEntityID = createdID
			result.EntityType = "Purchase"
			s.logger.Info("✅ Created QBO Purchase",
				"realm_id", input.RealmID,
				"purchase_id", createdID,
				"amount", input.Amount,
				"account_id", accountID,
			)
		}
	} else {
		bill := &quickbooks.Bill{
			VendorRef:   quickbooks.ReferenceType{Value: vendorID},
			TxnDate:     quickbooks.Date{Time: txnDate},
			PrivateNote: input.Description,
			Line: []quickbooks.Line{
				{
					Amount:      amountStr,
					DetailType:  "AccountBasedExpenseLineDetail",
					Description: input.Description,
					AccountBasedExpenseLineDetail: quickbooks.AccountBasedExpenseLineDetail{
						AccountRef: quickbooks.ReferenceType{Value: accountID},
					},
				},
			},
		}

		created, err := bc.CreateBill(bill)
		if err != nil {
			qboErr = fmt.Errorf("failed to create Bill in QBO: %w", err)
		} else {
			createdID = created.Id
			result.QBOEntityID = createdID
			result.EntityType = "Bill"
			s.logger.Info("✅ Created QBO Bill",
				"realm_id", input.RealmID,
				"bill_id", createdID,
				"amount", input.Amount,
				"vendor_id", vendorID,
				"account_id", accountID,
			)
		}
	}

	if s.repo != nil && proposedTxID.Valid {
		status := "SYNCED"
		errMsg := ""
		if qboErr != nil {
			status = "ERROR"
			errMsg = qboErr.Error()
		}
		updErr := s.repo.UpdateProposedTransactionSyncStatus(ctx, database.UpdateProposedTransactionSyncStatusParams{
			ID:               proposedTxID,
			SyncStatus:       pgtype.Text{String: status, Valid: true},
			QboTransactionID: pgtype.Text{String: createdID, Valid: createdID != ""},
			ErrorMessage:     pgtype.Text{String: errMsg, Valid: errMsg != ""},
		})
		if updErr != nil {
			s.logger.Warn("Failed to update proposed transaction status", "error", updErr)
		}
	}

	if qboErr != nil {
		var objNotFound quickbooks.ObjectNotFoundError
		if errors.As(qboErr, &objNotFound) {
			return nil, fmt.Errorf("ObjectNotFoundError: client needs CDC sync: %w", qboErr)
		}
		return nil, qboErr
	}

	return result, nil
}
