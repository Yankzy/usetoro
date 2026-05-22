package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// QboSyncStore is the minimal database interface required by QboSyncWorker.
type QboSyncStore interface {
	GetStagingTransactionsReadyForQBO(context.Context, pgtype.Text) ([]database.FignodeStagingTransaction, error)
	MarkStagingTransactionSynced(context.Context, database.MarkStagingTransactionSyncedParams) error
	MarkStagingTransactionFailed(context.Context, database.MarkStagingTransactionFailedParams) error
	GetCleanupSession(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetAccountByID(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorByID(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerByID(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
}

// QboSyncConnector is the minimal QBO connector interface required by QboSyncWorker.
type QboSyncConnector interface {
	BatchCreateStagingTransactions(context.Context, string, []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error)
}

// batchIDMapping ties a batch request bId back to the staging transaction row
// and the QBO entity type that was hydrated for it.
type batchIDMapping struct {
	TxnID      pgtype.UUID
	EntityType string // "Purchase" or "Deposit"
}

// QboSyncWorker pushes approved staging transactions to QuickBooks Online as
// Deposits (inflow) or Purchases (outflow) via the QBO Batch API.
type QboSyncWorker struct {
	logger    *slog.Logger
	cfg       *config.Config
	nc        *nats.Conn
	db        QboSyncStore
	connector QboSyncConnector
}

// NewQboSyncWorker creates a new QboSyncWorker.
func NewQboSyncWorker(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
	db QboSyncStore,
	connector QboSyncConnector,
) *QboSyncWorker {
	return &QboSyncWorker{
		logger:    logger,
		cfg:       cfg,
		nc:        nc,
		db:        db,
		connector: connector,
	}
}

func (w *QboSyncWorker) Init(ctx context.Context) error {
	return nil
}

func (w *QboSyncWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("qbo_sync worker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		derived, err := core.BuildWorkerInboxFromActivity(activityType)
		if err != nil {
			w.logger.Error("qbo_sync worker: failed to derive inbox",
				"activity_type", activityType, "error", err)
			return nil
		}
		subject = derived
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

// Handle processes a single QBO sync request for a realm.
func (w *QboSyncWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta != nil && meta.NumDelivered > 3 {
		w.logger.Error("qbo_sync worker: poison pill exceeded retries",
			"subject", msg.Subject, "delivered", meta.NumDelivered)
		msg.Term()
		return nil
	}

	realmID, err := w.extractRealmID(ctx, msg.Data)
	if err != nil || realmID == "" {
		w.logger.Warn("qbo_sync worker: could not extract realm_id, ignoring message",
			"error", err)
		return nil
	}

	w.logger.Info("qbo_sync worker: processing realm", "realm_id", realmID)

	var pgRealmID pgtype.Text
	_ = pgRealmID.Scan(realmID)

	rows, err := w.db.GetStagingTransactionsReadyForQBO(ctx, pgRealmID)
	if err != nil {
		return fmt.Errorf("failed to fetch staging transactions: %w", err)
	}

	if len(rows) == 0 {
		w.logger.Info("qbo_sync worker: no ready transactions", "realm_id", realmID)
		return nil
	}

	batchItems, itemMap, hydrateErrors := w.hydrateBatchItems(ctx, realmID, rows)

	for txnID, errMsg := range hydrateErrors {
		_ = w.db.MarkStagingTransactionFailed(ctx, database.MarkStagingTransactionFailedParams{
			ID:           txnID,
			ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		})
	}
	if len(hydrateErrors) > 0 {
		w.logger.Warn("qbo_sync worker: some rows failed hydration",
			"realm_id", realmID, "hydration_failures", len(hydrateErrors))
	}

	if len(batchItems) == 0 {
		return nil
	}

	responses, err := w.connector.BatchCreateStagingTransactions(ctx, realmID, batchItems)
	if err != nil {
		w.logger.Error("qbo_sync worker: batch push fatal", "realm_id", realmID, "error", err)
		for _, item := range batchItems {
			if mapping, ok := itemMap[item.BId]; ok {
				_ = w.db.MarkStagingTransactionFailed(ctx, database.MarkStagingTransactionFailedParams{
					ID:           mapping.TxnID,
					ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
				})
			}
		}
		if isOAuthRevoked(err) {
			return nil
		}
		return fmt.Errorf("batch push fatal: %w", err)
	}

	w.processBatchResponses(ctx, responses, itemMap, realmID)
	return nil
}

// extractRealmID pulls the realm_id from the incoming NATS message.
func (w *QboSyncWorker) extractRealmID(ctx context.Context, data []byte) (string, error) {
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		RealmID   string `json:"realm_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if payload.RealmID != "" {
		return payload.RealmID, nil
	}
	if payload.SessionID != "" {
		var pgSessionID pgtype.UUID
		if err := pgSessionID.Scan(payload.SessionID); err != nil {
			return "", fmt.Errorf("invalid session_id: %w", err)
		}
		session, err := w.db.GetCleanupSession(ctx, pgSessionID)
		if err != nil {
			return "", fmt.Errorf("session lookup failed: %w", err)
		}
		return session.RealmID.String, nil
	}
	return "", fmt.Errorf("no realm_id or session_id in payload")
}

// hydrateBatchItems converts staging transactions into QBO batch items.
func (w *QboSyncWorker) hydrateBatchItems(
	ctx context.Context,
	realmID string,
	rows []database.FignodeStagingTransaction,
) ([]quickbooks.BatchItemRequest, map[string]batchIDMapping, map[pgtype.UUID]string) {
	var items []quickbooks.BatchItemRequest
	itemMap := make(map[string]batchIDMapping)
	failures := make(map[pgtype.UUID]string)

	paymentErpID, payErr := w.resolvePaymentAccount(ctx, realmID, rows)
	if payErr != nil {
		for _, row := range rows {
			failures[row.ID] = payErr.Error()
		}
		return items, itemMap, failures
	}

	for _, row := range rows {
		if row.CashDirection.String == "" {
			failures[row.ID] = "missing cash_direction"
			continue
		}

		entityErpID, entityErr := w.resolveEntityERPID(ctx, realmID, row)
		if entityErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve entity ERP ID: %v", entityErr)
			continue
		}

		accountErpID, acctErr := w.resolveAccountERPID(ctx, realmID, row)
		if acctErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve account ERP ID: %v", acctErr)
			continue
		}

		amount, amtErr := parseAmount(row.RawAmount)
		if amtErr != nil {
			failures[row.ID] = fmt.Sprintf("invalid amount %q: %v", row.RawAmount, amtErr)
			continue
		}

		txnDate := parseTxnDate(row.ParsedDate)
		bID := uuid.UUID(row.ID.Bytes).String()

		switch strings.ToUpper(row.CashDirection.String) {
		case "INFLOW":
			deposit := quickbooks.Deposit{
				DepositToAccountRef: &quickbooks.ReferenceType{Value: paymentErpID},
				TxnDate:             txnDate,
				TotalAmt:            json.Number(strconv.FormatFloat(amount, 'f', 2, 64)),
				PrivateNote:         "System Trace: Processed via AI Booking Automation Pipeline v1.0.",
				Line: []quickbooks.DepositLine{
					{
						Amount:        json.Number(strconv.FormatFloat(amount, 'f', 2, 64)),
						DetailType:    "DepositLineDetail",
						Description:   formatLineDescription(row.AiReasoning),
						DepositLineDetail: &quickbooks.DepositLineDetail{
							Entity:     &quickbooks.ReferenceType{Value: entityErpID},
							AccountRef: &quickbooks.ReferenceType{Value: accountErpID},
						},
					},
				},
			}
			items = append(items, quickbooks.BatchItemRequest{
				BId:       bID,
				Operation: "create",
				Entity:    "Deposit",
				Payload:   deposit,
			})
			itemMap[bID] = batchIDMapping{TxnID: row.ID, EntityType: "Deposit"}

		case "OUTFLOW":
			absAmt := math.Abs(amount)
			purchase := quickbooks.Purchase{
				PaymentType: "Cash",
				AccountRef:  quickbooks.ReferenceType{Value: paymentErpID},
				EntityRef:   quickbooks.ReferenceType{Value: entityErpID},
				TxnDate:     txnDate,
				TotalAmt:    json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
				PrivateNote: "System Trace: Processed via AI Booking Automation Pipeline v1.0.",
				Line: []quickbooks.Line{
					{
						DetailType: "AccountBasedExpenseLineDetail",
						Amount:     json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
						Description: formatLineDescription(row.AiReasoning),
						AccountBasedExpenseLineDetail: quickbooks.AccountBasedExpenseLineDetail{
							AccountRef: quickbooks.ReferenceType{Value: accountErpID},
						},
					},
				},
			}
			items = append(items, quickbooks.BatchItemRequest{
				BId:       bID,
				Operation: "create",
				Entity:    "Purchase",
				Payload:   purchase,
			})
			itemMap[bID] = batchIDMapping{TxnID: row.ID, EntityType: "Purchase"}

		default:
			failures[row.ID] = fmt.Sprintf("unknown cash_direction: %s", row.CashDirection.String)
		}
	}

	return items, itemMap, failures
}

// resolvePaymentAccount finds the QBO ERP ID of the payment account for this realm.
// Resolves from the session's bank_account_id.
func (w *QboSyncWorker) resolvePaymentAccount(
	ctx context.Context,
	realmID string,
	rows []database.FignodeStagingTransaction,
) (string, error) {
	if len(rows) > 0 && rows[0].SessionID.Valid {
		session, err := w.db.GetCleanupSession(ctx, rows[0].SessionID)
		if err == nil && session.BankAccountID.Valid {
			acct, acctErr := w.db.GetAccountByID(ctx, session.BankAccountID)
			if acctErr == nil && acct.ErpID != "" {
				return acct.ErpID, nil
			}
		}
	}
	return "", fmt.Errorf("no payment account found for realm %s", realmID)
}

// resolveEntityERPID returns the QBO ERP ID for the entity (vendor for outflow, customer for inflow).
func (w *QboSyncWorker) resolveEntityERPID(
	ctx context.Context,
	realmID string,
	row database.FignodeStagingTransaction,
) (string, error) {
	isOutflow := strings.ToUpper(row.CashDirection.String) == "OUTFLOW"

	if isOutflow {
		entityID := row.PredictedVendorID
		if row.OverrideVendorID.Valid {
			entityID = row.OverrideVendorID
		}
		if !entityID.Valid {
			return "", fmt.Errorf("no vendor assigned to outflow transaction")
		}
		vendor, err := w.db.GetVendorByID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("vendor lookup failed: %w", err)
		}
		if vendor.ErpID == "" {
			return "", fmt.Errorf("vendor %s has no ERP ID", vendor.DisplayName)
		}
		return vendor.ErpID, nil
	}

	entityID := row.PredictedCustomerID
	if row.OverrideCustomerID.Valid {
		entityID = row.OverrideCustomerID
	}
	if !entityID.Valid {
		return "", fmt.Errorf("no customer assigned to inflow transaction")
	}
	customer, err := w.db.GetCustomerByID(ctx, entityID)
	if err != nil {
		return "", fmt.Errorf("customer lookup failed: %w", err)
	}
	if customer.ErpID == "" {
		return "", fmt.Errorf("customer %s has no ERP ID", customer.DisplayName)
	}
	return customer.ErpID, nil
}

// resolveAccountERPID returns the QBO ERP ID for the expense/revenue account.
func (w *QboSyncWorker) resolveAccountERPID(
	ctx context.Context,
	realmID string,
	row database.FignodeStagingTransaction,
) (string, error) {
	accountID := row.PredictedAccountID
	if row.OverrideAccountID.Valid {
		accountID = row.OverrideAccountID
	}
	if !accountID.Valid {
		return "", fmt.Errorf("no account assigned")
	}
	acct, err := w.db.GetAccountByID(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("account lookup failed: %w", err)
	}
	if acct.ErpID == "" {
		return "", fmt.Errorf("account %s has no ERP ID", acct.Name)
	}
	return acct.ErpID, nil
}

// processBatchResponses iterates through batch responses and marks DB rows synced or failed.
func (w *QboSyncWorker) processBatchResponses(
	ctx context.Context,
	responses []quickbooks.BatchItemResponse,
	itemMap map[string]batchIDMapping,
	realmID string,
) {
	var syncCount, failCount int

	for _, resp := range responses {
		mapping, ok := itemMap[resp.BId]
		if !ok {
			w.logger.Warn("qbo_sync worker: unrecognized bId in batch response",
				"bId", resp.BId)
			continue
		}

		if resp.HasError() {
			errMsg := resp.GetError()
			_ = w.db.MarkStagingTransactionFailed(ctx, database.MarkStagingTransactionFailedParams{
				ID:           mapping.TxnID,
				ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
			})
			failCount++
			w.logger.Warn("qbo_sync worker: batch item failed",
				"bId", resp.BId, "error", errMsg)
			continue
		}

		var erpID string
		switch mapping.EntityType {
		case "Deposit":
			if resp.Deposit != nil {
				erpID = resp.Deposit.Id
			}
		case "Purchase":
			if resp.Purchase != nil {
				erpID = resp.Purchase.Id
			}
		}

		if erpID == "" {
			_ = w.db.MarkStagingTransactionFailed(ctx, database.MarkStagingTransactionFailedParams{
				ID:           mapping.TxnID,
				ErrorMessage: pgtype.Text{String: "QBO returned no entity ID", Valid: true},
			})
			failCount++
			continue
		}

		_ = w.db.MarkStagingTransactionSynced(ctx, database.MarkStagingTransactionSyncedParams{
			ID:               mapping.TxnID,
			ErpTransactionID: pgtype.Text{String: erpID, Valid: true},
		})
		syncCount++
	}

	w.logger.Info("qbo_sync worker: batch complete",
		"realm_id", realmID,
		"synced", syncCount,
		"failed", failCount,
		"total_responses", len(responses),
	)
}

func parseAmount(raw string) (float64, error) {
	cleaned := strings.ReplaceAll(raw, ",", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0, fmt.Errorf("empty amount")
	}
	return strconv.ParseFloat(cleaned, 64)
}

func parseTxnDate(d pgtype.Date) quickbooks.Date {
	if !d.Valid {
		return quickbooks.Date{Time: time.Now()}
	}
	return quickbooks.Date{Time: d.Time}
}

// formatLineDescription surfaces the AI reasoning as a human-readable line item
// description inside the QBO entity so CPAs can see the categorization rationale
// directly in the general ledger without switching back to the Toro UI.
func formatLineDescription(reasoning pgtype.Text) string {
	if !reasoning.Valid || reasoning.String == "" {
		return ""
	}
	return reasoning.String
}

func isOAuthRevoked(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid_grant")
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.QBOConnector == nil {
			deps.Logger.Warn("qbo_sync worker: QBOConnector not available, skipping registration")
			return nil, nil
		}
		return NewQboSyncWorker(deps.Logger, deps.Config, deps.Queue, deps.Store.Queries, deps.QBOConnector), nil
	})
}
