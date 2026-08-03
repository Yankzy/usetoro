package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
			"Please contact the business owner to ask for clarification to properly categorize this transaction. You can use the LookupClient tool if needed to find their contact details, and use the SendEmail tool as the default communication channel.\n\n"+
			"CRITICAL INSTRUCTION: You are receiving this message through an internal system channel. Do NOT reply directly to this message with the email text you want to send. A direct conversational reply will only be logged internally and will NEVER be seen by the client. To contact the client, you MUST explicitly invoke the SendEmail tool. Use the SendEmail tool to send the actual email, and then provide a brief internal summary (e.g., 'I have emailed the client.') as your conversational response.\n\n"+
			"IMPORTANT: Before sending an email, you MUST use the FetchCommunicationHistory tool to check if we have already sent an email to this client about this exact transaction (same amount, customer, and description) within the last 24 hours. If an email has already been sent about this specific transaction recently, DO NOT send a duplicate email.\n\n"+
			"When the user replies back with the requested information or clarification, you MUST use the UpdateTransactionClassification tool. This tool will pass the user's answer back to the DAG and unblock it so it can proceed. Use this tool only when you have gathered enough context from the user to confidently resolve the hold reason. \n\n"+
			"CRITICAL RULE: NEVER use the UpdateTransactionClassification tool just to report that you have contacted the user. Only use it when the user has ACTUALLY replied with the answer. If you are just sending an email to ask for clarification, DO NOT use UpdateTransactionClassification. Just use SendEmail and finish your turn.\n\n"+
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

func (t *BookkeepingTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	return NewStateStore(deps.DBPool, deps.Redis)
}
