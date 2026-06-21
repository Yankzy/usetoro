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
	GetStagingTransactionsReadyForQBO(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	MarkStagingTransactionSynced(context.Context, database.MarkStagingTransactionSyncedParams) error
	MarkStagingTransactionFailed(context.Context, database.MarkStagingTransactionFailedParams) error
	GetCleanupSession(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetAccountByID(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorByID(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerByID(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
	MarkStagingTransactionTransferHold(context.Context, pgtype.UUID) error
	GetAccountsByRealm(context.Context, string) ([]database.ShadowErpAccount, error)
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

	pgSessionID, session, err := w.extractSessionAndRealm(ctx, msg.Data)
	if err != nil {
		w.logger.Warn("qbo_sync worker: could not extract session_id, ignoring message",
			"error", err)
		return nil
	}
	realmID := session.RealmID.String

	w.logger.Info("qbo_sync worker: processing session", "session_id", uuid.UUID(pgSessionID.Bytes).String(), "realm_id", realmID)

	rows, err := w.db.GetStagingTransactionsReadyForQBO(ctx, pgSessionID)
	if err != nil {
		return fmt.Errorf("failed to fetch staging transactions: %w", err)
	}

	if len(rows) == 0 {
		w.logger.Info("qbo_sync worker: no ready transactions", "session_id", uuid.UUID(pgSessionID.Bytes).String())
		return nil
	}

	if !session.BankAccountID.Valid {
		return fmt.Errorf("session has no bank account assigned")
	}
	acct, acctErr := w.db.GetAccountByID(ctx, session.BankAccountID)
	if acctErr != nil {
		return fmt.Errorf("failed to lookup session bank account: %w", acctErr)
	}
	if acct.ErpID == "" {
		return fmt.Errorf("payment account %q has no ERP ID", acct.Name)
	}

	paymentAccountErpID := acct.ErpID
	isCreditCard := acct.AccountType == "Credit Card"

	batchItems, itemMap, hydrateErrors := w.hydrateBatchItems(ctx, realmID, rows, paymentAccountErpID, isCreditCard)

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
		if isOAuthRevoked(err) {
			return nil
		}
		// Return error so NATS can Nak and retry later. Do not mark rows as FAILED.
		return fmt.Errorf("batch push fatal: %w", err)
	}

	w.processBatchResponses(ctx, responses, itemMap, realmID)
	return nil
}

// QboSyncWorkerPayload defines the expected JSON payload.
type QboSyncWorkerPayload struct {
	SessionID string `json:"session_id" desc:"The ID of the cleanup session to sync with QBO"`
}

// ToolName returns the unique LLM tool name for this worker.
func (w *QboSyncWorker) ToolName() string {
	return "TriggerQBOSync"
}

// ToolDescription provides the context for the LLM.
func (w *QboSyncWorker) ToolDescription() string {
	return "Triggers the QuickBooks Online Sync worker asynchronously to push approved staging transactions to QBO for a specific session."
}

// PayloadStruct returns a typed instance to automatically generate a JSON schema.
func (w *QboSyncWorker) PayloadStruct() any {
	return QboSyncWorkerPayload{}
}

// extractSessionAndRealm pulls the session_id from the incoming NATS message and looks up the session.
func (w *QboSyncWorker) extractSessionAndRealm(ctx context.Context, data []byte) (pgtype.UUID, database.GetCleanupSessionRow, error) {
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload QboSyncWorkerPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return pgtype.UUID{}, database.GetCleanupSessionRow{}, err
	}
	if payload.SessionID == "" {
		return pgtype.UUID{}, database.GetCleanupSessionRow{}, fmt.Errorf("no session_id in payload")
	}

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(payload.SessionID); err != nil {
		return pgtype.UUID{}, database.GetCleanupSessionRow{}, fmt.Errorf("invalid session_id: %w", err)
	}

	session, err := w.db.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		return pgtype.UUID{}, database.GetCleanupSessionRow{}, fmt.Errorf("session lookup failed: %w", err)
	}
	return pgSessionID, session, nil
}

// hydrateBatchItems converts staging transactions into QBO batch items.
func (w *QboSyncWorker) hydrateBatchItems(
	ctx context.Context,
	realmID string,
	rows []database.FignodeStagingTransaction,
	paymentErpID string,
	isCreditCard bool,
) ([]quickbooks.BatchItemRequest, map[string]batchIDMapping, map[pgtype.UUID]string) {
	var items []quickbooks.BatchItemRequest
	itemMap := make(map[string]batchIDMapping)
	failures := make(map[pgtype.UUID]string)

	ccAccounts := make([]string, 0)
	allAccounts, err := w.db.GetAccountsByRealm(ctx, realmID)
	if err == nil {
		for _, a := range allAccounts {
			if a.AccountType == "Credit Card" {
				ccAccounts = append(ccAccounts, strings.ToLower(a.Name))
			}
		}
	}

	for _, row := range rows {
		if row.CashDirection.String == "" {
			failures[row.ID] = "missing cash_direction"
			continue
		}

		accountObj, acctErr := w.resolveAccountERPID(ctx, realmID, row)
		if acctErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve account ERP ID: %v", acctErr)
			continue
		}
		accountErpID := accountObj.ErpID

		amount, amtErr := parseAmount(row.RawAmount)
		if amtErr != nil {
			failures[row.ID] = fmt.Sprintf("invalid amount %q: %v", row.RawAmount, amtErr)
			continue
		}

		txnDate := parseTxnDate(row.ParsedDate)
		bID := uuid.UUID(row.ID.Bytes).String()
		cashDir := strings.ToUpper(row.CashDirection.String)
		isTransfer := strings.ToUpper(row.MacroClass.String) == "TRANSFER"

		if cashDir == "OUTFLOW" {
			desc := strings.ToLower(row.RawDescription.String)
			isCreditCardTarget := (isTransfer && accountObj.AccountType == "Credit Card")
			if !isCreditCardTarget {
				for _, ccName := range ccAccounts {
					if strings.Contains(desc, ccName) {
						isCreditCardTarget = true
						break
					}
				}
			}

			if isCreditCardTarget {
				if holdErr := w.db.MarkStagingTransactionTransferHold(ctx, row.ID); holdErr != nil {
					w.logger.Error("failed to mark transfer hold", "txn_id", row.ID, "error", holdErr)
				}
				continue
			}
		}
		if isTransfer {
			var fromAccount, toAccount string
			if cashDir == "OUTFLOW" {
				fromAccount = paymentErpID
				toAccount = accountErpID
			} else if cashDir == "INFLOW" {
				fromAccount = accountErpID
				toAccount = paymentErpID
			} else {
				failures[row.ID] = fmt.Sprintf("unknown cash_direction for transfer: %s", row.CashDirection.String)
				continue
			}

			transfer := constructTransfer(amount, txnDate, fromAccount, toAccount, row.AiReasoning)
			items = append(items, quickbooks.BatchItemRequest{
				BId:       bID,
				Operation: "create",
				Entity:    "Transfer",
				Payload:   transfer,
			})
			itemMap[bID] = batchIDMapping{TxnID: row.ID, EntityType: "Transfer"}
			continue
		}

		entityErpID, entityErr := w.resolveEntityERPID(ctx, realmID, row)
		if entityErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve entity ERP ID: %v", entityErr)
			continue
		}

		switch cashDir {
		case "INFLOW":
			if isCreditCard {
				purchase := constructPurchase(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String, isCreditCard, true)
				items = append(items, quickbooks.BatchItemRequest{
					BId:       bID,
					Operation: "create",
					Entity:    "Purchase",
					Payload:   purchase,
				})
				itemMap[bID] = batchIDMapping{TxnID: row.ID, EntityType: "Purchase"}
			} else {
				typ := accountObj.AccountType
				sub := accountObj.AccountSubType.String

				if typ == "Accounts Receivable" {
					failures[row.ID] = "Direct deposits cannot hit AR. Please map to an Income account."
					continue
				}
				if typ == "Accounts Payable" {
					failures[row.ID] = "Direct deposits cannot hit AP. Vendor refunds must target Expense accounts."
					continue
				}
				if typ == "Other Current Asset" && sub == "UndepositedFunds" {
					failures[row.ID] = "Clearing account bypassed. Map merchant payouts directly to Income."
					continue
				}
				if typ == "Equity" && sub == "RetainedEarnings" {
					failures[row.ID] = "System Block: Intuit prohibits direct equity entries to Retained Earnings."
					continue
				}

				deposit := constructDeposit(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String)
				items = append(items, quickbooks.BatchItemRequest{
					BId:       bID,
					Operation: "create",
					Entity:    "Deposit",
					Payload:   deposit,
				})
				itemMap[bID] = batchIDMapping{TxnID: row.ID, EntityType: "Deposit"}
			}

		case "OUTFLOW":
			purchase := constructPurchase(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String, isCreditCard, false)
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

// resolveEntityERPID returns the QBO ERP ID for the entity (vendor for outflow, customer for inflow).
func (w *QboSyncWorker) resolveEntityERPID(
	ctx context.Context,
	realmID string,
	row database.FignodeStagingTransaction,
) (string, error) {
	if row.OverrideVendorID.Valid || row.PredictedVendorID.Valid {
		entityID := row.PredictedVendorID
		if row.OverrideVendorID.Valid {
			entityID = row.OverrideVendorID
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

	if row.OverrideCustomerID.Valid || row.PredictedCustomerID.Valid {
		entityID := row.PredictedCustomerID
		if row.OverrideCustomerID.Valid {
			entityID = row.OverrideCustomerID
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

	return "", fmt.Errorf("no entity assigned")
}

// resolveAccountERPID returns the QBO ERP ID for the expense/revenue account.
func (w *QboSyncWorker) resolveAccountERPID(
	ctx context.Context,
	realmID string,
	row database.FignodeStagingTransaction,
) (database.ShadowErpAccount, error) {
	accountID := row.PredictedAccountID
	if row.OverrideAccountID.Valid {
		accountID = row.OverrideAccountID
	}
	if !accountID.Valid {
		return database.ShadowErpAccount{}, fmt.Errorf("no account assigned")
	}
	acct, err := w.db.GetAccountByID(ctx, accountID)
	if err != nil {
		return database.ShadowErpAccount{}, fmt.Errorf("account lookup failed: %w", err)
	}
	if acct.ErpID == "" {
		return database.ShadowErpAccount{}, fmt.Errorf("account %s has no ERP ID", acct.Name)
	}
	return acct, nil
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
		case "Transfer":
			if resp.Transfer != nil {
				erpID = resp.Transfer.Id
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
	return "AI Reasoning: " + reasoning.String
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

// constructPurchase separates QBO Purchase payload construction for testability and clean layout
func constructPurchase(
	amount float64,
	txnDate quickbooks.Date,
	paymentErpID string,
	entityErpID string,
	accountErpID string,
	aiReasoning pgtype.Text,
	macroClass string,
	isCreditCard bool,
	isCredit bool,
) quickbooks.Purchase {
	paymentType := "Cash"
	if isCreditCard {
		paymentType = "CreditCard"
	}

	absAmt := math.Abs(amount)

	note := "System Trace: Auto-stratified via elements worker batch session."
	if macroClass == "Asset" || macroClass == "Liability" || macroClass == "Equity" {
		note += fmt.Sprintf(" | System Trace: Balance Sheet allocation targeting %s account.", macroClass)
	}

	return quickbooks.Purchase{
		PaymentType: paymentType,
		Credit:      isCredit,
		AccountRef:  quickbooks.ReferenceType{Value: paymentErpID},
		EntityRef:   quickbooks.ReferenceType{Value: entityErpID},
		TxnDate:     txnDate,
		TotalAmt:    json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		PrivateNote: note,
		Line: []quickbooks.Line{
			{
				DetailType:  "AccountBasedExpenseLineDetail",
				Amount:      json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
				Description: formatLineDescription(aiReasoning),
				AccountBasedExpenseLineDetail: quickbooks.AccountBasedExpenseLineDetail{
					AccountRef: quickbooks.ReferenceType{Value: accountErpID},
				},
			},
		},
	}
}

// constructDeposit separates QBO Deposit payload construction for testability and clean layout
func constructDeposit(
	amount float64,
	txnDate quickbooks.Date,
	paymentErpID string,
	entityErpID string,
	accountErpID string,
	aiReasoning pgtype.Text,
	macroClass string,
) quickbooks.Deposit {
	absAmt := math.Abs(amount)

	note := "System Trace: Processed via AI Booking Automation Pipeline v1.0."
	if macroClass == "Asset" || macroClass == "Liability" || macroClass == "Equity" {
		note += fmt.Sprintf(" | System Trace: Balance Sheet allocation targeting %s account.", macroClass)
	}

	return quickbooks.Deposit{
		DepositToAccountRef: &quickbooks.ReferenceType{Value: paymentErpID},
		TxnDate:             txnDate,
		TotalAmt:            json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		PrivateNote:         note,
		Line: []quickbooks.DepositLine{
			{
				Amount:      json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
				DetailType:  "DepositLineDetail",
				Description: formatLineDescription(aiReasoning),
				DepositLineDetail: &quickbooks.DepositLineDetail{
					Entity:     &quickbooks.ReferenceType{Value: entityErpID},
					AccountRef: &quickbooks.ReferenceType{Value: accountErpID},
				},
			},
		},
	}
}

// constructTransfer separates QBO Transfer payload construction
func constructTransfer(
	amount float64,
	txnDate quickbooks.Date,
	fromAccountErpID string,
	toAccountErpID string,
	aiReasoning pgtype.Text,
) quickbooks.Transfer {
	absAmt := math.Abs(amount)

	reasoning := formatLineDescription(aiReasoning)
	var note string
	if reasoning == "" {
		note = "System Trace: Processed via AI Booking Automation Pipeline v1.0."
	} else {
		note = "System Trace: Processed via AI Booking Automation Pipeline v1.0. | " + reasoning
	}

	return quickbooks.Transfer{
		Amount:         json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		TxnDate:        txnDate,
		FromAccountRef: quickbooks.ReferenceType{Value: fromAccountErpID},
		ToAccountRef:   quickbooks.ReferenceType{Value: toAccountErpID},
		PrivateNote:    note,
	}
}
