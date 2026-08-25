package pcm_cash

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	accountingservice "github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func init() {
	domain_tools.Register("pcm_cash_accounting", &PcmBankCashTool{})
	domain_tools.Register("pcm_petty_cash", &PcmPettyCashTool{})
}

// PcmBankCashTool handles Moroccan Bank Statements (5141) and general cash accounting rules.
type PcmBankCashTool struct{}

func (t *PcmBankCashTool) ExecuteAction(ctx context.Context, actionProvider string, node *ase.AutonomousSemanticEngineNode) error {
	rawDesc, _ := node.Payload["raw_description"].(string)
	rawAmtStr, _ := node.Payload["raw_amount"].(string)
	amountTTC, _ := strconv.ParseFloat(rawAmtStr, 64)

	switch actionProvider {
	case "moroccan_enricher":
		eng := enrichment.NewMoroccanEnrichmentEngine()
		dirStr, _ := node.Payload["cash_direction"].(string)
		if dirStr == "" {
			dirStr = "OUTFLOW"
		}
		currency, _ := node.Payload["currency"].(string)
		if currency == "" {
			currency = "MAD"
		}
		accountCode, _ := node.Payload["account_code"].(string)
		if accountCode == "" {
			return fmt.Errorf("HOLD_BANK_ACCOUNT_CONFIGURATION: bank ledger account is required")
		}
		rawTxn := enrichment.RawTransaction{
			TransactionID:   node.NodeID,
			RawDescription:  rawDesc,
			Amount:          amountTTC,
			Currency:        currency,
			CashDirection:   enrichment.CashDirection(dirStr),
			TransactionDate: time.Now(),
			AccountCode:     accountCode,
		}
		env := eng.EnrichTransaction(ctx, node.RealmID, rawTxn)
		node.Mu.Lock()
		node.Payload["enrichment_envelope"] = env
		node.Payload["suggested_account"] = env.PCGMAccounting.SuggestedAccount
		node.Payload["account_label"] = env.PCGMAccounting.AccountLabel
		node.Payload["tva_rate"] = env.PCGMAccounting.DefaultTVARate
		node.Payload["amount_ht"] = env.PCGMAccounting.NetHTAmount
		node.Payload["amount_vat"] = env.PCGMAccounting.TVAAmount
		node.Payload["normalized_merchant"] = env.Counterparty.NormalizedName
		node.Mu.Unlock()
	case "bank_fee_splitter":
		if IsBankFeeDescription(rawDesc) {
			split, err := SplitBankFee(amountTTC, rawDesc)
			if err != nil {
				return err
			}
			node.Mu.Lock()
			node.Payload["amount_ht"] = split.AmountHT
			node.Payload["amount_vat"] = split.AmountVAT
			node.Payload["expense_account"] = split.ExpenseAccount
			node.Payload["vat_account"] = split.VATAccount
			node.Payload["bank_fee_processed"] = true
			node.Mu.Unlock()
		}
	case "ras_tax_evaluator":
		if IsForeignVendorDescription(rawDesc) {
			curr, _ := node.Payload["currency"].(string)
			if curr == "" {
				curr = "MAD"
			}
			ras, err := CalculateRASWithholding(amountTTC, RASTypeForeignService, curr)
			if err != nil {
				return err
			}
			node.Mu.Lock()
			node.Payload["ras_withholding_amount"] = ras.WithholdingAmount
			node.Payload["ras_net_bank_outflow"] = ras.NetBankOutflow
			node.Payload["ras_account"] = ras.WithholdingAccount
			node.Payload["expense_account"] = ras.ExpenseAccount
			node.Mu.Unlock()
		}
	case "transit_reconciler":
		// A transaction-level DAG can identify a likely 5115 transfer, but it
		// cannot reconcile it without the counterpart canonical bank/book line.
		node.Mu.Lock()
		node.Payload["transit_account"] = TransitClearingAccount
		node.Payload["transit_match_required"] = true
		node.Mu.Unlock()
	case "pending_instruments_matcher":
		direction, _ := node.Payload["cash_direction"].(string)
		isOutflow := direction == "OUTFLOW"
		instType, found := DetectInstrumentType(rawDesc, isOutflow)
		if found {
			pAcc, cAcc := DeterminePendingAccounts(instType)
			node.Mu.Lock()
			node.Payload["pending_account"] = pAcc
			node.Payload["cleared_account"] = cAcc
			node.Payload["instrument_type"] = string(instType)
			node.Payload["instrument_match_required"] = true
			node.Mu.Unlock()
		}
	}
	return nil
}

func (t *PcmBankCashTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps domain_tools.ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var payload struct {
		SessionID               string   `json:"session_id"`
		EntityID                string   `json:"entity_id"`
		RealmID                 string   `json:"realm_id"`
		BankAccountID           string   `json:"bank_account_id"`
		BankLedgerAccountCode   string   `json:"bank_ledger_account_code"`
		Currency                string   `json:"currency"`
		SourceDocumentIDs       []string `json:"source_document_ids"`
		WorkflowID              string   `json:"workflow_id"`
		WorkflowTraceID         string   `json:"workflow_trace_id"`
		StatementPeriodKey      string   `json:"statement_period_key"`
		StatementOpeningBalance string   `json:"statement_opening_balance"`
		StatementClosingBalance string   `json:"statement_closing_balance"`
		DebugStartNode          string   `json:"debug_start_node"`
		DebugStopAfterNode      string   `json:"debug_stop_after_node"`
		DebugTransactionIDs     []string `json:"debug_transaction_ids"`
		DebugLLMTimeoutSeconds  int      `json:"debug_llm_timeout_seconds"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil || payload.SessionID == "" {
		deps.Logger.Error("pcm_bank_cash_tool: missing or invalid session_id in payload", "error", err)
		return nil, nil
	}

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(payload.SessionID); err != nil {
		deps.Logger.Error("pcm_bank_cash_tool: invalid session_id UUID", "session", payload.SessionID)
		return nil, nil
	}

	session, err := deps.DB.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		if dagName == "test_dag" {
			deps.Logger.Info("pcm_bank_cash_tool: staging session not found, creating synthetic agent for test_dag", "session_id", payload.SessionID)
			node := ase.NewASENode("default_user", dagName, map[string]any{
				"raw_description": "DAG Test Workflow Debug Sample",
				"raw_amount":      "100.00",
				"domain_tool":     "pcm_cash_accounting",
				"session_id":      payload.SessionID,
			})
			node.SetLogger(deps.Logger)
			node.Persister = deps.Store
			return []*ase.AutonomousSemanticEngineNode{node}, nil
		}
		deps.Logger.Error("pcm_bank_cash_tool: failed to fetch session", "error", err)
		return nil, err
	}

	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}
	realmID := payload.RealmID
	if realmID == "" && session.RealmID.Valid {
		realmID = session.RealmID.String
	}
	debugIDs := make(map[string]struct{}, len(payload.DebugTransactionIDs))
	for _, id := range payload.DebugTransactionIDs {
		debugIDs[id] = struct{}{}
	}
	debugRun := payload.DebugStartNode != "" || payload.DebugStopAfterNode != "" || len(debugIDs) > 0 || payload.DebugLLMTimeoutSeconds > 0

	pendingTxns, err := deps.DB.GetPendingStagingTransactions(ctx, pgSessionID)
	if err != nil {
		deps.Logger.Error("pcm_bank_cash_tool: failed to fetch pending staging transactions", "error", err)
		return nil, err
	}

	var enrichmentMap = make(map[string][]byte)
	if deps.DBPool != nil {
		rows, qErr := deps.DBPool.Query(ctx, "SELECT id, moroccan_enrichment FROM fignode.staging_transactions WHERE session_id = $1", pgSessionID)
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var id pgtype.UUID
				var enrJSON []byte
				if sErr := rows.Scan(&id, &enrJSON); sErr == nil {
					enrichmentMap[uuid.UUID(id.Bytes).String()] = enrJSON
				}
			}
		}
	}

	var agents []*ase.AutonomousSemanticEngineNode
	for _, txn := range pendingTxns {
		nodeIDStr := uuid.UUID(txn.ID.Bytes).String()
		if len(debugIDs) > 0 {
			if _, selected := debugIDs[nodeIDStr]; !selected {
				continue
			}
		}
		desc := ""
		if txn.RawDescription.Valid {
			desc = txn.RawDescription.String
		}
		direction := "OUTFLOW"
		if txn.CashDirection.Valid && txn.CashDirection.String != "" {
			direction = txn.CashDirection.String
		}

		canonicalBankLineID := ""
		operationDate := ""
		bankDescription := desc
		if bankLine, lineErr := deps.DB.GetBankStatementLineByStagingTransaction(ctx, txn.ID); lineErr == nil {
			canonicalBankLineID = uuid.UUID(bankLine.ID.Bytes).String()
			if bankLine.OperationDate.Valid {
				operationDate = bankLine.OperationDate.Time.Format("2006-01-02")
			} else if bankLine.ValueDate.Valid {
				operationDate = bankLine.ValueDate.Time.Format("2006-01-02")
			}
			bankDescription = bankLine.Description
		}
		if operationDate == "" && txn.ParsedDate.Valid {
			operationDate = txn.ParsedDate.Time.Format("2006-01-02")
		}

		node := ase.NewASENode(tenantID, dagName, map[string]any{
			"raw_description":           desc,
			"description":               bankDescription,
			"cash_direction":            direction,
			"raw_amount":                txn.RawAmount,
			"operation_date":            operationDate,
			"staging_transaction_id":    nodeIDStr,
			"bank_statement_line_id":    canonicalBankLineID,
			"domain_tool":               "pcm_cash_accounting",
			"session_id":                payload.SessionID,
			"statement_type":            "BANK_STATEMENT",
			"bank_account_id":           payload.BankAccountID,
			"account_code":              payload.BankLedgerAccountCode,
			"currency":                  payload.Currency,
			"source_document_ids":       payload.SourceDocumentIDs,
			"workflow_id":               payload.WorkflowID,
			"workflow_trace_id":         payload.WorkflowTraceID,
			"statement_period_key":      payload.StatementPeriodKey,
			"statement_opening_balance": payload.StatementOpeningBalance,
			"statement_closing_balance": payload.StatementClosingBalance,
			"entity_id":                 payload.EntityID,
			"realm_id":                  realmID,
			"debug_run":                 debugRun,
			"debug_start_node":          payload.DebugStartNode,
			"debug_stop_after_node":     payload.DebugStopAfterNode,
			"debug_llm_timeout_seconds": payload.DebugLLMTimeoutSeconds,
		})
		node.NodeID = nodeIDStr
		node.RealmID = realmID
		node.SetLogger(deps.Logger)
		node.Persister = deps.Store

		hydrateNodeFromPersistedEnrichment(node, txn, enrichmentMap[nodeIDStr])

		if txn.HumanAction.Valid && txn.HumanAction.String != "" {
			node.ContextUpdates = append(node.ContextUpdates, "User Resolution: "+txn.HumanAction.String)
		}
		if len(txn.AseExecutionTrace) > 0 {
			_ = json.Unmarshal(txn.AseExecutionTrace, &node.ExecutionTrace)
		}
		agents = append(agents, node)
	}

	if len(agents) == 0 && dagName == "test_dag" {
		node := ase.NewASENode(tenantID, dagName, map[string]any{
			"raw_description": "DAG Test Workflow Debug Sample",
			"raw_amount":      "100.00",
			"domain_tool":     "pcm_cash_accounting",
			"session_id":      payload.SessionID,
		})
		node.SetLogger(deps.Logger)
		node.Persister = deps.Store
		return []*ase.AutonomousSemanticEngineNode{node}, nil
	}

	return agents, nil
}

func (t *PcmBankCashTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps domain_tools.ToolDependencies) (map[string]interface{}, error) {
	startNodeID := "default"
	if len(a.ExecutionTrace) > 0 {
		startNodeID = a.ExecutionTrace[len(a.ExecutionTrace)-1].DAGNodeID
	}

	alertPrompt := fmt.Sprintf(
		"PCM BANK CASH ALERT: Transaction (ID: %s) stuck in %s.\nReason: %s\nDescription: %s\nAmount: %s\nCash Direction: %s\nStart Node: %s",
		a.NodeID, string(a.GetState()), a.HoldReason,
		a.Payload["raw_description"], a.Payload["raw_amount"], a.Payload["cash_direction"], startNodeID,
	)

	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   payloadString(a.Payload, "entity_id", a.TenantID),
		"realm_id":    payloadString(a.Payload, "realm_id", a.RealmID),
		"session_id":  payloadString(a.Payload, "session_id", ""),
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:pcm_cash_accounting:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func payloadString(payload map[string]any, key, fallback string) string {
	if value, ok := payload[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func (t *PcmBankCashTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps domain_tools.ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	idUUID, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, err
	}
	pgID := pgtype.UUID{Bytes: idUUID, Valid: true}
	dbTx, err := deps.DB.GetProposedTransactionByID(ctx, pgID)
	if err != nil {
		return nil, err
	}
	session, err := deps.DB.GetCleanupSession(ctx, dbTx.SessionID)
	if err != nil {
		return nil, err
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	desc := ""
	if dbTx.RawDescription.Valid {
		desc = dbTx.RawDescription.String
	}
	direction := "OUTFLOW"
	if dbTx.CashDirection.Valid && dbTx.CashDirection.String != "" {
		direction = dbTx.CashDirection.String
	}

	realmID := ""
	if session.RealmID.Valid {
		realmID = session.RealmID.String
	}
	bankLineID, bankAccountID, bankCode, currency, operationDate, sourceDocumentID, entityID := "", "", "", "", "", "", ""
	statementPeriodKey, statementOpening, statementClosing := "", "", ""
	if deps.DBPool != nil {
		var rawOCR []byte
		_ = deps.DBPool.QueryRow(ctx, `SELECT b.id::text, b.bank_account_id::text, ba.ledger_account_code,
			b.currency, COALESCE(b.operation_date, b.value_date)::text, b.source_document_id::text,
			COALESCE(u.entity_id::text, ''), d.raw_ocr_json
			FROM shadow_erp.bank_statement_lines b
			JOIN shadow_erp.bank_accounts ba ON ba.id = b.bank_account_id
			JOIN toro_core.documents d ON d.id = b.source_document_id
			JOIN fignode.staging_transactions st ON st.id = b.source_staging_transaction_id
			JOIN fignode.staging_sessions ss ON ss.id = st.session_id
			LEFT JOIN toro_core.users u ON u.id = ss.created_by
			WHERE st.id = $1`, pgID).Scan(
			&bankLineID, &bankAccountID, &bankCode, &currency, &operationDate, &sourceDocumentID, &entityID, &rawOCR,
		)
		statementPeriodKey, statementOpening, statementClosing = statementContextFromRawOCR(rawOCR)
	}

	node := ase.NewASENode(tenantID, dagName, map[string]any{
		"raw_description":           desc,
		"description":               desc,
		"cash_direction":            direction,
		"raw_amount":                dbTx.RawAmount,
		"domain_tool":               "pcm_cash_accounting",
		"session_id":                uuid.UUID(session.ID.Bytes).String(),
		"statement_type":            "BANK_STATEMENT",
		"entity_id":                 entityID,
		"realm_id":                  realmID,
		"bank_statement_line_id":    bankLineID,
		"bank_account_id":           bankAccountID,
		"account_code":              bankCode,
		"currency":                  currency,
		"operation_date":            operationDate,
		"staging_transaction_id":    nodeID,
		"source_document_ids":       []string{sourceDocumentID},
		"statement_period_key":      statementPeriodKey,
		"statement_opening_balance": statementOpening,
		"statement_closing_balance": statementClosing,
	})
	node.NodeID = nodeID
	node.RealmID = realmID
	node.SetLogger(deps.Logger)
	node.Persister = deps.Store

	var enrJSON []byte
	if deps.DBPool != nil {
		_ = deps.DBPool.QueryRow(ctx, "SELECT moroccan_enrichment FROM fignode.staging_transactions WHERE id = $1", pgID).Scan(&enrJSON)
	}
	hydrateNodeFromPersistedEnrichment(node, dbTx, enrJSON)
	if len(dbTx.AseExecutionTrace) > 0 {
		_ = json.Unmarshal(dbTx.AseExecutionTrace, &node.ExecutionTrace)
		for _, step := range node.ExecutionTrace {
			if step.PropertyKey != "" && len(step.Candidates) > 0 {
				node.Candidates[step.PropertyKey] = step.Candidates
			}
		}
	}

	return node, nil
}

func statementContextFromRawOCR(raw []byte) (periodKey, opening, closing string) {
	var extraction struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &extraction) != nil || extraction.Data == nil {
		return "", "", ""
	}
	read := func(keys ...string) string {
		for _, key := range keys {
			value, ok := extraction.Data[key]
			if !ok {
				continue
			}
			var text string
			if json.Unmarshal(value, &text) == nil {
				return text
			}
			return strings.TrimSpace(string(value))
		}
		return ""
	}
	end := read("end_date", "period_end", "statement_end_date", "statement_date")
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006", "2/1/2006", "2-1-2006"} {
		if parsed, err := time.Parse(layout, end); err == nil {
			periodKey = parsed.Format("2006-01")
			break
		}
	}
	if value, err := accountingservice.NewReconciliationMoney(read("starting_balance", "opening_balance")); err == nil {
		opening = value.String()
	}
	if value, err := accountingservice.NewReconciliationMoney(read("ending_balance", "closing_balance")); err == nil {
		closing = value.String()
	}
	return periodKey, opening, closing
}

func (t *PcmBankCashTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	systemPrompt := `Evaluate Moroccan Bank Statement classification adjustment based on human feedback.`
	desc, _ := a.Payload["raw_description"].(string)
	userPrompt := fmt.Sprintf("Transaction: %s\nContext: %s\nTrace: %s", desc, newContext, string(traceBytes))
	return systemPrompt, userPrompt
}

func (t *PcmBankCashTool) GetClassifier(deps domain_tools.ToolDependencies) ase.Classifier {
	c := domain_tools.NewBookkeepingClassifier(deps.Runtime, deps.NC, "tasks.accounting.1.batch_categorization", deps.Logger)
	c.SetDB(deps.DB)
	c.SetVectorStore(deps.VectorStore)
	return c
}

func (t *PcmBankCashTool) GetStatePersister(deps domain_tools.ToolDependencies) ase.StatePersister {
	return domain_tools.NewStateStore(deps.DBPool, deps.Redis)
}

// PcmPettyCashTool handles Moroccan Petty Cash Statements & Vouchers (5161).
type PcmPettyCashTool struct{}

func (t *PcmPettyCashTool) ExecuteAction(ctx context.Context, actionProvider string, node *ase.AutonomousSemanticEngineNode) error {
	if actionProvider == "petty_cash_guardrails" {
		curBal, _ := node.Payload["current_balance"].(float64)
		rawAmtStr, _ := node.Payload["raw_amount"].(string)
		amountTTC, _ := strconv.ParseFloat(rawAmtStr, 64)
		direction, _ := node.Payload["cash_direction"].(string)
		isOutflow := direction != "INFLOW"
		dailyVendorTotal, _ := node.Payload["vendor_daily_total"].(float64)
		monthlyVendorTotal, _ := node.Payload["vendor_monthly_total"].(float64)

		res, err := EvaluatePettyCashVoucher(curBal, amountTTC, isOutflow, dailyVendorTotal, monthlyVendorTotal)
		node.Mu.Lock()
		node.Payload["projected_balance"] = res.ProjectedBalance
		node.Payload["is_caisse_creditrice"] = res.IsCaisseCreditrice
		node.Payload["is_cap_exceeded"] = res.IsCapExceeded
		node.Payload["is_vat_deductible"] = res.IsVATDeductible
		if res.VatRestrictedAccount != "" {
			node.Payload["vat_restricted_account"] = res.VatRestrictedAccount
		}
		node.Mu.Unlock()

		if err != nil && res.IsCaisseCreditrice {
			node.HoldReason = ErrCaisseCreditricePrevented.Error()
			return err
		}
	}
	return nil
}

func (t *PcmPettyCashTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps domain_tools.ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil || payload.SessionID == "" {
		return nil, nil
	}

	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(payload.SessionID); err != nil {
		return nil, nil
	}
	session, err := deps.DB.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		return nil, err
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	pendingTxns, err := deps.DB.GetPendingStagingTransactions(ctx, pgSessionID)
	if err != nil {
		return nil, err
	}

	var enrichmentMap = make(map[string][]byte)
	if deps.DBPool != nil {
		rows, qErr := deps.DBPool.Query(ctx, "SELECT id, moroccan_enrichment FROM fignode.staging_transactions WHERE session_id = $1", pgSessionID)
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var id pgtype.UUID
				var enrJSON []byte
				if sErr := rows.Scan(&id, &enrJSON); sErr == nil {
					enrichmentMap[uuid.UUID(id.Bytes).String()] = enrJSON
				}
			}
		}
	}

	var agents []*ase.AutonomousSemanticEngineNode
	for _, txn := range pendingTxns {
		nodeIDStr := uuid.UUID(txn.ID.Bytes).String()
		desc := ""
		if txn.RawDescription.Valid {
			desc = txn.RawDescription.String
		}
		direction := "OUTFLOW"
		if txn.CashDirection.Valid && txn.CashDirection.String != "" {
			direction = txn.CashDirection.String
		}

		node := ase.NewASENode(tenantID, dagName, map[string]any{
			"raw_description": desc,
			"cash_direction":  direction,
			"raw_amount":      txn.RawAmount,
			"domain_tool":     "pcm_petty_cash",
			"session_id":      payload.SessionID,
			"statement_type":  "PETTY_CASH",
			"account_code":    "516100",
		})
		node.NodeID = nodeIDStr
		node.SetLogger(deps.Logger)
		node.Persister = deps.Store

		hydrateNodeFromPersistedEnrichment(node, txn, enrichmentMap[nodeIDStr])

		agents = append(agents, node)
	}

	return agents, nil
}

func (t *PcmPettyCashTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps domain_tools.ToolDependencies) (map[string]interface{}, error) {
	alertPrompt := fmt.Sprintf(
		"PETTY CASH ALERT (Account 5161): Voucher/Transaction (ID: %s) triggered HOLD in %s.\nReason: %s\nDescription: %s\nAmount: %s",
		a.NodeID, string(a.GetState()), a.HoldReason, a.Payload["raw_description"], a.Payload["raw_amount"],
	)
	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   a.TenantID,
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:pcm_petty_cash:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func (t *PcmPettyCashTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps domain_tools.ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	idUUID, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, err
	}
	pgID := pgtype.UUID{Bytes: idUUID, Valid: true}
	dbTx, err := deps.DB.GetProposedTransactionByID(ctx, pgID)
	if err != nil {
		return nil, err
	}
	session, err := deps.DB.GetCleanupSession(ctx, dbTx.SessionID)
	if err != nil {
		return nil, err
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	desc := ""
	if dbTx.RawDescription.Valid {
		desc = dbTx.RawDescription.String
	}
	direction := "OUTFLOW"
	if dbTx.CashDirection.Valid && dbTx.CashDirection.String != "" {
		direction = dbTx.CashDirection.String
	}

	node := ase.NewASENode(tenantID, dagName, map[string]any{
		"raw_description": desc,
		"cash_direction":  direction,
		"raw_amount":      dbTx.RawAmount,
		"domain_tool":     "pcm_petty_cash",
		"session_id":      uuid.UUID(session.ID.Bytes).String(),
		"statement_type":  "PETTY_CASH",
		"account_code":    "516100",
	})
	node.NodeID = nodeID
	node.SetLogger(deps.Logger)
	node.Persister = deps.Store

	var enrJSON []byte
	if deps.DBPool != nil {
		_ = deps.DBPool.QueryRow(ctx, "SELECT moroccan_enrichment FROM fignode.staging_transactions WHERE id = $1", pgID).Scan(&enrJSON)
	}
	hydrateNodeFromPersistedEnrichment(node, dbTx, enrJSON)

	return node, nil
}

func (t *PcmPettyCashTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	systemPrompt := `Evaluate Moroccan Petty Cash (5161) voucher adjustment based on human feedback.`
	desc, _ := a.Payload["raw_description"].(string)
	userPrompt := fmt.Sprintf("Petty Cash Voucher: %s\nContext: %s\nTrace: %s", desc, newContext, string(traceBytes))
	return systemPrompt, userPrompt
}

func (t *PcmPettyCashTool) GetClassifier(deps domain_tools.ToolDependencies) ase.Classifier {
	c := domain_tools.NewBookkeepingClassifier(deps.Runtime, deps.NC, "tasks.accounting.1.pcm_petty_cash_batch", deps.Logger)
	c.SetDB(deps.DB)
	c.SetVectorStore(deps.VectorStore)
	return c
}

func (t *PcmPettyCashTool) GetStatePersister(deps domain_tools.ToolDependencies) ase.StatePersister {
	return domain_tools.NewStateStore(deps.DBPool, deps.Redis)
}

func hydrateNodeFromPersistedEnrichment(node *ase.AutonomousSemanticEngineNode, txn database.FignodeStagingTransaction, enrichmentJSON []byte) {
	if txn.PredictedVendorName.Valid && txn.PredictedVendorName.String != "" {
		node.Payload["normalized_merchant"] = txn.PredictedVendorName.String
	}
	if txn.PredictedAccountName.Valid && txn.PredictedAccountName.String != "" {
		node.Payload["suggested_account"] = txn.PredictedAccountName.String
	}
	if txn.MerchantName.Valid && txn.MerchantName.String != "" {
		node.Payload["merchant_name"] = txn.MerchantName.String
	}
	if txn.Category.Valid && txn.Category.String != "" {
		node.Payload["category"] = txn.Category.String
	}

	if len(enrichmentJSON) > 0 && string(enrichmentJSON) != "{}" {
		var env enrichment.AnnotatedMoroccanTransactionEnvelope
		if err := json.Unmarshal(enrichmentJSON, &env); err == nil {
			node.Payload["enrichment_envelope"] = env
			node.Payload["normalized_merchant"] = env.Counterparty.NormalizedName
			if env.Counterparty.Identifiers.ICE != nil {
				node.Payload["ice_number"] = *env.Counterparty.Identifiers.ICE
			}
			node.Payload["suggested_account"] = env.PCGMAccounting.SuggestedAccount
			node.Payload["account_label"] = env.PCGMAccounting.AccountLabel
			node.Payload["tva_rate"] = env.PCGMAccounting.DefaultTVARate
			node.Payload["amount_ht"] = env.PCGMAccounting.NetHTAmount
			node.Payload["amount_vat"] = env.PCGMAccounting.TVAAmount
			node.Payload["is_foreign_service"] = env.Counterparty.IsForeignService
			node.Payload["ras_withholding_amount"] = env.TaxAndCompliance.RASAmountMAD
			if env.TaxAndCompliance.RASAccount != nil {
				node.Payload["ras_account"] = *env.TaxAndCompliance.RASAccount
			}
			node.Payload["guardrail_status"] = string(env.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus)
			if env.DocumentEvidence.MatchedDocumentID != nil {
				node.Payload["matched_document_id"] = *env.DocumentEvidence.MatchedDocumentID
			}
		}
	}
}
