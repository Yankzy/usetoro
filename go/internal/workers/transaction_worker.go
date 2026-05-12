package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

// TransactionWorker handles processing of ProposedTransactions in the background.
// It categorizes newly inserted transactions, and syncs queued transactions to the ERP.
type TransactionWorker struct {
	logger         *slog.Logger
	nc             *nats.Conn
	js             nats.JetStreamContext
	dbPool         *pgxpool.Pool
	queries        *database.Queries
	entityResolver *ai.EntityResolver
	coaMapper      *ai.CoAMapper
	ruleEngine     *accounting.RuleEngineService
	factory        erp.ProviderFactory
	cfg            *config.Config
}

func NewTransactionWorker(
	logger *slog.Logger,
	nc *nats.Conn,
	dbPool *pgxpool.Pool,
	queries *database.Queries,
	resolver *ai.EntityResolver,
	coa *ai.CoAMapper,
	ruleEngine *accounting.RuleEngineService,
	factory erp.ProviderFactory,
	cfg *config.Config,
) (*TransactionWorker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &TransactionWorker{
		logger:         logger,
		nc:             nc,
		js:             js,
		dbPool:         dbPool,
		queries:        queries,
		entityResolver: resolver,
		coaMapper:      coa,
		ruleEngine:     ruleEngine,
		factory:        factory,
		cfg:            cfg,
	}, nil
}

func (w *TransactionWorker) Init(ctx context.Context) error {
	return nil
}

func (w *TransactionWorker) Subscriptions() []SubscriptionConfig {
	if w.cfg == nil {
		w.logger.Error("transaction worker: missing config")
		return nil
	}
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		w.logger.Error("transaction worker: transaction subject not configured")
		return nil
	}
	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}
	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{nats.Durable(durableFromSubject(subject)), nats.ManualAck(), nats.BindStream("LEDGER")},
		},
	}
}

func (w *TransactionWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.handleEvent(ctx, msg)
	return nil
}

func (w *TransactionWorker) handleEvent(ctx context.Context, msg *nats.Msg) {
	var event cdc.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("Failed to parse CDC Event JSON", "error", err)
		msg.Term()
		return
	}

	idStr, ok := event.Data["id"].(string)
	if !ok {
		msg.Ack()
		return
	}

	uid, err := uuid.Parse(idStr)
	if err != nil {
		w.logger.Error("Invalid proposed tx ID", "id", idStr)
		msg.Ack()
		return
	}
	var txID pgtype.UUID
	txID.Bytes = uid
	txID.Valid = true

	status, _ := event.Data["sync_status"].(string)

	tx, err := w.queries.GetProposedTransactionByID(ctx, txID)
	if err != nil {
		// Possibly didn't commit yet or deleted
		w.logger.Warn("ProposedTransaction not found, skipping", "id", idStr)
		msg.Ack()
		return
	}

	// Realm now lives on the parent session, not on the staging transaction.
	var realmID string
	if tx.SessionID.Valid {
		if sess, sErr := w.queries.GetCleanupSession(ctx, tx.SessionID); sErr == nil {
			realmID = sess.RealmID.String
		}
	}
	if realmID == "" {
		w.logger.Warn("ProposedTransaction has no resolvable realm; skipping", "id", idStr)
		msg.Ack()
		return
	}

	switch status {
	case "PENDING_CLASSIFICATION":
		w.categorizeTransaction(ctx, tx, realmID)
		msg.Ack()
	case "PENDING_SYNC":
		w.syncToERP(ctx, tx, realmID)
		msg.Ack()
	default:
		// Nothing to do for this status
		msg.Ack()
	}
}

func (w *TransactionWorker) categorizeTransaction(ctx context.Context, tx database.FignodeStagingTransaction, realmID string) {
	w.logger.Info("Categorizing transaction", "id", tx.ID)

	var vendorID, accountID string
	description := tx.RawDescription.String

	// 1. Entity Resolver
	if w.entityResolver != nil && description != "" {
		match, err := w.entityResolver.ResolveEntity(ctx, realmID, "vendor", description)
		if err == nil && match != nil {
			vendorID = match.ID
		}
	}

	// 2. CoA Mapper
	if w.coaMapper != nil && description != "" {
		matches, err := w.coaMapper.MapDescriptionToAccount(ctx, realmID, description, 1)
		if err == nil && len(matches) > 0 {
			accountID = matches[0].AccountID
		}
	}

	var vendorPgUUID pgtype.UUID
	if vendorID != "" {
		if v, err := w.queries.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{RealmID: realmID, ErpID: vendorID}); err == nil {
			vendorPgUUID = v.ID
		}
	}

	var accountPgUUID pgtype.UUID
	if accountID != "" {
		if a, err := w.queries.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{RealmID: realmID, ErpID: accountID}); err == nil {
			accountPgUUID = a.ID
		}
	}

	reason := "AI classification complete"
	status := "PENDING_SYNC"

	if !vendorPgUUID.Valid && tx.SourceType == "Bill" {
		status = "ERROR"
		reason = "Vendor missing required for Bill"
	} else if !accountPgUUID.Valid {
		status = "ERROR"
		reason = "Account missing required for syncing"
	}

	_, updErr := w.dbPool.Exec(ctx, `
		UPDATE shadow_erp.proposed_transactions
		SET predicted_vendor_id = $2, predicted_account_id = $3, sync_status = $4, ai_reasoning = $5, updated_at = NOW()
		WHERE id = $1
	`, tx.ID, vendorPgUUID, accountPgUUID, status, reason)
	if updErr != nil {
		w.logger.Error("Failed to update classified proposed transaction", "error", updErr)
	} else {
		w.logger.Info("Transaction categorized", "id", tx.ID, "status", status)
	}
}

func (w *TransactionWorker) syncToERP(ctx context.Context, tx database.FignodeStagingTransaction, realmID string) {
	w.logger.Info("Syncing transaction to ERP", "id", tx.ID)

	provider, err := w.factory.GetProviderForRealm(ctx, "quickbooks_online", realmID)
	if err != nil {
		w.markError(ctx, tx.ID, err.Error())
		return
	}

	// Resolve ERP IDs for the vendor and account
	var vendorErpID, accountErpID string
	if tx.PredictedVendorID.Valid {
		if v, err := w.queries.GetVendor(ctx, database.GetVendorParams{RealmID: realmID, ID: tx.PredictedVendorID}); err == nil {
			vendorErpID = v.ErpID
		}
	}
	if tx.PredictedAccountID.Valid {
		if a, err := w.queries.GetAccountByID(ctx, tx.PredictedAccountID); err == nil {
			accountErpID = a.ErpID
		}
	}

	var amt float64
	cleaned := strings.ReplaceAll(tx.RawAmount, "*", "")
	cleaned = strings.TrimSpace(cleaned)
	if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
		amt = f
	}

	input := erp.ExpenseInput{
		RealmID:     realmID,
		Amount:      amt,
		TxnDate:     tx.RawDate.Time,
		Description: tx.RawDescription.String,
		VendorHint:  vendorErpID,
		AccountHint: accountErpID,
		Paid:        tx.SourceType == "Purchase",
	}

	created, err := provider.PostExpense(ctx, input)
	if err != nil {
		w.markError(ctx, tx.ID, err.Error())
		return
	}

	if created != nil {
		w.queries.UpdateProposedTransactionSyncStatus(ctx, database.UpdateProposedTransactionSyncStatusParams{
			ID:               tx.ID,
			Status:           "SYNCED",
			ErpTransactionID: pgtype.Text{String: created.ERPEntityID, Valid: true},
		})
		w.logger.Info("✅ Synced transaction to ERP", "id", tx.ID, "erp_id", created.ERPEntityID)
	}
}

func (w *TransactionWorker) markError(ctx context.Context, txID pgtype.UUID, errMsg string) {
	w.queries.UpdateProposedTransactionSyncStatus(ctx, database.UpdateProposedTransactionSyncStatusParams{
		ID:           txID,
		Status:       "ERROR",
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
	})
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewTransactionWorker(deps.Logger, deps.Queue, deps.DBPool, deps.Store.Queries, deps.EntityResolver, deps.CoAMapper, deps.RuleEngine, deps.ProviderFactory, deps.Config)
	})
}
