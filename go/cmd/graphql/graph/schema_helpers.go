package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/cmd/graphql/graph/model"
	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// getQBOConnectorHelper initializes the QBO connector after validating the user
func (r *mutationResolver) getQBOConnectorHelper(ctx context.Context, realmID string) (*connectors.QBOConnector, uuid.UUID, error) {
	entityID, _ := ctx.Value(auth.EntityIDKey).(uuid.UUID)
	if entityID == uuid.Nil {
		return nil, uuid.Nil, fmt.Errorf("unauthorized")
	}

	conn, err := r.Store.GetQBOConnection(ctx, entityID.String())
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, uuid.Nil, fmt.Errorf("no qbo connection")
		}
		r.Logger.Error("Failed to fetch QBO connection", "error", err)
		return nil, uuid.Nil, fmt.Errorf("internal server error")
	}

	if conn.RealmID != realmID {
		return nil, uuid.Nil, fmt.Errorf("unauthorized")
	}

	clientID := os.Getenv("QBO_CLIENT_ID")
	clientSecret := os.Getenv("QBO_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return nil, uuid.Nil, fmt.Errorf("qbo client credentials not configured")
	}

	isProd := strings.EqualFold(os.Getenv("QBO_IS_PRODUCTION"), "true")
	cfg := &config.Config{
		QBOClientID:     clientID,
		QBOClientSecret: clientSecret,
		QBOIsProduction: isProd,
	}

	qboConn := connectors.NewQBOConnector(r.Logger, cfg, r.Store)
	return qboConn, entityID, nil
}

// mapDatabaseCustomerToModel maps a database ShadowErpCustomer to a GraphQL Customer model.
func mapDatabaseCustomerToModel(c *database.ShadowErpCustomer) *model.Customer {
	var deletedAt *time.Time
	if c.DeletedAt.Valid {
		deletedAt = &c.DeletedAt.Time
	}
	return &model.Customer{
		ID:          uuid.UUID(c.ID.Bytes).String(),
		RealmID:     c.RealmID,
		DisplayName: c.DisplayName,
		SyncToken:   c.SyncToken,
		CreatedAt:   c.CreatedAt.Time,
		UpdatedAt:   c.UpdatedAt.Time,
		DeletedAt:   deletedAt,
	}
}

// mapDatabaseVendorToModel maps a database ShadowErpVendor to a GraphQL Vendor model.
func mapDatabaseVendorToModel(v *database.ShadowErpVendor) *model.Vendor {
	var lastKnownAccountID *string
	if v.LastKnownAccountID.Valid {
		s := uuid.UUID(v.LastKnownAccountID.Bytes).String()
		lastKnownAccountID = &s
	}
	var deletedAt *time.Time
	if v.DeletedAt.Valid {
		deletedAt = &v.DeletedAt.Time
	}
	return &model.Vendor{
		ID:                 uuid.UUID(v.ID.Bytes).String(),
		RealmID:            v.RealmID,
		ErpID:              v.ErpID,
		DisplayName:        v.DisplayName,
		SyncToken:          v.SyncToken,
		LastKnownAccountID: lastKnownAccountID,
		CreatedAt:          v.CreatedAt.Time,
		UpdatedAt:          v.UpdatedAt.Time,
		DeletedAt:          deletedAt,
	}
}

// mapDatabaseAccountToModel maps a database ShadowErpAccount to a GraphQL Account model
func mapDatabaseAccountToModel(a *database.ShadowErpAccount) *model.Account {
	var classification *string
	if a.Classification.Valid {
		classification = &a.Classification.String
	}

	var accountType *string
	if a.AccountType != "" {
		t := a.AccountType
		accountType = &t
	}

	var accountSubType *string
	if a.AccountSubType.Valid {
		accountSubType = &a.AccountSubType.String
	}

	var fullyQualifiedName *string
	if a.FullyQualifiedName.Valid {
		fullyQualifiedName = &a.FullyQualifiedName.String
	}

	var active *bool
	if a.Active.Valid {
		active = &a.Active.Bool
	}

	var deletedAt *time.Time
	if a.DeletedAt.Valid {
		deletedAt = &a.DeletedAt.Time
	}

	var domain *string
	if a.Domain.Valid {
		domain = &a.Domain.String
	}

	var currencyRefName *string
	if a.CurrencyRefName.Valid {
		currencyRefName = &a.CurrencyRefName.String
	}

	var currencyRefValue *string
	if a.CurrencyRefValue.Valid {
		currencyRefValue = &a.CurrencyRefValue.String
	}

	var currentBalanceWithSubAccounts *float64
	if a.CurrentBalanceWithSubAccounts.Valid {
		v, _ := a.CurrentBalanceWithSubAccounts.Float64Value()
		currentBalanceWithSubAccounts = &v.Float64
	}

	var sparse *bool
	if a.Sparse.Valid {
		sparse = &a.Sparse.Bool
	}

	var qboCreatedTime *time.Time
	if a.ErpCreatedTime.Valid {
		qboCreatedTime = &a.ErpCreatedTime.Time
	}

	var qboUpdatedTime *time.Time
	if a.ErpUpdatedTime.Valid {
		qboUpdatedTime = &a.ErpUpdatedTime.Time
	}

	var currentBalance *float64
	if a.CurrentBalance.Valid {
		v, _ := a.CurrentBalance.Float64Value()
		currentBalance = &v.Float64
	}

	var subAccount *bool
	if a.SubAccount.Valid {
		subAccount = &a.SubAccount.Bool
	}

	return &model.Account{
		ID:                            uuid.UUID(a.ID.Bytes).String(),
		RealmID:                       a.RealmID,
		Name:                          a.Name,
		Classification:                classification,
		AccountType:                   accountType,
		AccountSubType:                accountSubType,
		FullyQualifiedName:            fullyQualifiedName,
		Active:                        active,
		SyncToken:                     a.SyncToken,
		Domain:                        domain,
		CurrencyRefName:               currencyRefName,
		CurrencyRefValue:              currencyRefValue,
		CurrentBalanceWithSubAccounts: currentBalanceWithSubAccounts,
		Sparse:                        sparse,
		ErpCreatedTime:                qboCreatedTime,
		ErpUpdatedTime:                qboUpdatedTime,
		CurrentBalance:                currentBalance,
		SubAccount:                    subAccount,
		CreatedAt:                     a.CreatedAt.Time,
		UpdatedAt:                     a.UpdatedAt.Time,
		DeletedAt:                     deletedAt,
	}
}

// ─── Clean-Up Mode Mappers ────────────────────────────────────────────────────

func mapSessionToModel(s database.FignodeStagingSession) *model.FignodeSession {
	out := &model.FignodeSession{
		ID:        uuid.UUID(s.ID.Bytes).String(),
		RowCount:  int32(s.RowCount),
		Status:    s.Status,
		CreatedAt: s.CreatedAt.Time,
		UpdatedAt: s.UpdatedAt.Time,
	}
	if s.RealmID.Valid {
		out.RealmID = &s.RealmID.String
	}
	if s.FileName.Valid {
		out.FileName = &s.FileName.String
	}
	return out
}

func mapStagingRowToModel(r database.FignodeStagingTransaction) *model.FignodeStagingRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &realmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}
	// Note: RawVendorName not explicitly available in FignodeStagingTransaction;
	// it merges vendor/desc based on Fignode schema simplicity, so we omit mapping it directly from base model.

	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}

func mapSessionRowToModel(r database.GetSessionRowsRow) *model.FignodeStagingRow {
	rowRealmID := ""
	if r.RealmID.Valid {
		rowRealmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &rowRealmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}

	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedVendorName != "" {
		out.PredictedVendorName = &r.PredictedVendorName
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}
	if r.PredictedAccountName.Valid {
		out.PredictedAccountName = &r.PredictedAccountName.String
	}
	if r.PredictedAccountType.Valid {
		out.PredictedAccountType = &r.PredictedAccountType.String
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideVendorName.Valid {
		out.OverrideVendorName = &r.OverrideVendorName.String
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.OverrideAccountName.Valid {
		out.OverrideAccountName = &r.OverrideAccountName.String
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}

func mapGetPendingSessionRowsRowToModel(r database.GetPendingSessionRowsRow) *model.FignodeStagingRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &realmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}

	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedVendorName != "" {
		out.PredictedVendorName = &r.PredictedVendorName
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}
	if r.PredictedAccountName.Valid {
		out.PredictedAccountName = &r.PredictedAccountName.String
	}
	if r.PredictedAccountType.Valid {
		out.PredictedAccountType = &r.PredictedAccountType.String
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideVendorName.Valid {
		out.OverrideVendorName = &r.OverrideVendorName.String
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.OverrideAccountName.Valid {
		out.OverrideAccountName = &r.OverrideAccountName.String
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}

func mapGetCleanupRowRowToModel(r database.GetCleanupRowRow) *model.FignodeStagingRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &realmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}
	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}

func mapOverrideCleanupRowRowToModel(r database.OverrideCleanupRowRow) *model.FignodeStagingRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &realmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}
	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}

// buildQBOPurchase constructs a QBO Purchase object from a staging row.
// It resolves the vendor's QBO ID from the DB if vendorPgID is valid.
func buildQBOPurchase(
	row database.FignodeStagingTransaction,
	accountERPID string,
	vendorPgID pgtype.UUID,
	q *database.Queries,
	ctx context.Context,
	amount float64,
) *quickbooks.Purchase {
	amtStr := fmt.Sprintf("%.2f", amount)

	purchase := &quickbooks.Purchase{
		PaymentType: "Cash",
		AccountRef:  quickbooks.ReferenceType{Value: accountERPID},
		Line: []quickbooks.Line{{
			Amount:     json.Number(amtStr),
			DetailType: "AccountBasedExpenseLineDetail",
			AccountBasedExpenseLineDetail: quickbooks.AccountBasedExpenseLineDetail{
				AccountRef: quickbooks.ReferenceType{Value: accountERPID},
			},
		}},
	}

	if row.RawDate.Valid {
		purchase.TxnDate = quickbooks.Date{Time: row.RawDate.Time}
	}
	if row.RawDescription.Valid {
		purchase.PrivateNote = row.RawDescription.String
	}

	// Resolve vendor QBO ID.
	if vendorPgID.Valid && q != nil {
		vendor, err := q.GetVendorByID(ctx, vendorPgID)
		if err == nil {
			purchase.EntityRef = quickbooks.ReferenceType{
				Value: vendor.ErpID,
				Type:  "Vendor",
			}
		}
	}

	return purchase
}

func parseDirtyStringAmount(s string) float64 {
	if s == "" {
		return 0
	}
	cleaned := strings.ReplaceAll(s, "*", "")
	cleaned = strings.TrimSpace(cleaned)
	if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
		return f
	}
	return 0
}

func uuidStrFromPG(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func mapGetPendingRealmRowsRowToModel(r database.GetPendingRealmRowsRow) *model.FignodeStagingRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	var sessionID *string
	if r.SessionID.Valid {
		sid := uuid.UUID(r.SessionID.Bytes).String()
		sessionID = &sid
	}
	out := &model.FignodeStagingRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   sessionID,
		RealmID:     &realmID,
		SourceType:  r.SourceType,
		RawAmount:   parseDirtyStringAmount(r.RawAmount),
		IsDuplicate: r.DuplicateOf.Valid,
		IsRecurring: r.IsRecurring,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
	if r.RawDescription.Valid {
		out.RawDescription = &r.RawDescription.String
	}
	if r.RawDate.Valid {
		out.RawDate = &r.RawDate.Time
	}

	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedVendorName != "" {
		out.PredictedVendorName = &r.PredictedVendorName
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}
	if r.PredictedAccountName.Valid {
		out.PredictedAccountName = &r.PredictedAccountName.String
	}
	if r.PredictedAccountType.Valid {
		out.PredictedAccountType = &r.PredictedAccountType.String
	}

	if r.ConfidenceScore.Valid {
		f, _ := r.ConfidenceScore.Float64Value()
		out.ConfidenceScore = &f.Float64
	}
	if r.AiReasoning.Valid {
		out.AiReasoning = &r.AiReasoning.String
	}
	if r.DuplicateOf.Valid {
		s := uuid.UUID(r.DuplicateOf.Bytes).String()
		out.DuplicateOf = &s
	}
	if len(r.SplitSuggestion) > 0 {
		s := string(r.SplitSuggestion)
		out.SplitSuggestion = &s
	}
	if r.OverrideVendorID.Valid {
		s := uuid.UUID(r.OverrideVendorID.Bytes).String()
		out.OverrideVendorID = &s
	}
	if r.OverrideVendorName.Valid {
		out.OverrideVendorName = &r.OverrideVendorName.String
	}
	if r.OverrideAccountID.Valid {
		s := uuid.UUID(r.OverrideAccountID.Bytes).String()
		out.OverrideAccountID = &s
	}
	if r.OverrideAccountName.Valid {
		out.OverrideAccountName = &r.OverrideAccountName.String
	}
	if r.ErpTransactionID.Valid {
		out.ErpTransactionID = &r.ErpTransactionID.String
	}
	return out
}



func (r *mutationResolver) postCleanupSessionHelper(ctx context.Context, sessionID string) (*model.FignodePostResult, error) {
	sessionUUID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid session id")
	}
	pgSessionID := pgtype.UUID{Bytes: sessionUUID, Valid: true}

	// Fetch the session to get realm_id.
	session, err := r.Store.Queries.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		r.Logger.Error("PostCleanupSession: get session", "error", err)
		return nil, fmt.Errorf("internal server error")
	}

	approvedRows, err := r.Store.Queries.GetApprovedRows(ctx, pgSessionID)
	if err != nil {
		r.Logger.Error("PostCleanupSession: get approved rows", "error", err)
		return nil, fmt.Errorf("internal server error")
	}
	if len(approvedRows) == 0 {
		return &model.FignodePostResult{
			SessionID: sessionID, PostedCount: 0, ErrorCount: 0, Errors: []string{},
		}, nil
	}

	if !session.RealmID.Valid || session.RealmID.String == "" {
		return nil, fmt.Errorf("this session has no QBO connection — download the Excel export instead")
	}

	// Build QBO connector using shared helper.
	qboConnector, _, err := r.getQBOConnectorHelper(ctx, session.RealmID.String)
	if err != nil {
		return nil, err
	}
	qboClient, err := qboConnector.ClientForRealm(ctx, session.RealmID.String)
	if err != nil {
		r.Logger.Error("PostCleanupSession: get QBO client", "realm_id", session.RealmID, "error", err)
		return nil, fmt.Errorf("failed to connect to QuickBooks")
	}

	result := &model.FignodePostResult{
		SessionID: sessionID,
		Errors:    []string{},
	}

	// Process in batches of BatchMaxSize (30).
	const batchMax = 30
	for batchStart := 0; batchStart < len(approvedRows); batchStart += batchMax {
		end := batchStart + batchMax
		if end > len(approvedRows) {
			end = len(approvedRows)
		}
		batch := approvedRows[batchStart:end]

		// Build batch requests, mapping staging rows → QBO Purchase objects.
		builder := quickbooks.NewBatchBuilder()
		rowIDBatch := make([]pgtype.UUID, 0, len(batch))

		for _, row := range batch {
			// Resolve effective vendor + account (override takes precedence over prediction).
			accountPgID := row.OverrideAccountID
			if !accountPgID.Valid {
				accountPgID = row.PredictedAccountID
			}
			vendorPgID := row.OverrideVendorID
			if !vendorPgID.Valid {
				vendorPgID = row.PredictedVendorID
			}

			if !accountPgID.Valid {
				result.ErrorCount++
				result.Errors = append(result.Errors,
					fmt.Sprintf("row %s: no account resolved", uuidStrFromPG(row.ID)))
				continue
			}

			// Look up QBO IDs from shadow tables.
			acct, err := r.Store.Queries.GetAccountByID(ctx, accountPgID)
			if err != nil {
				result.ErrorCount++
				result.Errors = append(result.Errors,
					fmt.Sprintf("row %s: account lookup failed: %v", uuidStrFromPG(row.ID), err))
				continue
			}

			amount := parseDirtyStringAmount(row.RawAmount)

			fignodeTx := database.FignodeStagingTransaction{
				ID:                 row.ID,
				SessionID:          row.SessionID,
				RealmID:            row.RealmID,
				RawDescription:     row.RawDescription,
				RawAmount:          row.RawAmount,
				RawDate:            row.RawDate,
				PredictedVendorID:  row.PredictedVendorID,
				PredictedAccountID: row.PredictedAccountID,
				ConfidenceScore:    row.ConfidenceScore,
				AiReasoning:        row.AiReasoning,
				DuplicateOf:        row.DuplicateOf,
				IsRecurring:        row.IsRecurring,
				SplitSuggestion:    row.SplitSuggestion,
				OverrideVendorID:   row.OverrideVendorID,
				OverrideAccountID:  row.OverrideAccountID,
				Status:             row.Status,
				ErpTransactionID:   row.ErpTransactionID,
				CreatedAt:          row.CreatedAt,
				UpdatedAt:          row.UpdatedAt,
			}
			purchase := buildQBOPurchase(fignodeTx, acct.ErpID, vendorPgID, r.Store.Queries, ctx, amount)
			builder.AddCreate("Purchase", purchase)
			rowIDBatch = append(rowIDBatch, row.ID)
		}

		if builder.Count() == 0 {
			continue
		}

		batchResp, batchErr := qboClient.BatchContext(ctx, builder.Build())
		if batchErr != nil {
			r.Logger.Error("PostCleanupSession: batch call failed", "error", batchErr)
			for _, id := range rowIDBatch {
				result.ErrorCount++
				result.Errors = append(result.Errors,
					fmt.Sprintf("row %s: batch error: %v", uuidStrFromPG(id), batchErr))
			}
			continue
		}

		// Map responses back to rows.
		for i, resp := range batchResp.BatchItemResponse {
			if i >= len(rowIDBatch) {
				break
			}
			rowID := rowIDBatch[i]
			if resp.HasError() {
				result.ErrorCount++
				result.Errors = append(result.Errors,
					fmt.Sprintf("row %s: QBO error: %s", uuidStrFromPG(rowID), resp.GetError()))
				continue
			}
			// Extract the created Purchase ID.
			qboTxnID := ""
			if resp.Purchase != nil {
				qboTxnID = resp.Purchase.Id
			}
			if markErr := r.Store.Queries.MarkRowPosted(ctx, database.MarkRowPostedParams{
				ID:               rowID,
				ErpTransactionID: pgtype.Text{String: qboTxnID, Valid: qboTxnID != ""},
			}); markErr != nil {
				r.Logger.Warn("PostCleanupSession: mark row posted failed",
					"row_id", uuidStrFromPG(rowID), "error", markErr)
			}
			result.PostedCount++
		}
	}

	// Mark session as POSTED if all rows were posted.
	if result.ErrorCount == 0 {
		if statusErr := r.Store.Queries.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
			ID:     pgSessionID,
			Status: "POSTED",
		}); statusErr != nil {
			r.Logger.Warn("PostCleanupSession: update session status failed", "error", statusErr)
		}
	}

	return result, nil
}
func (r *queryResolver) cleanupSessionsHelper(ctx context.Context, realmID *string) ([]*model.FignodeSession, error) {
	arg := database.ListCleanupSessionsParams{}
	if realmID != nil && *realmID != "" {
		arg.RealmID = pgtype.Text{String: *realmID, Valid: true}
	} else {
		// Fall back to user-scoped listing.
		userID, _ := ctx.Value(auth.UserIDKey).(uuid.UUID)
		if userID != uuid.Nil {
			arg.CreatedBy = pgtype.UUID{Bytes: userID, Valid: true}
		}
	}

	rows, err := r.Store.Queries.ListCleanupSessions(ctx, arg)
	if err != nil {
		r.Logger.Error("CleanupSessions: db error", "realm_id", realmID, "error", err)
		return nil, fmt.Errorf("internal server error")
	}
	out := make([]*model.FignodeSession, 0, len(rows))
	for _, s := range rows {
		out = append(out, mapSessionToModel(s))
	}
	return out, nil
}
func (r *queryResolver) cleanupRowsHelper(ctx context.Context, sessionID string, status *string) ([]*model.FignodeStagingRow, error) {
	sessionUUID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid session id")
	}
	pgSessionID := pgtype.UUID{Bytes: sessionUUID, Valid: true}

	var pgStatus pgtype.Text
	if status != nil {
		pgStatus = pgtype.Text{String: *status, Valid: true}
	}

	rows, err := r.Store.Queries.GetSessionRows(ctx, database.GetSessionRowsParams{
		SessionID: pgSessionID,
		Status:    pgStatus,
	})
	if err != nil {
		r.Logger.Error("CleanupRows: db error", "session_id", sessionID, "error", err)
		return nil, fmt.Errorf("internal server error")
	}

	out := make([]*model.FignodeStagingRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapSessionRowToModel(row))
	}
	return out, nil
}
