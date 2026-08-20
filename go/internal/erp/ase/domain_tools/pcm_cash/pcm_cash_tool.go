package pcm_cash

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
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
		rawTxn := enrichment.RawTransaction{
			TransactionID:   node.NodeID,
			RawDescription:  rawDesc,
			Amount:          amountTTC,
			Currency:        "MAD",
			CashDirection:   enrichment.CashDirection(dirStr),
			TransactionDate: time.Now(),
			AccountCode:     "514100",
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
		// Deterministic 5115 Transit clearing evaluation
		node.Mu.Lock()
		node.Payload["transit_account"] = TransitClearingAccount
		node.Payload["transit_reconciled"] = true
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
			node.Mu.Unlock()
		}
	}
	return nil
}

func (t *PcmBankCashTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps domain_tools.ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var payload struct {
		SessionID              string   `json:"session_id"`
		EntityID               string   `json:"entity_id"`
		RealmID                string   `json:"realm_id"`
		DebugStartNode         string   `json:"debug_start_node"`
		DebugStopAfterNode     string   `json:"debug_stop_after_node"`
		DebugTransactionIDs    []string `json:"debug_transaction_ids"`
		DebugLLMTimeoutSeconds int      `json:"debug_llm_timeout_seconds"`
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

		node := ase.NewASENode(tenantID, dagName, map[string]any{
			"raw_description":           desc,
			"cash_direction":            direction,
			"raw_amount":                txn.RawAmount,
			"domain_tool":               "pcm_cash_accounting",
			"session_id":                payload.SessionID,
			"statement_type":            "BANK_STATEMENT",
			"account_code":              "514100",
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

	node := ase.NewASENode(tenantID, dagName, map[string]any{
		"raw_description": desc,
		"cash_direction":  direction,
		"raw_amount":      dbTx.RawAmount,
		"domain_tool":     "pcm_cash_accounting",
		"session_id":      uuid.UUID(session.ID.Bytes).String(),
		"statement_type":  "BANK_STATEMENT",
		"account_code":    "514100",
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
