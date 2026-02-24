package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	quickbooks "github.com/Yankzy/usetoro/qbo"
	"github.com/jackc/pgx/v5/pgtype"
)

// Connector defines the interface for all external data providers.
type Connector interface {
	Fetch(ctx context.Context, tenantID string) error
}

// QBOConnector integrates with QuickBooks Online.
type QBOConnector struct {
	logger       *slog.Logger
	cfg          *config.Config
	store        *store.Store
	vectorWorker *ai.VectorSyncWorker
}

func NewQBOConnector(logger *slog.Logger, cfg *config.Config, store *store.Store, vw *ai.VectorSyncWorker) *QBOConnector {
	return &QBOConnector{
		logger:       logger,
		cfg:          cfg,
		store:        store,
		vectorWorker: vw,
	}
}

func (c *QBOConnector) Fetch(ctx context.Context, tenantID string) error {
	if err := c.SyncCompanyInfo(ctx, tenantID, ""); err != nil {
		return fmt.Errorf("fetch company info: %w", err)
	}
	if _, err := c.SyncFullChartOfAccounts(ctx, tenantID, ""); err != nil {
		return fmt.Errorf("fetch CoA: %w", err)
	}
	if _, err := c.SyncFullCustomers(ctx, tenantID, ""); err != nil {
		return fmt.Errorf("fetch customers: %w", err)
	}
	if _, err := c.SyncFullVendors(ctx, tenantID, ""); err != nil {
		return fmt.Errorf("fetch vendors: %w", err)
	}
	return nil
}

// FetchEntity fetches a specific QBO entity and upserts it to the shadow DB.
// Called by webhook processor when an entity change notification is received.
func (c *QBOConnector) FetchEntity(ctx context.Context, realmID, entityType, entityID, operation string) error {
	c.logger.Info("📥 Fetching QBO entity",
		"realm_id", realmID,
		"entity_type", entityType,
		"entity_id", entityID,
		"operation", operation,
	)

	// 1. Initialize QBO client
	client, err := c.getClient(ctx, "", realmID)
	if err != nil {
		return err
	}

	// 3. Handle soft deletes
	if operation == "Delete" {
		return c.softDeleteEntity(ctx, realmID, entityType, entityID)
	}

	// 4. Fetch entity from QBO API based on type
	var entityData interface{}
	var err2 error

	switch entityType {
	case "Account":
		qboEntity, err := client.FindAccountById(entityID)
		err2 = err
		if err2 == nil {
			localEntity, dbErr := c.store.Queries.GetAccountByQBOID(ctx, database.GetAccountByQBOIDParams{RealmID: realmID, QboID: entityID})
			if dbErr == nil && shouldSkipSync(localEntity.SyncToken, qboEntity.SyncToken) {
				c.logger.Info("Skipping Echo Event for Account", "entity_id", entityID, "sync_token", qboEntity.SyncToken)
				return nil
			}
			entityData = qboEntity
		}
	case "Vendor":
		qboEntity, err := client.FindVendorById(entityID)
		err2 = err
		if err2 == nil {
			localEntity, dbErr := c.store.Queries.GetVendorByQBOID(ctx, database.GetVendorByQBOIDParams{RealmID: realmID, QboID: entityID})
			if dbErr == nil && shouldSkipSync(localEntity.SyncToken, qboEntity.SyncToken) {
				c.logger.Info("Skipping Echo Event for Vendor", "entity_id", entityID, "sync_token", qboEntity.SyncToken)
				return nil
			}
			entityData = qboEntity
		}
	case "Customer":
		qboEntity, err := client.FindCustomerById(entityID)
		err2 = err
		if err2 == nil {
			localEntity, dbErr := c.store.Queries.GetCustomerByQBOID(ctx, database.GetCustomerByQBOIDParams{RealmID: realmID, QboID: entityID})
			if dbErr == nil && shouldSkipSync(localEntity.SyncToken, qboEntity.SyncToken) {
				c.logger.Info("Skipping Echo Event for Customer", "entity_id", entityID, "sync_token", qboEntity.SyncToken)
				return nil
			}
			entityData = qboEntity
		}
	case "Invoice":
		qboEntity, err := client.FindInvoiceById(entityID)
		err2 = err
		if err2 == nil {
			localEntity, dbErr := c.store.Queries.GetInvoiceByQBOID(ctx, database.GetInvoiceByQBOIDParams{RealmID: realmID, QboID: entityID})
			if dbErr == nil && shouldSkipSync(localEntity.SyncToken, qboEntity.SyncToken) {
				c.logger.Info("Skipping Echo Event for Invoice", "entity_id", entityID, "sync_token", qboEntity.SyncToken)
				return nil
			}
			entityData = qboEntity
		}
	case "Bill":
		qboEntity, err := client.FindBillById(entityID)
		err2 = err
		if err2 == nil {
			localEntity, dbErr := c.store.Queries.GetBillByQBOID(ctx, database.GetBillByQBOIDParams{RealmID: realmID, QboID: entityID})
			if dbErr == nil && shouldSkipSync(localEntity.SyncToken, qboEntity.SyncToken) {
				c.logger.Info("Skipping Echo Event for Bill", "entity_id", entityID, "sync_token", qboEntity.SyncToken)
				return nil
			}
			entityData = qboEntity
		}
	default:
		c.logger.Warn("Unsupported entity type for sync", "entity_type", entityType)
		return nil // Not an error, just not supported yet
	}

	if err2 != nil {
		return fmt.Errorf("failed to fetch %s %s from QBO: %w", entityType, entityID, err2)
	}

	// 5. Upsert to shadow DB
	if err := c.upsertEntity(ctx, realmID, entityType, entityID, entityData); err != nil {
		return err
	}
	return c.updateLastWebhookTime(ctx, realmID, entityType, time.Now())
}

// softDeleteEntity marks an entity as deleted in the shadow DB using sqlc
func (c *QBOConnector) softDeleteEntity(ctx context.Context, realmID, entityType, entityID string) error {
	now := time.Now()
	var err error

	switch entityType {
	case "Account":
		err = c.store.Queries.SoftDeleteAccount(ctx, database.SoftDeleteAccountParams{
			DeletedAt: pgtype.Timestamptz{Time: now, Valid: true},
			RealmID:   realmID,
			QboID:     entityID,
		})
	case "Vendor":
		err = c.store.Queries.SoftDeleteVendor(ctx, database.SoftDeleteVendorParams{
			DeletedAt: pgtype.Timestamptz{Time: now, Valid: true},
			RealmID:   realmID,
			QboID:     entityID,
		})
	case "Customer":
		err = c.store.Queries.SoftDeleteCustomer(ctx, database.SoftDeleteCustomerParams{
			DeletedAt: pgtype.Timestamptz{Time: now, Valid: true},
			RealmID:   realmID,
			QboID:     entityID,
		})
	case "Invoice":
		err = c.store.Queries.SoftDeleteInvoice(ctx, database.SoftDeleteInvoiceParams{
			DeletedAt: pgtype.Timestamptz{Time: now, Valid: true},
			RealmID:   realmID,
			QboID:     entityID,
		})
	case "Bill":
		err = c.store.Queries.SoftDeleteBill(ctx, database.SoftDeleteBillParams{
			DeletedAt: pgtype.Timestamptz{Time: now, Valid: true},
			RealmID:   realmID,
			QboID:     entityID,
		})
	default:
		return fmt.Errorf("unsupported entity type: %s", entityType)
	}

	if err != nil {
		return fmt.Errorf("failed to soft delete %s: %w", entityType, err)
	}

	c.logger.Info("🗑️ Soft deleted entity", "entity_type", entityType, "entity_id", entityID)
	return nil
}

// upsertEntity inserts or updates an entity in the shadow DB using sqlc
func (c *QBOConnector) upsertEntity(ctx context.Context, realmID, entityType, entityID string, data interface{}) error {
	var err error

	switch entityType {
	case "Account":
		acct := data.(*quickbooks.Account)
		err = c.store.Queries.UpsertAccount(ctx, database.UpsertAccountParams{
			QboID:                         acct.Id,
			RealmID:                       realmID,
			Name:                          acct.Name,
			AccountType:                   acct.AccountType,
			AccountSubType:                pgtype.Text{String: acct.AccountSubType, Valid: acct.AccountSubType != ""},
			Classification:                pgtype.Text{String: acct.Classification, Valid: acct.Classification != ""},
			FullyQualifiedName:            pgtype.Text{String: acct.FullyQualifiedName, Valid: acct.FullyQualifiedName != ""},
			Active:                        pgtype.Bool{Bool: acct.Active, Valid: true},
			SyncToken:                     acct.SyncToken,
			Domain:                        pgtype.Text{String: acct.Domain, Valid: acct.Domain != ""},
			CurrencyRefName:               pgtype.Text{String: acct.CurrencyRef.Name, Valid: acct.CurrencyRef.Name != ""},
			CurrencyRefValue:              pgtype.Text{String: acct.CurrencyRef.Value, Valid: acct.CurrencyRef.Value != ""},
			CurrentBalanceWithSubAccounts: jsonNumberToNumeric(acct.CurrentBalanceWithSubAccounts),
			Sparse:                        pgtype.Bool{Bool: acct.Sparse, Valid: true},
			QboCreatedTime:                pgtype.Timestamptz{Time: acct.MetaData.CreateTime.Time, Valid: !acct.MetaData.CreateTime.IsZero()},
			QboUpdatedTime:                pgtype.Timestamptz{Time: acct.MetaData.LastUpdatedTime.Time, Valid: !acct.MetaData.LastUpdatedTime.IsZero()},
			CurrentBalance:                jsonNumberToNumeric(acct.CurrentBalance),
			SubAccount:                    pgtype.Bool{Bool: acct.SubAccount, Valid: true},
		})

	case "Vendor":
		vendor := data.(*quickbooks.Vendor)
		apAcct := vendor.APAccountRef.Value
		err = c.store.Queries.UpsertVendor(ctx, database.UpsertVendorParams{
			QboID:                 vendor.Id,
			RealmID:               realmID,
			DisplayName:           vendor.DisplayName,
			SyncToken:             vendor.SyncToken,
			LastKnownAccountQboID: pgtype.Text{String: apAcct, Valid: apAcct != ""},
			AiSynonyms:            nil,
		})

	case "Customer":
		cust := data.(*quickbooks.Customer)
		err = c.store.Queries.UpsertCustomer(ctx, database.UpsertCustomerParams{
			QboID:       cust.Id,
			RealmID:     realmID,
			DisplayName: cust.DisplayName,
			SyncToken:   cust.SyncToken,
		})

	case "Invoice":
		invoice := data.(*quickbooks.Invoice)
		// Extract customer ID from reference
		customerID := ""
		if invoice.CustomerRef.Value != "" {
			customerID = invoice.CustomerRef.Value
		}

		err = c.store.Queries.UpsertInvoice(ctx, database.UpsertInvoiceParams{
			QboID:         invoice.Id,
			RealmID:       realmID,
			CustomerQboID: pgtype.Text{String: customerID, Valid: customerID != ""},
			DocNumber:     pgtype.Text{String: invoice.DocNumber, Valid: invoice.DocNumber != ""},
			TotalAmount:   jsonNumberToNumeric(invoice.TotalAmt),
			Balance:       jsonNumberToNumeric(invoice.Balance),
			DueDate:       pgtype.Date{Time: invoice.DueDate.Time, Valid: !invoice.DueDate.IsZero()},
			TxnDate:       pgtype.Date{Time: invoice.TxnDate.Time, Valid: !invoice.TxnDate.IsZero()},
			SyncToken:     invoice.SyncToken,
		})

	case "Bill":
		bill := data.(*quickbooks.Bill)
		// Extract vendor ID from reference
		vendorID := ""
		if bill.VendorRef.Value != "" {
			vendorID = bill.VendorRef.Value
		}

		err = c.store.Queries.UpsertBill(ctx, database.UpsertBillParams{
			QboID:       bill.Id,
			RealmID:     realmID,
			VendorQboID: pgtype.Text{String: vendorID, Valid: vendorID != ""},
			DocNumber:   pgtype.Text{String: bill.DocNumber, Valid: bill.DocNumber != ""},
			TotalAmount: jsonNumberToNumeric(bill.TotalAmt),
			Balance:     jsonNumberToNumeric(bill.Balance),
			DueDate:     pgtype.Date{Time: bill.DueDate.Time, Valid: !bill.DueDate.IsZero()},
			TxnDate:     pgtype.Date{Time: bill.TxnDate.Time, Valid: !bill.TxnDate.IsZero()},
			SyncToken:   bill.SyncToken,
		})

	default:
		return fmt.Errorf("unsupported entity type: %s", entityType)
	}

	if err != nil {
		return fmt.Errorf("failed to upsert %s: %w", entityType, err)
	}

	c.logger.Info("💾 Upserted entity to shadow DB", "entity_type", entityType, "entity_id", entityID)
	return nil
}

// getClient initializes a QBO client with tokens from the store and an auto-refresh callback.
func (c *QBOConnector) getClient(ctx context.Context, tenantID, realmID string) (*quickbooks.Client, error) {
	if realmID == "" && tenantID != "" {
		conn, err := c.store.GetQBOConnection(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to get QBO connection for tenant %s: %w", tenantID, err)
		}
		realmID = conn.RealmID
	}

	if realmID == "" {
		return nil, fmt.Errorf("realmID is required")
	}

	accessToken, refreshToken, expiresAt, entityID, err := c.store.GetQBOTokens(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to get QBO tokens for realm %s: %w", realmID, err)
	}

	// Prefer the caller-supplied tenantID; fall back to the entityID stored alongside the tokens.
	effectiveEntityID := tenantID
	if effectiveEntityID == "" {
		effectiveEntityID = entityID
	}

	client, err := quickbooks.NewClient(
		c.cfg.QBOClientID,
		c.cfg.QBOClientSecret,
		realmID,
		c.cfg.QBOIsProduction,
		c.cfg.QBOMinorVersion,
		&quickbooks.BearerToken{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			Expiry:       expiresAt,
		},
		func(token *quickbooks.BearerToken) error {
			c.logger.Info("Auto-refreshed QBO token", "realm_id", realmID)
			return c.store.SaveQBOTokens(ctx, effectiveEntityID, realmID, token.AccessToken, token.RefreshToken, token.Expiry)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize QBO client: %w", err)
	}

	return client, nil
}

// ClientForRealm returns an authenticated QBO client for the given realmID.
// Exposed so that higher-level services (e.g. accounting.TransactionService)
// can obtain a client without being tightly coupled to QBOConnector internals.
func (c *QBOConnector) ClientForRealm(ctx context.Context, realmID string) (*quickbooks.Client, error) {
	return c.getClient(ctx, "", realmID)
}

// SyncCDC performs a CDC (Change Data Capture) sync for the specified realm.
// This fetches all entities that have changed since the last sync and upserts them to the shadow DB.
// Optimization: Uses event-driven timestamps (last successful webhook) per entity type instead of fixed schedule.
func (c *QBOConnector) SyncCDC(ctx context.Context, realmID string, lastSync time.Time) error {
	c.logger.Info("🔄 Running CDC sync", "realm_id", realmID, "fallback_timestamp", lastSync.Format(time.RFC3339))

	// 1. Initialize QBO client
	client, err := c.getClient(ctx, "", realmID)
	if err != nil {
		return err
	}

	// Get connection with webhook timestamps
	conn, err := c.store.Queries.GetConnectionWithWebhookTimes(ctx, realmID)
	if err != nil {
		c.logger.Warn("Failed to get webhook times, using fallback", "error", err)
	}

	// 3. Query CDC per entity type using event-driven timestamps
	// Use last webhook time if available, otherwise fall back to last_sync_timestamp
	maxLookback := time.Now().Add(-30 * 24 * time.Hour) // QBO 30-day limit

	entityTimestamps := map[string]time.Time{
		"Account":  getMaxTime(conn.LastWebhookAccount.Time, lastSync, maxLookback),
		"Vendor":   getMaxTime(conn.LastWebhookVendor.Time, lastSync, maxLookback),
		"Customer": getMaxTime(conn.LastWebhookCustomer.Time, lastSync, maxLookback),
		"Invoice":  getMaxTime(conn.LastWebhookInvoice.Time, lastSync, maxLookback),
		"Bill":     getMaxTime(conn.LastWebhookBill.Time, lastSync, maxLookback),
	}

	// Log event-driven timestamps
	for entityType, timestamp := range entityTimestamps {
		c.logger.Debug("CDC timestamp for entity type",
			"entity_type", entityType,
			"since", timestamp.Format(time.RFC3339),
		)
	}

	var totalProcessed int
	var processingErrors []string

	// Process each entity type with its specific timestamp
	for entityType, changedSince := range entityTimestamps {
		response, err := client.QueryCDC(entityType, changedSince)
		if err != nil {
			c.logger.Error("CDC query failed for entity type", "entity_type", entityType, "error", err)
			processingErrors = append(processingErrors, fmt.Sprintf("%s: %v", entityType, err))
			continue
		}

		// Collect entities from response
		var entities interface{}
		var count int

		for _, cdcEntity := range response.CDCResponse {
			for _, queryResp := range cdcEntity.QueryResponse {
				switch entityType {
				case "Account":
					if len(queryResp.Account) > 0 {
						entities = queryResp.Account
						count = len(queryResp.Account)
					}
				case "Vendor":
					if len(queryResp.Vendor) > 0 {
						entities = queryResp.Vendor
						count = len(queryResp.Vendor)
					}
				case "Customer":
					if len(queryResp.Customer) > 0 {
						entities = queryResp.Customer
						count = len(queryResp.Customer)
					}
				case "Invoice":
					if len(queryResp.Invoice) > 0 {
						entities = queryResp.Invoice
						count = len(queryResp.Invoice)
					}
				case "Bill":
					if len(queryResp.Bill) > 0 {
						entities = queryResp.Bill
						count = len(queryResp.Bill)
					}
				}
			}
		}

		if count == 0 {
			c.logger.Debug("No changes for entity type", "entity_type", entityType)
			continue
		}

		// Batch upsert
		var batchErr error
		switch entityType {
		case "Account":
			batchErr = c.batchUpsertAccounts(ctx, realmID, entities.([]quickbooks.Account))
		case "Vendor":
			batchErr = c.batchUpsertVendors(ctx, realmID, entities.([]quickbooks.Vendor))
		case "Customer":
			batchErr = c.batchUpsertCustomers(ctx, realmID, entities.([]quickbooks.Customer))
		case "Invoice":
			batchErr = c.batchUpsertInvoices(ctx, realmID, entities.([]quickbooks.Invoice))
		case "Bill":
			batchErr = c.batchUpsertBills(ctx, realmID, entities.([]quickbooks.Bill))
		}

		if batchErr != nil {
			c.logger.Error("Failed to batch upsert entities", "entity_type", entityType, "error", batchErr, "count", count)
			processingErrors = append(processingErrors, fmt.Sprintf("%s: %v", entityType, batchErr))
		} else {
			totalProcessed += count
			c.logger.Info("CDC processed entity type", "entity_type", entityType, "count", count)
		}
	}

	// 6. Update last sync timestamp (even if some entities failed)
	if err := c.store.Queries.UpdateLastSyncTimestamp(ctx, database.UpdateLastSyncTimestampParams{
		RealmID:           realmID,
		LastSyncTimestamp: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to update last sync timestamp: %w", err)
	}

	if len(processingErrors) > 0 {
		c.logger.Warn("CDC sync completed with errors",
			"realm_id", realmID,
			"entities_processed", totalProcessed,
			"errors", processingErrors,
		)
	} else {
		c.logger.Info("✅ CDC sync completed",
			"realm_id", realmID,
			"entities_processed", totalProcessed,
		)
	}

	// 7. Trigger Vector Sync (AI Hook)
	if c.vectorWorker != nil {
		c.logger.Info("🤖 Triggering vector sync", "realm_id", realmID)
		go func() {
			// Use a fresh context for background sync
			ctx := context.Background()
			if err := c.vectorWorker.SyncRealm(ctx, realmID); err != nil {
				c.logger.Error("Failed to sync vectors after CDC", "error", err, "realm_id", realmID)
			} else {
				c.logger.Info("🤖 Vector sync completed", "realm_id", realmID)
			}
		}()
	}

	return nil
}

// CreateAccount creates an account in QBO and upserts it to the shadow DB.
func (c *QBOConnector) CreateAccount(ctx context.Context, tenantID, realmID string, account *quickbooks.Account) (*quickbooks.Account, error) {
	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return nil, err
	}

	createdAccount, err := client.CreateAccount(account)
	if err != nil {
		return nil, fmt.Errorf("failed to create account in QBO: %w", err)
	}

	if err := c.upsertEntity(ctx, realmID, "Account", createdAccount.Id, createdAccount); err != nil {
		return nil, fmt.Errorf("failed to upsert created account to shadow db: %w", err)
	}

	return createdAccount, nil
}

// UpdateAccount updates an account in QBO using sparse fields and upserts it to the shadow DB.
func (c *QBOConnector) UpdateAccount(ctx context.Context, tenantID, realmID string, account *quickbooks.Account) (*quickbooks.Account, error) {
	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return nil, err
	}

	updatedAccount, err := client.UpdateAccount(account)
	if err != nil {
		return nil, fmt.Errorf("failed to update account in QBO: %w", err)
	}

	if err := c.upsertEntity(ctx, realmID, "Account", updatedAccount.Id, updatedAccount); err != nil {
		return nil, fmt.Errorf("failed to upsert updated account to shadow db: %w", err)
	}

	return updatedAccount, nil
}

// SoftDeleteAccount soft deletes an account in QBO (sets active to false) and updates the shadow DB.
func (c *QBOConnector) SoftDeleteAccount(ctx context.Context, tenantID, realmID string, id string) (*quickbooks.Account, error) {
	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return nil, err
	}

	deactivatedAccount, err := client.DeactivateAccount(id)
	if err != nil {
		return nil, fmt.Errorf("failed to deactivate account in QBO: %w", err)
	}

	if err := c.upsertEntity(ctx, realmID, "Account", deactivatedAccount.Id, deactivatedAccount); err != nil {
		c.logger.Error("failed to upsert deactivated account to shadow db, using local soft delete", "error", err)
		// Fallback to local soft delete if full upsert fails
		c.softDeleteEntity(ctx, realmID, "Account", id)
	}

	return deactivatedAccount, nil
}

// SyncFullChartOfAccounts performs a full Chart of Accounts sync for the specified realm.
// If realmID is empty, it is resolved from tenantID.
func (c *QBOConnector) SyncFullChartOfAccounts(ctx context.Context, tenantID, realmID string) (int, error) {
	if realmID == "" && tenantID != "" {
		conn, err := c.store.GetQBOConnection(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("failed to get QBO connection for tenant %s: %w", tenantID, err)
		}
		realmID = conn.RealmID
	}

	if realmID == "" {
		return 0, fmt.Errorf("realmID is required")
	}

	c.logger.Info("🔄 Running full CoA sync", "realm_id", realmID, "tenant_id", tenantID)

	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return 0, err
	}

	accounts, err := client.FindAccounts()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch accounts: %w", err)
	}

	if err := c.batchUpsertAccounts(ctx, realmID, accounts); err != nil {
		return 0, fmt.Errorf("failed to upsert accounts: %w", err)
	}

	if err := c.store.Queries.UpdateLastSyncTimestamp(ctx, database.UpdateLastSyncTimestampParams{
		RealmID:           realmID,
		LastSyncTimestamp: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		return 0, fmt.Errorf("failed to update last sync timestamp: %w", err)
	}

	if c.vectorWorker != nil {
		c.logger.Info("🤖 Triggering vector sync after full CoA sync", "realm_id", realmID)
		go func() {
			bgCtx := context.Background()
			if err := c.vectorWorker.SyncRealm(bgCtx, realmID); err != nil {
				c.logger.Error("Failed to sync vectors after full CoA sync", "error", err, "realm_id", realmID)
			}
		}()
	}

	c.logger.Info("✅ Full CoA sync completed", "realm_id", realmID, "count", len(accounts))
	return len(accounts), nil
}

// SyncFullCustomers performs a full Customer sync for the specified realm.
// If realmID is empty, it is resolved from tenantID.
func (c *QBOConnector) SyncFullCustomers(ctx context.Context, tenantID, realmID string) (int, error) {
	if realmID == "" && tenantID != "" {
		conn, err := c.store.GetQBOConnection(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("failed to get QBO connection for tenant %s: %w", tenantID, err)
		}
		realmID = conn.RealmID
	}

	if realmID == "" {
		return 0, fmt.Errorf("realmID is required")
	}

	c.logger.Info("🔄 Running full Customers sync", "realm_id", realmID, "tenant_id", tenantID)

	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return 0, err
	}

	customers, err := client.FindCustomers()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch customers: %w", err)
	}

	if err := c.batchUpsertCustomers(ctx, realmID, customers); err != nil {
		return 0, fmt.Errorf("failed to upsert customers: %w", err)
	}

	if c.vectorWorker != nil {
		c.logger.Info("🤖 Triggering vector sync after full Customers sync", "realm_id", realmID)
		go func() {
			bgCtx := context.Background()
			if err := c.vectorWorker.SyncRealm(bgCtx, realmID); err != nil {
				c.logger.Error("Failed to sync vectors after full Customers sync", "error", err, "realm_id", realmID)
			}
		}()
	}

	c.logger.Info("✅ Full Customers sync completed", "realm_id", realmID, "count", len(customers))
	return len(customers), nil
}

// SyncFullVendors performs a full vendor sync for the specified realm.
// If realmID is empty, it is resolved from tenantID.
func (c *QBOConnector) SyncFullVendors(ctx context.Context, tenantID, realmID string) (int, error) {
	if realmID == "" && tenantID != "" {
		conn, err := c.store.GetQBOConnection(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("failed to get QBO connection for tenant %s: %w", tenantID, err)
		}
		realmID = conn.RealmID
	}

	if realmID == "" {
		return 0, fmt.Errorf("realmID is required")
	}

	c.logger.Info("🔄 Running full vendor sync", "realm_id", realmID, "tenant_id", tenantID)

	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return 0, err
	}

	vendors, err := client.FindVendors()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch vendors: %w", err)
	}

	if err := c.batchUpsertVendors(ctx, realmID, vendors); err != nil {
		return 0, fmt.Errorf("failed to upsert vendors: %w", err)
	}

	if err := c.store.Queries.UpdateLastSyncTimestamp(ctx, database.UpdateLastSyncTimestampParams{
		RealmID:           realmID,
		LastSyncTimestamp: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		return 0, fmt.Errorf("failed to update last sync timestamp: %w", err)
	}

	if c.vectorWorker != nil {
		c.logger.Info("🤖 Triggering vector sync after full vendor sync", "realm_id", realmID)
		go func() {
			bgCtx := context.Background()
			if err := c.vectorWorker.SyncRealm(bgCtx, realmID); err != nil {
				c.logger.Error("Failed to sync vectors after full vendor sync", "error", err, "realm_id", realmID)
			}
		}()
	}

	c.logger.Info("✅ Full vendor sync completed", "realm_id", realmID, "count", len(vendors))
	return len(vendors), nil
}

// SyncCompanyInfo fetches the QBO CompanyInfo for the given realm and upserts it
// into shadow_erp.company_info. It is lightweight (single API call, no pagination)
// and should run at connection time and periodically via CDC.
func (c *QBOConnector) SyncCompanyInfo(ctx context.Context, tenantID, realmID string) error {
	if realmID == "" && tenantID != "" {
		conn, err := c.store.GetQBOConnection(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("failed to get QBO connection for tenant %s: %w", tenantID, err)
		}
		realmID = conn.RealmID
	}
	if realmID == "" {
		return fmt.Errorf("realmID is required")
	}

	client, err := c.getClient(ctx, tenantID, realmID)
	if err != nil {
		return err
	}

	info, err := client.FindCompanyInfoContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch company info from QBO: %w", err)
	}

	// Marshal address and name-value blobs to JSONB
	companyAddrJSON, err := json.Marshal(info.CompanyAddr)
	if err != nil {
		return fmt.Errorf("failed to marshal company_addr: %w", err)
	}
	legalAddrJSON, err := json.Marshal(info.LegalAddr)
	if err != nil {
		return fmt.Errorf("failed to marshal legal_addr: %w", err)
	}
	nameValuesJSON, err := json.Marshal(info.NameValue)
	if err != nil {
		return fmt.Errorf("failed to marshal name_values: %w", err)
	}

	phone := ""
	if info.PrimaryPhone != nil {
		phone = info.PrimaryPhone.FreeFormNumber
	}
	email := ""
	if info.Email != nil {
		email = info.Email.Address
	}
	webAddr := ""
	if info.WebAddr != nil {
		webAddr = info.WebAddr.URI
	}

	params := database.UpsertCompanyInfoParams{
		RealmID:     realmID,
		QboID:       info.Id,
		SyncToken:   info.SyncToken,
		CompanyName: info.CompanyName,
		LegalName:   pgtype.Text{String: info.LegalName, Valid: info.LegalName != ""},
		Domain:      pgtype.Text{String: info.Domain, Valid: info.Domain != ""},
		Country:     pgtype.Text{String: info.Country, Valid: info.Country != ""},
		FiscalYearStartMonth: pgtype.Text{
			String: info.FiscalYearStartMonth,
			Valid:  info.FiscalYearStartMonth != "",
		},
		CompanyStartDate: pgtype.Date{
			Time:  info.CompanyStartDate.Time,
			Valid: !info.CompanyStartDate.IsZero(),
		},
		SupportedLanguages: pgtype.Text{
			String: info.SupportedLanguages,
			Valid:  info.SupportedLanguages != "",
		},
		CompanyAddr:  companyAddrJSON,
		LegalAddr:    legalAddrJSON,
		PrimaryPhone: pgtype.Text{String: phone, Valid: phone != ""},
		Email:        pgtype.Text{String: email, Valid: email != ""},
		WebAddr:      pgtype.Text{String: webAddr, Valid: webAddr != ""},
		NameValues:   nameValuesJSON,
		QboCreatedTime: pgtype.Timestamptz{
			Time:  info.Metadata.CreateTime.Time,
			Valid: info.Metadata != nil && !info.Metadata.CreateTime.IsZero(),
		},
		QboUpdatedTime: pgtype.Timestamptz{
			Time:  info.Metadata.LastUpdatedTime.Time,
			Valid: info.Metadata != nil && !info.Metadata.LastUpdatedTime.IsZero(),
		},
	}

	if err := c.store.Queries.UpsertCompanyInfo(ctx, params); err != nil {
		return fmt.Errorf("failed to upsert company info: %w", err)
	}

	c.logger.Info("✅ Company info synced", "realm_id", realmID, "company", info.CompanyName)
	return nil
}

func jsonNumberToNumeric(n json.Number) pgtype.Numeric {
	s := string(n)
	if s == "" {
		return pgtype.Numeric{Valid: false}
	}
	var num pgtype.Numeric
	_ = num.Scan(s)
	return num
}

// batchUpsertAccounts uses a PostgreSQL transaction to upsert multiple accounts efficiently
func (c *QBOConnector) batchUpsertAccounts(ctx context.Context, realmID string, accounts []quickbooks.Account) error {
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := c.store.Queries.WithTx(tx)

	for _, account := range accounts {
		if err := qtx.UpsertAccount(ctx, database.UpsertAccountParams{
			QboID:                         account.Id,
			RealmID:                       realmID,
			Name:                          account.Name,
			AccountType:                   account.AccountType,
			AccountSubType:                pgtype.Text{String: account.AccountSubType, Valid: account.AccountSubType != ""},
			Classification:                pgtype.Text{String: account.Classification, Valid: account.Classification != ""},
			FullyQualifiedName:            pgtype.Text{String: account.FullyQualifiedName, Valid: account.FullyQualifiedName != ""},
			Active:                        pgtype.Bool{Bool: account.Active, Valid: true},
			SyncToken:                     account.SyncToken,
			Domain:                        pgtype.Text{String: account.Domain, Valid: account.Domain != ""},
			CurrencyRefName:               pgtype.Text{String: account.CurrencyRef.Name, Valid: account.CurrencyRef.Name != ""},
			CurrencyRefValue:              pgtype.Text{String: account.CurrencyRef.Value, Valid: account.CurrencyRef.Value != ""},
			CurrentBalanceWithSubAccounts: jsonNumberToNumeric(account.CurrentBalanceWithSubAccounts),
			Sparse:                        pgtype.Bool{Bool: account.Sparse, Valid: true},
			QboCreatedTime:                pgtype.Timestamptz{Time: account.MetaData.CreateTime.Time, Valid: !account.MetaData.CreateTime.IsZero()},
			QboUpdatedTime:                pgtype.Timestamptz{Time: account.MetaData.LastUpdatedTime.Time, Valid: !account.MetaData.LastUpdatedTime.IsZero()},
			CurrentBalance:                jsonNumberToNumeric(account.CurrentBalance),
			SubAccount:                    pgtype.Bool{Bool: account.SubAccount, Valid: true},
		}); err != nil {
			return fmt.Errorf("failed to upsert account %s: %w", account.Id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Debug("💾 Batch upserted accounts", "count", len(accounts))
	return nil
}

// batchUpsertVendors uses a PostgreSQL transaction to upsert multiple vendors efficiently
func (c *QBOConnector) batchUpsertVendors(ctx context.Context, realmID string, vendors []quickbooks.Vendor) error {
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := c.store.Queries.WithTx(tx)

	for _, vendor := range vendors {
		apAcct := vendor.APAccountRef.Value
		if err := qtx.UpsertVendor(ctx, database.UpsertVendorParams{
			QboID:                 vendor.Id,
			RealmID:               realmID,
			DisplayName:           vendor.DisplayName,
			SyncToken:             vendor.SyncToken,
			LastKnownAccountQboID: pgtype.Text{String: apAcct, Valid: apAcct != ""},
			AiSynonyms:            nil,
		}); err != nil {
			return fmt.Errorf("failed to upsert vendor %s: %w", vendor.Id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Debug("💾 Batch upserted vendors", "count", len(vendors))
	return nil
}

// batchUpsertCustomers uses a PostgreSQL transaction to upsert multiple customers efficiently
func (c *QBOConnector) batchUpsertCustomers(ctx context.Context, realmID string, customers []quickbooks.Customer) error {
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := c.store.Queries.WithTx(tx)

	for _, customer := range customers {
		if err := qtx.UpsertCustomer(ctx, database.UpsertCustomerParams{
			QboID:       customer.Id,
			RealmID:     realmID,
			DisplayName: customer.DisplayName,
			SyncToken:   customer.SyncToken,
		}); err != nil {
			return fmt.Errorf("failed to upsert customer %s: %w", customer.Id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Debug("💾 Batch upserted customers", "count", len(customers))
	return nil
}

// batchUpsertInvoices uses a PostgreSQL transaction to upsert multiple invoices efficiently
func (c *QBOConnector) batchUpsertInvoices(ctx context.Context, realmID string, invoices []quickbooks.Invoice) error {
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := c.store.Queries.WithTx(tx)

	for _, invoice := range invoices {
		customerID := ""
		if invoice.CustomerRef.Value != "" {
			customerID = invoice.CustomerRef.Value
		}

		if err := qtx.UpsertInvoice(ctx, database.UpsertInvoiceParams{
			QboID:         invoice.Id,
			RealmID:       realmID,
			CustomerQboID: pgtype.Text{String: customerID, Valid: customerID != ""},
			DocNumber:     pgtype.Text{String: invoice.DocNumber, Valid: invoice.DocNumber != ""},
			TotalAmount:   jsonNumberToNumeric(invoice.TotalAmt),
			Balance:       jsonNumberToNumeric(invoice.Balance),
			DueDate:       pgtype.Date{Time: invoice.DueDate.Time, Valid: !invoice.DueDate.IsZero()},
			TxnDate:       pgtype.Date{Time: invoice.TxnDate.Time, Valid: !invoice.TxnDate.IsZero()},
			SyncToken:     invoice.SyncToken,
		}); err != nil {
			return fmt.Errorf("failed to upsert invoice %s: %w", invoice.Id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Debug("💾 Batch upserted invoices", "count", len(invoices))
	return nil
}

// batchUpsertBills uses a PostgreSQL transaction to upsert multiple bills efficiently
func (c *QBOConnector) batchUpsertBills(ctx context.Context, realmID string, bills []quickbooks.Bill) error {
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := c.store.Queries.WithTx(tx)

	for _, bill := range bills {
		vendorID := ""
		if bill.VendorRef.Value != "" {
			vendorID = bill.VendorRef.Value
		}

		if err := qtx.UpsertBill(ctx, database.UpsertBillParams{
			QboID:       bill.Id,
			RealmID:     realmID,
			VendorQboID: pgtype.Text{String: vendorID, Valid: vendorID != ""},
			DocNumber:   pgtype.Text{String: bill.DocNumber, Valid: bill.DocNumber != ""},
			TotalAmount: jsonNumberToNumeric(bill.TotalAmt),
			Balance:     jsonNumberToNumeric(bill.Balance),
			DueDate:     pgtype.Date{Time: bill.DueDate.Time, Valid: !bill.DueDate.IsZero()},
			TxnDate:     pgtype.Date{Time: bill.TxnDate.Time, Valid: !bill.TxnDate.IsZero()},
			SyncToken:   bill.SyncToken,
		}); err != nil {
			return fmt.Errorf("failed to upsert bill %s: %w", bill.Id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	c.logger.Debug("💾 Batch upserted bills", "count", len(bills))
	return nil
}

// updateLastWebhookTime updates the last successful webhook timestamp for the given entity type
func (c *QBOConnector) updateLastWebhookTime(ctx context.Context, realmID, entityType string, timestamp time.Time) error {
	ts := pgtype.Timestamptz{Time: timestamp, Valid: true}

	switch entityType {
	case "Account":
		return c.store.Queries.UpdateLastWebhookAccount(ctx, database.UpdateLastWebhookAccountParams{
			RealmID:            realmID,
			LastWebhookAccount: ts,
		})
	case "Vendor":
		return c.store.Queries.UpdateLastWebhookVendor(ctx, database.UpdateLastWebhookVendorParams{
			RealmID:           realmID,
			LastWebhookVendor: ts,
		})
	case "Customer":
		return c.store.Queries.UpdateLastWebhookCustomer(ctx, database.UpdateLastWebhookCustomerParams{
			RealmID:             realmID,
			LastWebhookCustomer: ts,
		})
	case "Invoice":
		return c.store.Queries.UpdateLastWebhookInvoice(ctx, database.UpdateLastWebhookInvoiceParams{
			RealmID:            realmID,
			LastWebhookInvoice: ts,
		})
	case "Bill":
		return c.store.Queries.UpdateLastWebhookBill(ctx, database.UpdateLastWebhookBillParams{
			RealmID:         realmID,
			LastWebhookBill: ts,
		})
	default:
		// Unsupported entity type, silently skip (no error)
		return nil
	}
}

// executeBatched chunks a typed slice into ≤BatchMaxSize groups and fires one
// BatchContext call per chunk. All BatchItemResponse objects are aggregated and
// returned. The first network/API error short-circuits and returns what was
// collected up to that point alongside the error.
func executeBatched[T any](
	ctx context.Context,
	client *quickbooks.Client,
	entityType string,
	entities []T,
	addFn func(*quickbooks.BatchBuilder, T),
) ([]quickbooks.BatchItemResponse, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	var all []quickbooks.BatchItemResponse
	for i := 0; i < len(entities); i += quickbooks.BatchMaxSize {
		end := min(i+quickbooks.BatchMaxSize, len(entities))
		builder := quickbooks.NewBatchBuilder()
		for _, e := range entities[i:end] {
			addFn(builder, e)
		}
		resp, err := client.BatchContext(ctx, builder.Build())
		if err != nil {
			return all, fmt.Errorf("batch chunk [%d:%d] for %s: %w", i, end, entityType, err)
		}
		all = append(all, resp.BatchItemResponse...)
	}
	return all, nil
}

// batchSendToQBO is the shared implementation for create/update batch operations.
// It chunks large entity lists, respects the 40 req/min batch limit via BatchContext,
// and logs any per-item partial failures.
func (c *QBOConnector) batchSendToQBO(
	ctx context.Context,
	realmID, entityType, operation string,
	entities interface{},
) (*quickbooks.BatchResponse, error) {
	client, err := c.getClient(ctx, "", realmID)
	if err != nil {
		return nil, err
	}

	var items []quickbooks.BatchItemResponse

	switch operation {
	case "create":
		switch entityType {
		case "Vendor":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Vendor),
				func(b *quickbooks.BatchBuilder, v quickbooks.Vendor) { b.AddCreate(entityType, v) })
		case "Customer":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Customer),
				func(b *quickbooks.BatchBuilder, cu quickbooks.Customer) { b.AddCreate(entityType, cu) })
		case "Bill":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Bill),
				func(b *quickbooks.BatchBuilder, bi quickbooks.Bill) { b.AddCreate(entityType, bi) })
		case "Invoice":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Invoice),
				func(b *quickbooks.BatchBuilder, inv quickbooks.Invoice) { b.AddCreate(entityType, inv) })
		default:
			return nil, fmt.Errorf("unsupported entity type for batch create: %s", entityType)
		}
	case "update":
		switch entityType {
		case "Vendor":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Vendor),
				func(b *quickbooks.BatchBuilder, v quickbooks.Vendor) { b.AddUpdate(entityType, v) })
		case "Customer":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Customer),
				func(b *quickbooks.BatchBuilder, cu quickbooks.Customer) { b.AddUpdate(entityType, cu) })
		case "Bill":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Bill),
				func(b *quickbooks.BatchBuilder, bi quickbooks.Bill) { b.AddUpdate(entityType, bi) })
		case "Invoice":
			items, err = executeBatched(ctx, client, entityType, entities.([]quickbooks.Invoice),
				func(b *quickbooks.BatchBuilder, inv quickbooks.Invoice) { b.AddUpdate(entityType, inv) })
		default:
			return nil, fmt.Errorf("unsupported entity type for batch update: %s", entityType)
		}
	default:
		return nil, fmt.Errorf("unsupported batch operation: %s", operation)
	}

	if err != nil {
		c.trackRateLimit(client, false)
		return nil, fmt.Errorf("batch %s failed for %s: %w", operation, entityType, err)
	}

	c.trackRateLimit(client, true)

	c.logger.Info("🚀 Batch operation completed",
		"operation", operation,
		"entity_type", entityType,
		"total", len(items),
		"realm_id", realmID,
	)

	var failureCount int
	for _, item := range items {
		if item.HasError() {
			failureCount++
			c.logger.Warn("Batch item failed", "bId", item.BId, "error", item.GetError())
		}
	}
	if failureCount > 0 {
		c.logger.Warn("Batch operation completed with partial failures",
			"operation", operation,
			"entity_type", entityType,
			"total", len(items),
			"failures", failureCount,
		)
	}

	return &quickbooks.BatchResponse{BatchItemResponse: items}, nil
}

// batchCreateToQBO creates multiple entities in QBO using the Batch API.
// Large slices are automatically chunked into ≤30-item batches.
func (c *QBOConnector) batchCreateToQBO(ctx context.Context, realmID, entityType string, entities interface{}) (*quickbooks.BatchResponse, error) {
	return c.batchSendToQBO(ctx, realmID, entityType, "create", entities)
}

// batchUpdateToQBO updates multiple entities in QBO using the Batch API.
// Large slices are automatically chunked into ≤30-item batches.
func (c *QBOConnector) batchUpdateToQBO(ctx context.Context, realmID, entityType string, entities interface{}) (*quickbooks.BatchResponse, error) {
	return c.batchSendToQBO(ctx, realmID, entityType, "update", entities)
}

// trackRateLimit logs rate limit information from the QBO client
// This helps monitor API usage and identify potential throttling issues
func (c *QBOConnector) trackRateLimit(client *quickbooks.Client, success bool) {
	status := "success"
	if !success {
		status = "failed"
	}

	// Check if throttled
	if client.IsThrottled() {
		c.logger.Warn("⚠️ QBO API rate limit hit - request throttled",
			"status", status,
			"throttled", true,
		)
	} else {
		c.logger.Debug("📊 QBO API request tracking",
			"status", status,
			"throttled", false,
		)
	}
}

// getMaxTime returns the most recent timestamp, but not beyond maxLookback.

// If webhookTime is zero (no webhooks received), falls back to fallbackTime.
// Ensures we don't exceed QBO's 30-day lookback limit.
func getMaxTime(webhookTime, fallbackTime, maxLookback time.Time) time.Time {
	var chosen time.Time

	// Use webhook time if available, otherwise use fallback
	if webhookTime.IsZero() {
		chosen = fallbackTime
	} else {
		// Use webhook time if it's more recent than fallback
		if webhookTime.After(fallbackTime) {
			chosen = webhookTime
		} else {
			chosen = fallbackTime
		}
	}

	// Enforce QBO's 30-day lookback limit
	if chosen.Before(maxLookback) {
		chosen = maxLookback
	}

	return chosen
}

// shouldSkipSync parses SyncTokens as integers and returns true if the local token is greater than or equal to the remote token.
// This handles cases where a webhook provides an older or identical state to what we already have.
func shouldSkipSync(localToken, remoteToken string) bool {
	localInt, err1 := strconv.Atoi(localToken)
	remoteInt, err2 := strconv.Atoi(remoteToken)
	if err1 == nil && err2 == nil {
		return localInt >= remoteInt
	}
	// Fallback to string comparison if not parseable as int
	return localToken == remoteToken
}
