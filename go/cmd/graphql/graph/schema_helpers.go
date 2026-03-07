package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	qboConn := connectors.NewQBOConnector(r.Logger, cfg, r.Store, nil)
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

func mapSessionToModel(s database.ShadowErpCleanupSession) *model.CleanupSession {
	out := &model.CleanupSession{
		ID:        uuid.UUID(s.ID.Bytes).String(),
		RowCount:  s.RowCount,
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

func mapStagingRowToModel(r database.ShadowErpCleanupStaging) *model.CleanupRow {
	realmID := ""
	if r.RealmID.Valid {
		realmID = r.RealmID.String
	}
	out := &model.CleanupRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   uuid.UUID(r.SessionID.Bytes).String(),
		RealmID:     realmID,
		RawAmount:   numericToFloat64(r.RawAmount),
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
	if r.RawVendorName.Valid {
		out.RawVendorName = &r.RawVendorName.String
	}
	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedAccountID.Valid {
		s := uuid.UUID(r.PredictedAccountID.Bytes).String()
		out.PredictedAccountID = &s
	}
	if r.NormalizedVendor.Valid {
		out.NormalizedVendor = &r.NormalizedVendor.String
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

func mapSessionRowToModel(r database.GetSessionRowsRow) *model.CleanupRow {
	rowRealmID := ""
	if r.RealmID.Valid {
		rowRealmID = r.RealmID.String
	}
	out := &model.CleanupRow{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		SessionID:   uuid.UUID(r.SessionID.Bytes).String(),
		RealmID:     rowRealmID,
		RawAmount:   numericToFloat64(r.RawAmount),
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
	if r.RawVendorName.Valid {
		out.RawVendorName = &r.RawVendorName.String
	}
	if r.PredictedVendorID.Valid {
		s := uuid.UUID(r.PredictedVendorID.Bytes).String()
		out.PredictedVendorID = &s
	}
	if r.PredictedVendorName.Valid {
		out.PredictedVendorName = &r.PredictedVendorName.String
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
	if r.NormalizedVendor.Valid {
		out.NormalizedVendor = &r.NormalizedVendor.String
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

// buildQBOPurchase constructs a QBO Purchase object from a staging row.
// It resolves the vendor's QBO ID from the DB if vendorPgID is valid.
func buildQBOPurchase(
	row database.ShadowErpCleanupStaging,
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

func numericToFloat64(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

func uuidStrFromPG(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
