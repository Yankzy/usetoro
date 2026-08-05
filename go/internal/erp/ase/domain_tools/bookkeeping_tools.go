package domain_tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Property Keys for the multi-dimensional Candidates Map (Bookkeeping Domain)
const (
	PropMacroClass   = "macro_class"
	PropAccountType  = "account_type"
	PropCounterparty = "counterparty"
	PropAccountRef   = "resolved_account_id"
)

var AccountTypeOptions = map[string]string{
	"ASSET":     "Accounts Receivable, Bank, Fixed Assets, Leasehold Improvements, Other Current Assets, Other Assets",
	"LIABILITY": "Accounts Payable, Credit Cards, Long Term Liability, Other Current Liability",
	"EQUITY":    "Equity",
	"REVENUE":   "Income, Other Income",
	"EXPENSE":   "Expense, Other Expense, Cost of Goods Sold",
}

var MacroClassSpecificRules = map[string]string{
	"ASSET": `Evaluate each transaction against these distinct asset categories:
1. BANK: Use this if the description indicates cash or liquidity positions (e.g., checking, savings, or internal transfers between funding sources).
2. FIXED ASSET: Use this if the purchase is for long-term physical equipment, machinery, company vehicles, or software infrastructure licenses where the absolute value is > $2,500.
3. OTHER CURRENT ASSET: Use this for short-term economic values expected to convert to cash within one year (e.g., security deposits, inventory prepayments).
4. OTHER ASSETS: Use this for non-current, non-fixed assets (e.g., long-term investments, intangible assets).
5. LEASEHOLD IMPROVEMENTS: Use this for structural modifications to rented property.
6. ACCOUNTS RECEIVABLE: Use this only if the transaction represents money owed to the business by a customer.`,

	"LIABILITY": `Evaluate each transaction against these distinct liability categories:
1. CREDIT CARDS: Use this ONLY if the description explicitly references a known credit card provider or card payment obligation.
2. LONG TERM LIABILITY: Use this for debts with a maturity beyond one year (e.g., equipment loans, mortgages, SBA loans).
3. OTHER CURRENT LIABILITY: Use this for short-term obligations due within one year (e.g., payroll taxes payable, sales tax collected, short-term notes).
4. ACCOUNTS PAYABLE: Use this only if the transaction represents money the business owes to a supplier or vendor.`,

	"EQUITY": `Evaluate each transaction against this category:
1. EQUITY: Use this for all owner-related transactions including owner draws, owner investments, retained earnings adjustments, and partner distributions.`,

	"REVENUE": `Evaluate each transaction against these distinct revenue categories:
1. INCOME: Use this for all standard operating revenue from the primary business activity (e.g., product sales, service fees, consulting income, Stripe payouts).
2. OTHER INCOME: Use this for non-operating or incidental revenue (e.g., interest earned, foreign exchange gains, insurance claim proceeds, asset sale gains).`,

	"EXPENSE": `Evaluate each transaction against these distinct expense categories:
1. COST OF GOODS SOLD: Use this ONLY if the transaction is directly, structurally tied to producing revenue or purchasing inventory based on the Business Industry.
2. OTHER EXPENSE: Use this for unusual, non-operating outflows that do not reflect day-to-day business operations (e.g., tax penalties, legal settlements, corporate restructuring costs).
3. EXPENSE: Use this for all standard operating overhead, general administrative costs, software subscriptions, travel, meals, and general supplies.`,
}

func init() {
	Register("bookkeeping", &BookkeepingTool{})
}

type BookkeepingTool struct{}

func (t *BookkeepingTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	// 1. Extract TaskPayload
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil || payload.SessionID == "" {
		deps.Logger.Error("bookkeeping_tool: failed to unmarshal custom payload or missing session_id", "error", err)
		return nil, nil // Return empty, not error, if it can't parse
	}

	sessionID := payload.SessionID
	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(sessionID); err != nil {
		deps.Logger.Error("bookkeeping_tool: invalid session_id UUID", "session", sessionID)
		return nil, nil
	}

	// Fetch session to determine OutflowIs logic
	session, err := deps.DB.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		deps.Logger.Error("bookkeeping_tool: failed to fetch session", "error", err)
		return nil, err
	}
	outflowIs := session.OutflowIs
	realmID := ""
	if session.RealmID.Valid {
		realmID = session.RealmID.String
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	// Query unclassified Fignode transactions
	pendingTxns, err := deps.DB.GetPendingStagingTransactions(ctx, pgSessionID)
	if err != nil {
		deps.Logger.Error("bookkeeping_tool: failed to fetch pending staging transactions", "error", err)
		return nil, err
	}

	var agents []*ase.AutonomousSemanticEngineNode
	for _, txn := range pendingTxns {
		agent := t.mapToAgent(txn, sessionID, outflowIs, tenantID, realmID, dagName, deps)
		agents = append(agents, agent)
	}

	return agents, nil
}

func (t *BookkeepingTool) mapToAgent(txn database.FignodeStagingTransaction, sessionID, outflowIs, tenantID, realmID, dagName string, deps ToolDependencies) *ase.AutonomousSemanticEngineNode {
	desc := ""
	if txn.RawDescription.Valid {
		desc = txn.RawDescription.String
	}

	direction := "OUTFLOW"
	if txn.CashDirection.Valid && txn.CashDirection.String != "" {
		direction = txn.CashDirection.String
	} else {
		amtStr := strings.ReplaceAll(txn.RawAmount, ",", "")
		amtStr = strings.ReplaceAll(amtStr, "$", "")
		amtStr = strings.TrimSpace(amtStr)
		isNegativeFormat := false
		if strings.HasPrefix(amtStr, "(") && strings.HasSuffix(amtStr, ")") {
			amtStr = strings.Trim(amtStr, "()")
			isNegativeFormat = true
		}
		if amt, err := strconv.ParseFloat(amtStr, 64); err == nil {
			if isNegativeFormat {
				amt = -amt
			}
			isOutflow := false
			if outflowIs == "" || outflowIs == "NEGATIVE" {
				isOutflow = amt < 0
			} else {
				isOutflow = amt > 0
			}
			if isOutflow {
				direction = "OUTFLOW"
			} else {
				direction = "INFLOW"
			}
		}
	}

	agent := ase.NewASENode(tenantID, realmID, dagName, map[string]any{"raw_description": desc, "cash_direction": direction, "raw_amount": txn.RawAmount, "domain_tool": "bookkeeping", "session_id": sessionID})
	agent.NodeID = uuid.UUID(txn.ID.Bytes).String()
	agent.SetLogger(deps.Logger)
	agent.Persister = deps.Store

	if txn.HumanAction.Valid && txn.HumanAction.String != "" {
		agent.ContextUpdates = append(agent.ContextUpdates, "User/Human Resolution: "+txn.HumanAction.String)
	}

	if len(txn.AseExecutionTrace) > 0 {
		_ = json.Unmarshal(txn.AseExecutionTrace, &agent.ExecutionTrace)
		// Rehydrate Candidates map to preserve previous properties during Resume
		for _, step := range agent.ExecutionTrace {
			if step.PropertyKey != "" && len(step.Candidates) > 0 {
				agent.Candidates[step.PropertyKey] = step.Candidates
			}
		}
	}

	return agent
}

func (t *BookkeepingTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	var pgNodeID pgtype.UUID
	_ = pgNodeID.Scan(a.NodeID)
	if err := deps.DB.SetTransactionInReview(ctx, pgNodeID); err != nil {
		deps.Logger.Error("bookkeeping_tool: failed to set transaction in review", "error", err)
	}

	clientName := "Unknown"
	entityID := a.TenantID
	if a.RealmID != "" && deps.DB != nil {
		if companyInfo, err := deps.DB.GetCompanyInfo(ctx, a.RealmID); err == nil {
			clientName = companyInfo.CompanyName
		} else {
			deps.Logger.Warn("bookkeeping_tool: failed to fetch company info for client name", "realm_id", a.RealmID, "error", err)
		}

		conn, err := deps.DB.GetERPConnectionByRealm(ctx, database.GetERPConnectionByRealmParams{
			ErpSystem: "quickbooks_online",
			RealmID:   a.RealmID,
		})
		if err == nil && conn.EntityID.Valid {
			entityID = uuid.UUID(conn.EntityID.Bytes).String()
		} else {
			deps.Logger.Warn("bookkeeping_tool: failed to find ERP connection for firm entity ID lookup", "realm_id", a.RealmID, "error", err)
		}
	}

	startNodeID := "default"
	if len(a.ExecutionTrace) > 0 {
		startNodeID = a.ExecutionTrace[len(a.ExecutionTrace)-1].DAGNodeID
	}

	sessionID := ""
	if sid, ok := a.Payload["session_id"].(string); ok {
		sessionID = sid
	}

	if sessionID != "" {
		var sUUID, tUUID pgtype.UUID
		_ = sUUID.Scan(sessionID)
		_ = tUUID.Scan(a.NodeID)
		err := deps.DB.AddSessionEmailHold(ctx, database.AddSessionEmailHoldParams{
			SessionID:     sUUID,
			TransactionID: tUUID,
		})
		if err != nil {
			deps.Logger.Error("bookkeeping_tool: failed to add session email hold", "error", err)
		} else {
			deps.Logger.Info("bookkeeping_tool: intercepted email and added to batch hold", "session_id", sessionID, "transaction_id", a.NodeID)
		}

		return nil, nil
	} else {
		deps.Logger.Warn("bookkeeping_tool: missing session_id for email hold interception", "transaction_id", a.NodeID)
	}

	alertPrompt := fmt.Sprintf(
		"SYSTEM ALERT: A transaction (ID: %s) for Client '%s' (Realm ID: %s) under Tenant ID '%s' is stuck in %s.\n\nReason: %s\nDetails: %s\nAmount: %s\nCash Direction: %s\n\n"+
			"Please contact the business owner to ask for clarification to properly categorize this transaction. You can use the LookupClient tool if needed to find their contact details.\n\n"+
			"CRITICAL INSTRUCTION: You MUST first use the CheckExistingDocuments tool to check if the client already sent the receipt or document. If it is not found, you MUST use the QueueClientRequest tool to queue an outreach email. Do NOT use the SendEmail tool directly for this, as QueueClientRequest aggregates requests into a single daily digest for the client. After queuing, provide a brief internal summary (e.g., 'I have queued a request to the client.') as your conversational response.\n\n"+
			"IMPORTANT: Before queuing a request, you MUST use the FetchCommunicationHistory tool to check if we have already contacted this client about this exact transaction (same amount, customer, and description) within the last 24 hours. If we have already reached out recently, DO NOT queue a duplicate request.\n\n"+
			"When the user replies back with the requested information or clarification, you MUST use the UpdateTransactionClassification tool. This tool will pass the user's answer back to the DAG and unblock it so it can proceed. Use this tool only when you have gathered enough context from the user to confidently resolve the hold reason. \n\n"+
			"CRITICAL RULE: NEVER use the UpdateTransactionClassification tool just to report that you have contacted the user. Only use it when the user has ACTUALLY replied with the answer. If you are just queuing a request for clarification, DO NOT use UpdateTransactionClassification. Just use QueueClientRequest and finish your turn.\n\n"+
			"SUPER CRITICAL: You MUST NEVER use the UpdateTransactionClassification tool in the exact same turn that you receive this SYSTEM ALERT. The SYSTEM ALERT means you must ask for information. You cannot possibly have the answer yet. DO NOT hallucinate an answer. DO NOT run the UpdateTransactionClassification tool right now.\n\n"+
			"CRITICAL LOOP PREVENTION: If you already used the UpdateTransactionClassification tool with the user's latest reply and the transaction generated ANOTHER system alert because it is STILL stuck, you MUST ask the user for more clarification. Do NOT repeatedly submit the same user reply using the tool over and over again.\n\n"+
			"CRITICAL: When using the UpdateTransactionClassification tool, you MUST provide '%s' as the start_node_id. Do NOT invent or guess the start_node_id.",
		a.NodeID, clientName, a.RealmID, entityID, string(a.GetState()), a.HoldReason, a.Payload["raw_description"].(string), a.Payload["raw_amount"].(string), a.Payload["cash_direction"].(string), startNodeID,
	)

	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   entityID,
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:bookkeeping:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func (t *BookkeepingTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	idUUID, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, err
	}
	pgID := pgtype.UUID{Bytes: idUUID, Valid: true}
	dbTx, err := deps.DB.GetProposedTransactionByID(ctx, pgID)
	if err != nil {
		deps.Logger.Error("bookkeeping_tool: failed to fetch tx for resume", "error", err)
		return nil, err
	}

	session, err := deps.DB.GetCleanupSession(ctx, dbTx.SessionID)
	if err != nil {
		return nil, err
	}

	outflowIs := session.OutflowIs
	realmID := ""
	if session.RealmID.Valid {
		realmID = session.RealmID.String
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	sessionID := uuid.UUID(session.ID.Bytes).String()

	return t.mapToAgent(dbTx, sessionID, outflowIs, tenantID, realmID, dagName, deps), nil
}

func (t *BookkeepingTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	domainSystemPrompt := `For example, if the human says "This is not an asset, it's a liability", the step with dag_node_id "macro_classifier_outflow" that selected "ASSET" is wrong.
Additionally, formulate a generalized accounting rule for this specific company so this mistake is never repeated. Return it in the "extracted_rule" field. Keep it concise.
Also provide a "rule_keyword" (e.g., the vendor name or main subject). If it's a general rule, use "GLOBAL".`

	description := "Unknown"
	if desc, ok := a.Payload["raw_description"].(string); ok {
		description = desc
	}

	userPrompt := fmt.Sprintf("Transaction Description: %s\nNew Human Context: %s\nExecution Trace:\n%s", description, newContext, string(traceBytes))
	return domainSystemPrompt, userPrompt
}

func (t *BookkeepingTool) GetClassifier(deps ToolDependencies) ase.Classifier {
	c := NewBookkeepingClassifier(deps.Runtime, deps.NC, "tasks.accounting.1.batch_categorization", deps.Logger)
	c.SetDB(deps.DB)
	c.SetVectorStore(deps.VectorStore)
	return c
}

func (t *BookkeepingTool) GenerateExportPayload(ctx context.Context, sessionID string, agents []*ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, string, error) {
	var sessionUUID pgtype.UUID
	if err := sessionUUID.Scan(sessionID); err != nil {
		return nil, "", fmt.Errorf("invalid session id format: %w", err)
	}

	// 1. If there are queued client clarification requests, generate digest alert for general agent
	requests, err := deps.DB.GetQueuedRequestsBySession(ctx, sessionUUID)
	if err == nil && len(requests) > 0 {
		var sb strings.Builder
		sb.WriteString("System Alert: A bank reconciliation session has just completed. ")
		sb.WriteString(fmt.Sprintf("We have %d transactions that require client clarification or missing documents.\n\n", len(requests)))
		sb.WriteString("Please send a SINGLE digest email to the client detailing the following requests. ")
		sb.WriteString("Be polite and concise. Do NOT send separate emails for each transaction.\n\n")

		for i, req := range requests {
			sb.WriteString(fmt.Sprintf("Request %d:\n", i+1))
			sb.WriteString(fmt.Sprintf("- Transaction ID: %s\n", uuid.UUID(req.TransactionID.Bytes).String()))
			desc := "Unknown"
			if req.RawDescription.Valid {
				desc = req.RawDescription.String
			}
			sb.WriteString(fmt.Sprintf("- Description: %s\n", desc))
			sb.WriteString(fmt.Sprintf("- Amount: %s\n", req.RawAmount))
			dateStr := "Unknown"
			if req.RawDate.Valid {
				dateStr = req.RawDate.String
			}
			sb.WriteString(fmt.Sprintf("- Date: %s\n", dateStr))

			contextStr := ""
			if req.Context.Valid {
				contextStr = req.Context.String
			}
			sb.WriteString(fmt.Sprintf("- What we need (%s): %s\n\n", req.RequestType, contextStr))
		}

		entityID := ""
		if len(agents) > 0 {
			entityID = agents[0].TenantID
		}

		if err := deps.DB.MarkOutboxRequestsSent(ctx, sessionUUID); err != nil {
			deps.Logger.Error("bookkeeping_tool: failed to mark requests as sent", "error", err)
		}

		payload := map[string]interface{}{
			"prompt":      sb.String(),
			"body_text":   sb.String(),
			"entity_id":   entityID,
			"source":      "system",
			"from_handle": fmt.Sprintf("ase_session:%s", sessionID),
			"to_handle":   "general-agent",
		}

		return payload, "workers.general_agent_ingress", nil
	}

	// 2. Happy Path / Export Mode: Generate Sage 100 (.PNM / .CSV) export payload
	txs, err := deps.DB.GetPcmSessionTransactions(ctx, sessionUUID)
	if err != nil || len(txs) == 0 {
		return nil, "", fmt.Errorf("no transactions found to export for session %s: %v", sessionID, err)
	}

	session, err := deps.DB.GetCleanupSession(ctx, sessionUUID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch cleanup session %s: %w", sessionID, err)
	}

	realmID := ""
	if session.RealmID.Valid {
		realmID = session.RealmID.String
	}

	toHandle := ""
	fromHandle := "rap_atlas_sarl@a.usetoro.io"
	if realmID != "" {
		fromHandle = fmt.Sprintf("%s@a.usetoro.io", realmID)
	}
	if session.CreatedBy.Valid {
		if user, uErr := deps.DB.GetUserByID(ctx, session.CreatedBy); uErr == nil && user.Email != "" {
			toHandle = user.Email
		}
	}
	if toHandle == "" && len(agents) > 0 {
		if th, ok := agents[0].Payload["from_handle"].(string); ok && th != "" {
			toHandle = th
		}
	}

	accounts, err := deps.DB.GetAccountsByRealm(ctx, realmID)
	accMap := make(map[string]string)
	if err == nil {
		for _, acc := range accounts {
			accCode := acc.ID.String()
			if acc.AccountCode.Valid && acc.AccountCode.String != "" {
				accCode = acc.AccountCode.String
			}
			accMap[acc.ID.String()] = accCode
		}
	}

	bankAccountCode := "514100"
	if session.BankAccountID.Valid {
		if code, ok := accMap[session.BankAccountID.String()]; ok {
			bankAccountCode = code
		}
	}
	_ = bankAccountCode

	var csvLines []string
	csvLines = append(csvLines, "Journal;Date;CompteG;CompteA;Piece;Libelle;Debit;Credit")

	for itemIdx, tx := range txs {
		accountID := ""
		counterparty := ""
		if len(tx.AseExecutionTrace) > 0 {
			var trace []map[string]interface{}
			if err := json.Unmarshal(tx.AseExecutionTrace, &trace); err == nil {
				for _, step := range trace {
					if step["dag_node_id"] == "account_selection" {
						if edge, ok := step["selected_edge"].(string); ok {
							accountID = edge
						}
					}
					if step["property_key"] == "counterparty" || step["dag_node_id"] == "counterparty_extractor" {
						if edge, ok := step["selected_edge"].(string); ok && edge != "" {
							counterparty = edge
						}
					}
				}
			}
		}

		accountCode := ""
		if accountID != "" {
			if code, ok := accMap[accountID]; ok {
				accountCode = code
			} else {
				accountCode = accountID
			}
		}
		// Fallback to Moroccan Pending Classification Account if unassigned
		if accountCode == "" {
			accountCode = "471000"
		}

		dateStr := ""
		if tx.ParsedDate.Valid {
			dateStr = tx.ParsedDate.Time.Format("020106")
		} else if tx.RawDate.Valid && tx.RawDate.String != "" {
			parsed := false
			for _, layout := range []string{"02/01/2006", "2006-01-02", "02-01-2006", "02/01/06"} {
				if t, err := time.Parse(layout, tx.RawDate.String); err == nil {
					dateStr = t.Format("020106")
					parsed = true
					break
				}
			}
			if !parsed {
				dateStr = strings.ReplaceAll(tx.RawDate.String, "/", "")
			}
		}
		if dateStr == "" {
			dateStr = time.Now().Format("020106")
		}

		desc := ""
		if tx.RawDescription.Valid {
			desc = tx.RawDescription.String
		}

		amountStr := tx.RawAmount
		amountStr = strings.ReplaceAll(amountStr, ",", "")
		amountStr = strings.ReplaceAll(amountStr, " ", "")

		var amount float64
		if f, err := strconv.ParseFloat(amountStr, 64); err == nil {
			amount = f
		}
		if amount < 0 {
			amount = -amount
		}

		direction := ""
		if tx.CashDirection.Valid {
			direction = strings.ToUpper(tx.CashDirection.String)
		}
		if direction == "" {
			lower := strings.ToLower(desc)
			if strings.Contains(lower, "client") || strings.Contains(lower, "virement recu") || strings.Contains(lower, "recu") {
				direction = "INFLOW"
			} else {
				direction = "OUTFLOW"
			}
		}

		desc = strings.ReplaceAll(desc, ";", " ")
		desc = strings.ReplaceAll(desc, "\"", "")
		desc = strings.TrimSpace(desc)
		if len(desc) > 35 {
			desc = desc[:35]
		}

		// Resolve Auxiliary Account (CompteA)
		compteA := extractAuxAccount(desc, counterparty, direction, accountCode)

		// General Account refinement for Bank Journal counterparties
		if direction == "INFLOW" && (accountCode == "711100" || accountCode == "471000") {
			accountCode = "342100"
		} else if direction == "OUTFLOW" && accountCode == "471000" {
			if strings.HasPrefix(compteA, "F_") {
				accountCode = "441100"
			} else {
				accountCode = "619000"
			}
		}

		journal := "BQ"
		piece := fmt.Sprintf("BNK%03d", itemIdx+1)

		if direction == "OUTFLOW" {
			// Outflow (Expense / Payment): Debit the counterpart account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, amount, 0.00))
		} else {
			// Inflow (Revenue / Receipt): Credit the counterpart account
			csvLines = append(csvLines, fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.2f;%.2f",
				journal, dateStr, accountCode, compteA, piece, desc, 0.00, amount))
		}
	}

	csvData := strings.Join(csvLines, "\r\n")
	base64Data := base64.StdEncoding.EncodeToString([]byte(csvData))

	payload := map[string]interface{}{
		"session_id":  sessionID,
		"from_handle": fromHandle,
		"to_handle":   toHandle,
		"attachments": []map[string]interface{}{
			{
				"Name":        "Bank_Reconciliation_Export.csv",
				"ContentType": "text/csv",
				"Content":     base64Data,
			},
		},
	}

	_ = deps.DB.MarkPcmSessionExported(ctx, sessionUUID)

	return payload, "workers.pcm_export", nil
}

// extractAuxAccount formats a string into a valid Sage auxiliary account code (max 10 uppercase alphanumeric chars)
func extractAuxAccount(desc string, counterparty string, direction string, accountCode string) string {
	lowerDesc := strings.ToLower(desc)
	if strings.Contains(lowerDesc, "frais tenue") || strings.Contains(lowerDesc, "agios") || strings.Contains(lowerDesc, "frais dossier") {
		return ""
	}

	rawEntity := counterparty
	if rawEntity == "" {
		cleaned := desc
		for _, prefix := range []string{
			"Virement Client ", "Virement Recu ", "Virement Recu", "Virement ",
			"Paiement CB ", "Paiement ", "Prelevement Facture ", "Prelevement Mensuel ", "Prelevement ",
			"Achats Fournitures ", "Achats ", "Honoraires Cabinet ", "Honoraires ",
		} {
			if strings.HasPrefix(strings.ToLower(cleaned), strings.ToLower(prefix)) {
				cleaned = cleaned[len(prefix):]
				break
			}
		}
		for _, suffix := range []string{" SA", " SARL", " IT Solutions", " Casablanca", " Business", " Construction SA", " Industrie SA"} {
			if idx := strings.Index(strings.ToLower(cleaned), strings.ToLower(suffix)); idx > 0 {
				cleaned = cleaned[:idx]
			}
		}
		rawEntity = strings.TrimSpace(cleaned)
	}

	if rawEntity == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range strings.ToUpper(rawEntity) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	code := b.String()

	prefix := "F_"
	if direction == "INFLOW" || strings.HasPrefix(accountCode, "3") || strings.Contains(lowerDesc, "client") || strings.Contains(lowerDesc, "virement recu") {
		prefix = "C_"
	}

	if strings.HasPrefix(code, "CLIENT") {
		code = strings.TrimPrefix(code, "CLIENT")
	}

	if len(code) > 8 {
		code = code[:8]
	}
	if code == "" {
		return ""
	}

	res := prefix + code
	if len(res) > 10 {
		res = res[:10]
	}
	return res
}

func (t *BookkeepingTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	return NewStateStore(deps.DBPool, deps.Redis)
}
