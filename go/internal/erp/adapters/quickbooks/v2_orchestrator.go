package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/database"
	sdk "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/v2_agents"
)

// V2Store is the minimal database interface required by the V2 orchestrator.
type V2Store interface {
	GetStagingTransactionsReadyForQBO(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	GetCleanupSession(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetAccountByID(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorByID(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerByID(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
	GetAccountsByRealm(context.Context, string) ([]database.ShadowErpAccount, error)
}

// V2Connector is the minimal QBO connector interface required by the V2 orchestrator.
type V2Connector interface {
	BatchCreateStagingTransactions(context.Context, string, []sdk.BatchItemRequest) ([]sdk.BatchItemResponse, error)
}

// V2Orchestrator executes the 3-phase V2 pipeline locally.
// It is called by the V2 push worker after receiving a NATS message.
type V2Orchestrator struct {
	logger    *slog.Logger
	db        V2Store
	pool      *pgxpool.Pool
	connector V2Connector
	llm       v2_agents.LLMClient
}

// NewV2Orchestrator creates a new V2 orchestrator.
func NewV2Orchestrator(
	logger *slog.Logger,
	db V2Store,
	pool *pgxpool.Pool,
	connector V2Connector,
	llm v2_agents.LLMClient,
) *V2Orchestrator {
	return &V2Orchestrator{
		logger:    logger,
		db:        db,
		pool:      pool,
		connector: connector,
		llm:       llm,
	}
}

// batchIDMapping ties a batch request bId back to the staging transaction row.
type v2BatchMapping struct {
	TxnID      pgtype.UUID
	EntityType string
}

// Run executes the full V2 3-phase pipeline for a given session.
func (o *V2Orchestrator) Run(ctx context.Context, pgSessionID pgtype.UUID) error {
	session, err := o.db.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		return fmt.Errorf("v2 orchestrator: session lookup failed: %w", err)
	}
	realmID := session.RealmID.String

	o.logger.Info("v2 orchestrator: processing session",
		"realm_id", realmID)

	rows, err := o.db.GetStagingTransactionsReadyForQBO(ctx, pgSessionID)
	if err != nil {
		return fmt.Errorf("v2 orchestrator: failed to fetch staging transactions: %w", err)
	}

	if len(rows) == 0 {
		o.logger.Info("v2 orchestrator: no ready transactions",
			"realm_id", realmID)
		return nil
	}

	if !session.BankAccountID.Valid {
		return fmt.Errorf("v2 orchestrator: session has no bank account assigned")
	}
	acct, err := o.db.GetAccountByID(ctx, session.BankAccountID)
	if err != nil {
		return fmt.Errorf("v2 orchestrator: failed to lookup session bank account: %w", err)
	}
	if acct.ErpID == "" {
		return fmt.Errorf("v2 orchestrator: payment account %q has no ERP ID", acct.Name)
	}

	sourceAccountErpID := acct.ErpID
	sourceAccountType := acct.AccountType

	// Phase 1: Stratification — classify each row's accounting intent.
	stratResults := make(map[pgtype.UUID]*v2_agents.StratificationResult)
	var cleanRows []database.FignodeStagingTransaction

	for _, row := range rows {
		result, stratErr := v2_agents.ClassifyIntent(ctx, o.llm, row, sourceAccountType)
		if stratErr != nil {
			o.logger.Warn("v2 orchestrator: stratification failed, falling back to STANDARD_PL",
				"txn_id", row.ID, "error", stratErr)
			stratResults[row.ID] = &v2_agents.StratificationResult{
				AccountingIntent: v2_agents.IntentStandardPL,
				Reasoning:        fmt.Sprintf("stratification error: %v", stratErr),
				Confidence:       0.0,
			}
		} else {
			stratResults[row.ID] = result
		}

		o.logger.Info("v2 orchestrator: stratified",
			"txn_id", row.ID,
			"intent", stratResults[row.ID].AccountingIntent,
			"confidence", stratResults[row.ID].Confidence,
		)
	}

	// Phase 2: Transfer Guard — evaluate POTENTIAL_TRANSFER rows.
	for _, row := range rows {
		sr := stratResults[row.ID]
		if sr.AccountingIntent != v2_agents.IntentPotentialTransfer {
			cleanRows = append(cleanRows, row)
			continue
		}

		guardResult, guardErr := v2_agents.EvaluateTransferRisk(ctx, o.llm, row, acct.Name, sr)
		if guardErr != nil {
			o.logger.Warn("v2 orchestrator: transfer guard failed, defaulting to TRANSFER_HOLD",
				"txn_id", row.ID, "error", guardErr)
			o.holdTransaction(ctx, row.ID,
				"Transfer guard evaluation failed. Please verify whether a mirror statement exists for this transaction.")
			continue
		}

		switch guardResult.Action {
		case v2_agents.ActionAllow:
			o.logger.Info("v2 orchestrator: transfer guard allowed",
				"txn_id", row.ID)
			cleanRows = append(cleanRows, row)

		case v2_agents.ActionRequireExternalDocument:
			prompt := guardResult.ClientPrompt
			if prompt == "" {
				prompt = "This transaction appears to be a transfer between accounts. Please provide the mirror statement from the receiving account to confirm this is not a duplicate."
			}
			o.holdTransaction(ctx, row.ID, prompt)
			o.logger.Info("v2 orchestrator: transfer held",
				"txn_id", row.ID,
				"reason", guardResult.Reasoning)

		default:
			o.logger.Warn("v2 orchestrator: unknown transfer guard action, holding",
				"txn_id", row.ID, "action", guardResult.Action)
			o.holdTransaction(ctx, row.ID, "Unknown transfer guard result. Manual review required.")
		}
	}

	if len(cleanRows) == 0 {
		o.logger.Info("v2 orchestrator: no clean rows to push",
			"realm_id", realmID)
		return nil
	}

	// Phase 3: Direct Hydration & Batch Push.
	o.logger.Info("v2 orchestrator: pushing clean rows",
		"realm_id", realmID, "count", len(cleanRows))

	batchItems, itemMap, hydrateErrors := o.hydrateBatchItems(ctx, realmID, cleanRows, sourceAccountErpID, sourceAccountType)

	for txnID, errMsg := range hydrateErrors {
		o.markV2Failed(ctx, txnID, errMsg)
	}
	if len(hydrateErrors) > 0 {
		o.logger.Warn("v2 orchestrator: hydration failures",
			"realm_id", realmID, "count", len(hydrateErrors))
	}

	if len(batchItems) == 0 {
		return nil
	}

	responses, err := o.connector.BatchCreateStagingTransactions(ctx, realmID, batchItems)
	if err != nil {
		o.logger.Error("v2 orchestrator: batch push fatal",
			"realm_id", realmID, "error", err)
		return fmt.Errorf("v2 orchestrator: batch push fatal: %w", err)
	}

	o.processBatchResponses(ctx, responses, itemMap, realmID)
	return nil
}

// holdTransaction marks a staging row as TRANSFER_HOLD with a client-facing reason.
func (o *V2Orchestrator) holdTransaction(ctx context.Context, txnID pgtype.UUID, reason string) {
	_, err := o.pool.Exec(ctx,
		`UPDATE fignode.staging_transactions
		 SET status = 'TRANSFER_HOLD',
		     v2_status = 'TRANSFER_HOLD',
		     v2_transfer_hold_reason = $2,
		     error_message = 'V2 Transfer Guard: ' || $2,
		     updated_at = NOW()
		 WHERE id = $1`,
		txnID, reason,
	)
	if err != nil {
		o.logger.Error("v2 orchestrator: failed to mark transfer hold",
			"txn_id", txnID, "error", err)
	}
}

// markV2Failed marks a staging row with V2 failure status.
func (o *V2Orchestrator) markV2Failed(ctx context.Context, txnID pgtype.UUID, errMsg string) {
	_, err := o.pool.Exec(ctx,
		`UPDATE fignode.staging_transactions
		 SET v2_status = 'v2_failed',
		     error_message = $2,
		     updated_at = NOW()
		 WHERE id = $1`,
		txnID, errMsg,
	)
	if err != nil {
		o.logger.Error("v2 orchestrator: failed to mark v2 failed",
			"txn_id", txnID, "error", err)
	}
}

// hydrateBatchItems converts clean staging transactions into QBO batch items.
func (o *V2Orchestrator) hydrateBatchItems(
	ctx context.Context,
	realmID string,
	rows []database.FignodeStagingTransaction,
	paymentErpID string,
	sourceAccountType string,
) ([]sdk.BatchItemRequest, map[string]v2BatchMapping, map[pgtype.UUID]string) {
	var items []sdk.BatchItemRequest
	itemMap := make(map[string]v2BatchMapping)
	failures := make(map[pgtype.UUID]string)

	isCreditCard := sourceAccountType == "Credit Card"

	ccAccounts := make([]string, 0)
	allAccounts, err := o.db.GetAccountsByRealm(ctx, realmID)
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

		accountObj, acctErr := o.resolveAccountERPID(ctx, row)
		if acctErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve account ERP ID: %v", acctErr)
			continue
		}
		accountErpID := accountObj.ErpID

		amount, amtErr := parseV2Amount(row.RawAmount)
		if amtErr != nil {
			failures[row.ID] = fmt.Sprintf("invalid amount %q: %v", row.RawAmount, amtErr)
			continue
		}

		txnDate := parseV2TxnDate(row.ParsedDate)
		bID := row.ID.String()
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
				o.holdTransaction(ctx, row.ID,
					"Credit card payment detected. Please upload the mirror credit card statement to confirm this is not a duplicate.")
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

			transfer := constructV2Transfer(amount, txnDate, fromAccount, toAccount, row.AiReasoning)
			items = append(items, sdk.BatchItemRequest{
				BId:       bID,
				Operation: "create",
				Entity:    "Transfer",
				Payload:   transfer,
			})
			itemMap[bID] = v2BatchMapping{TxnID: row.ID, EntityType: "Transfer"}
			continue
		}

		entityErpID, entityErr := o.resolveEntityERPID(ctx, row)
		if entityErr != nil {
			failures[row.ID] = fmt.Sprintf("cannot resolve entity ERP ID: %v", entityErr)
			continue
		}

		switch cashDir {
		case "INFLOW":
			if isCreditCard {
				purchase := constructV2Purchase(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String, isCreditCard, true)
				items = append(items, sdk.BatchItemRequest{
					BId:       bID,
					Operation: "create",
					Entity:    "Purchase",
					Payload:   purchase,
				})
				itemMap[bID] = v2BatchMapping{TxnID: row.ID, EntityType: "Purchase"}
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

				deposit := constructV2Deposit(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String)
				items = append(items, sdk.BatchItemRequest{
					BId:       bID,
					Operation: "create",
					Entity:    "Deposit",
					Payload:   deposit,
				})
				itemMap[bID] = v2BatchMapping{TxnID: row.ID, EntityType: "Deposit"}
			}

		case "OUTFLOW":
			purchase := constructV2Purchase(amount, txnDate, paymentErpID, entityErpID, accountErpID, row.AiReasoning, accountObj.Classification.String, isCreditCard, false)
			items = append(items, sdk.BatchItemRequest{
				BId:       bID,
				Operation: "create",
				Entity:    "Purchase",
				Payload:   purchase,
			})
			itemMap[bID] = v2BatchMapping{TxnID: row.ID, EntityType: "Purchase"}

		default:
			failures[row.ID] = fmt.Sprintf("unknown cash_direction: %s", row.CashDirection.String)
		}
	}

	return items, itemMap, failures
}

// resolveEntityERPID returns the QBO ERP ID for the entity.
func (o *V2Orchestrator) resolveEntityERPID(
	ctx context.Context,
	row database.FignodeStagingTransaction,
) (string, error) {
	if row.OverrideVendorID.Valid || row.PredictedVendorID.Valid {
		entityID := row.PredictedVendorID
		if row.OverrideVendorID.Valid {
			entityID = row.OverrideVendorID
		}
		vendor, err := o.db.GetVendorByID(ctx, entityID)
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
		customer, err := o.db.GetCustomerByID(ctx, entityID)
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
func (o *V2Orchestrator) resolveAccountERPID(
	ctx context.Context,
	row database.FignodeStagingTransaction,
) (database.ShadowErpAccount, error) {
	accountID := row.PredictedAccountID
	if row.OverrideAccountID.Valid {
		accountID = row.OverrideAccountID
	}
	if !accountID.Valid {
		return database.ShadowErpAccount{}, fmt.Errorf("no account assigned")
	}
	acct, err := o.db.GetAccountByID(ctx, accountID)
	if err != nil {
		return database.ShadowErpAccount{}, fmt.Errorf("account lookup failed: %w", err)
	}
	if acct.ErpID == "" {
		return database.ShadowErpAccount{}, fmt.Errorf("account %s has no ERP ID", acct.Name)
	}
	return acct, nil
}

// processBatchResponses iterates through batch responses and logs results.
func (o *V2Orchestrator) processBatchResponses(
	ctx context.Context,
	responses []sdk.BatchItemResponse,
	itemMap map[string]v2BatchMapping,
	realmID string,
) {
	var syncCount, failCount int

	for _, resp := range responses {
		mapping, ok := itemMap[resp.BId]
		if !ok {
			o.logger.Warn("v2 orchestrator: unrecognized bId in batch response",
				"bId", resp.BId)
			continue
		}

		if resp.HasError() {
			errMsg := resp.GetError()
			o.markV2Failed(ctx, mapping.TxnID, errMsg)
			failCount++
			o.logger.Warn("v2 orchestrator: batch item failed",
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
			o.markV2Failed(ctx, mapping.TxnID, "QBO returned no entity ID")
			failCount++
			continue
		}

		o.markV2Synced(ctx, mapping.TxnID, erpID)
		syncCount++
	}

	o.logger.Info("v2 orchestrator: batch complete",
		"realm_id", realmID,
		"v2_synced", syncCount,
		"v2_failed", failCount,
		"total_responses", len(responses),
	)
}

// markV2Synced marks a row as successfully synced by V2.
func (o *V2Orchestrator) markV2Synced(ctx context.Context, txnID pgtype.UUID, erpID string) {
	_, err := o.pool.Exec(ctx,
		`UPDATE fignode.staging_transactions
		 SET v2_status = 'v2_synced',
		     v2_erp_transaction_id = $2,
		     erp_transaction_id = COALESCE(erp_transaction_id, $2),
		     synced_at = NOW(),
		     updated_at = NOW()
		 WHERE id = $1`,
		txnID, erpID,
	)
	if err != nil {
		o.logger.Error("v2 orchestrator: failed to mark v2 synced",
			"txn_id", txnID, "error", err)
	}
}

// --- Pure helper functions for QBO payload construction ---

func parseV2Amount(raw string) (float64, error) {
	cleaned := strings.ReplaceAll(raw, ",", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0, fmt.Errorf("empty amount")
	}
	return strconv.ParseFloat(cleaned, 64)
}

func parseV2TxnDate(d pgtype.Date) sdk.Date {
	if !d.Valid {
		return sdk.Date{Time: time.Now()}
	}
	return sdk.Date{Time: d.Time}
}

func formatV2LineDescription(reasoning pgtype.Text) string {
	if !reasoning.Valid || reasoning.String == "" {
		return ""
	}
	return "AI Reasoning: " + reasoning.String
}

// constructV2Purchase builds a QBO Purchase payload for the V2 pipeline.
func constructV2Purchase(
	amount float64,
	txnDate sdk.Date,
	paymentErpID string,
	entityErpID string,
	accountErpID string,
	aiReasoning pgtype.Text,
	macroClass string,
	isCreditCard bool,
	isCredit bool,
) sdk.Purchase {
	paymentType := "Cash"
	if isCreditCard {
		paymentType = "CreditCard"
	}

	absAmt := math.Abs(amount)

	note := "System Trace: Auto-stratified via V2 AI Orchestration Pipeline."
	if macroClass == "Asset" || macroClass == "Liability" || macroClass == "Equity" {
		note += fmt.Sprintf(" | System Trace: Balance Sheet allocation targeting %s account.", macroClass)
	}

	return sdk.Purchase{
		PaymentType: paymentType,
		Credit:      isCredit,
		AccountRef:  sdk.ReferenceType{Value: paymentErpID},
		EntityRef:   sdk.ReferenceType{Value: entityErpID},
		TxnDate:     txnDate,
		TotalAmt:    json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		PrivateNote: note,
		Line: []sdk.Line{
			{
				DetailType: "AccountBasedExpenseLineDetail",
				Amount:     json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
				Description: formatV2LineDescription(aiReasoning),
				AccountBasedExpenseLineDetail: sdk.AccountBasedExpenseLineDetail{
					AccountRef: sdk.ReferenceType{Value: accountErpID},
				},
			},
		},
	}
}

// constructV2Deposit builds a QBO Deposit payload for the V2 pipeline.
func constructV2Deposit(
	amount float64,
	txnDate sdk.Date,
	paymentErpID string,
	entityErpID string,
	accountErpID string,
	aiReasoning pgtype.Text,
	macroClass string,
) sdk.Deposit {
	absAmt := math.Abs(amount)

	note := "System Trace: Processed via V2 AI Booking Automation Pipeline."
	if macroClass == "Asset" || macroClass == "Liability" || macroClass == "Equity" {
		note += fmt.Sprintf(" | System Trace: Balance Sheet allocation targeting %s account.", macroClass)
	}

	return sdk.Deposit{
		DepositToAccountRef: &sdk.ReferenceType{Value: paymentErpID},
		TxnDate:             txnDate,
		TotalAmt:            json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		PrivateNote:         note,
		Line: []sdk.DepositLine{
			{
				Amount:        json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
				DetailType:    "DepositLineDetail",
				Description:   formatV2LineDescription(aiReasoning),
				DepositLineDetail: &sdk.DepositLineDetail{
					Entity:     &sdk.ReferenceType{Value: entityErpID},
					AccountRef: &sdk.ReferenceType{Value: accountErpID},
				},
			},
		},
	}
}

// constructV2Transfer builds a QBO Transfer payload for the V2 pipeline.
func constructV2Transfer(
	amount float64,
	txnDate sdk.Date,
	fromAccountErpID string,
	toAccountErpID string,
	aiReasoning pgtype.Text,
) sdk.Transfer {
	absAmt := math.Abs(amount)

	reasoning := formatV2LineDescription(aiReasoning)
	var note string
	if reasoning == "" {
		note = "System Trace: Processed via V2 AI Booking Automation Pipeline."
	} else {
		note = "System Trace: Processed via V2 AI Booking Automation Pipeline. | " + reasoning
	}

	return sdk.Transfer{
		Amount:         json.Number(strconv.FormatFloat(absAmt, 'f', 2, 64)),
		TxnDate:        txnDate,
		FromAccountRef: sdk.ReferenceType{Value: fromAccountErpID},
		ToAccountRef:   sdk.ReferenceType{Value: toAccountErpID},
		PrivateNote:    note,
	}
}
