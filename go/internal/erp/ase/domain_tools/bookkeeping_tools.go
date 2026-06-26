package domain_tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

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

	cfg := ase.GetConfig(a.TenantID, a.RealmID, a.DagName)
	if cfg != nil {
		if nodeConfig, ok := cfg.DAG.Nodes[startNodeID]; ok && nodeConfig.Email != nil {
			tmpl, err := template.New("email").Parse(nodeConfig.Email.BodyText)
			if err == nil {
				var bodyBuf bytes.Buffer

				templateData := make(map[string]interface{})
				for k, v := range a.Payload {
					parts := strings.Split(k, "_")
					for i, p := range parts {
						if len(p) > 0 {
							parts[i] = strings.ToUpper(p[:1]) + p[1:]
						}
					}
					capitalizedKey := strings.Join(parts, "")
					templateData[capitalizedKey] = v
				}

				_ = tmpl.Execute(&bodyBuf, templateData)
				bodyText := bodyBuf.String()

				toEmail := ""
				if companyInfo, err := deps.DB.GetCompanyInfo(ctx, a.RealmID); err == nil && companyInfo.Email.Valid {
					toEmail = companyInfo.Email.String
				}

				sessionID := ""
				if sid, ok := a.Payload["session_id"].(string); ok {
					sessionID = sid
				}

				if toEmail != "" {
					var entityUUID pgtype.UUID
					_ = entityUUID.Scan(entityID)

					sessionManager := conversation.NewSessionManager(deps.DB, deps.Logger)
					sess, err := sessionManager.CreateSession(ctx, conversation.FindOrCreateParams{
						EntityID:          entityUUID,
						Source:            "email",
						ParticipantHandle: toEmail,
						ToroHandle:        "sarah@usetoro.io", // Or whatever the from_handle is
						Subject:           nodeConfig.Email.Subject,
						SystemPrompt:      "",
					})

					convoSessionID := sessionID // fallback
					if err == nil && sess.ID.Valid {
						convoSessionID = uuid.UUID(sess.ID.Bytes).String()
						deps.Logger.Info("created new conversation session for deterministic email", "session_id", convoSessionID, "ase_node_id", a.NodeID)
					} else {
						deps.Logger.Error("failed to create conversation session for deterministic email", "error", err, "ase_node_id", a.NodeID)
					}

					response := map[string]string{
						"body_text":     bodyText,
						"from_handle":   "sarah@usetoro.io",
						"to_handle":     toEmail,
						"source":        "email",
						"subject":       nodeConfig.Email.Subject,
						"session_id":    convoSessionID,
						"entity_id":     entityID,
						"custom_msg_id": fmt.Sprintf("ase:%s:bookkeeping:%s", a.NodeID, a.DagName),
					}
					proofData, _ := json.Marshal(response)

					proof := core.Proof{
						Type: "outgoing_chat",
						Data: proofData,
					}
					proofBytes, _ := json.Marshal(proof)

					envelope := core.Envelope{
						ID:           uuid.New().String(),
						Performative: core.INFORM,
						Body:         proofBytes,
					}
					envelopeBytes, _ := json.Marshal(envelope)

					if deps.NC != nil {
						_ = deps.NC.Publish("proof.outgoing.chat", envelopeBytes)
					}
				}

				return nil, nil
			} else {
				deps.Logger.Error("bookkeeping_tool: failed to parse deterministic email template", "error", err)
			}
		}
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
