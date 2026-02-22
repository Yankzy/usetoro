package graph

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/cmd/graphql/graph/model"
	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	if a.QboCreatedTime.Valid {
		qboCreatedTime = &a.QboCreatedTime.Time
	}

	var qboUpdatedTime *time.Time
	if a.QboUpdatedTime.Valid {
		qboUpdatedTime = &a.QboUpdatedTime.Time
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
		QboCreatedTime:                qboCreatedTime,
		QboUpdatedTime:                qboUpdatedTime,
		CurrentBalance:                currentBalance,
		SubAccount:                    subAccount,
		CreatedAt:                     a.CreatedAt.Time,
		UpdatedAt:                     a.UpdatedAt.Time,
		DeletedAt:                     deletedAt,
	}
}
