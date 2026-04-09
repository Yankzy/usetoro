package csvmapping

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/nats-io/nats.go"
)

type LLMColumnMapping struct {
	DateColIdx        int     `json:"date_col_idx"`
	DescriptionColIdx int     `json:"description_col_idx"`
	AmountColIdx      int     `json:"amount_col_idx"`
	IsSplitAmount     bool    `json:"is_split_amount"`
	DebitColIdx       *int    `json:"debit_col_idx"`
	CreditColIdx      *int    `json:"credit_col_idx"`
	VendorColIdx      *int    `json:"vendor_col_idx"`
	CustomerColIdx    *int    `json:"customer_col_idx"`
	CustomColIdx      *int    `json:"custom_col_idx"`
	IsExpensePositive bool    `json:"is_expense_positive"`
	ConfidenceScore   float64 `json:"confidence_score"`
	Reasoning         string  `json:"reasoning"`
}

type csvMappingTaskPayload struct {
	SessionID string     `json:"session_id"`
	RealmID   string     `json:"realm_id"`
	Rows      [][]string `json:"rows"`
}

type RawRow struct {
	SessionID   string
	RealmID     string
	Date        string
	Description string
	Amount      string
	Vendor      string
	Customer    string
}

type CSVMappingAgent struct {
	*agent.BaseAgent
	rt      *agent.Runtime
	queries *database.Queries
}

const AgentName = "csv-mapping-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a CSVMappingAgent
	a.rt = agent.NewRuntime(env.Logger, env.Bus, env.Config, env.Memory)
	a.queries = env.Queries

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] csv-mapping-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			a.Logger.Error("Transient error processing message, nacking", "error", err)
			if strings.Contains(err.Error(), "insufficient funds") {
				_, _ = a.queries.LogStalledMessage(context.Background(), database.LogStalledMessageParams{
					AgentDid:        env.Config.DID,
					OriginalSubject: msg.Subject,
					Payload:         msg.Data,
					ErrorReason:     "Paywall deadlocked: " + err.Error(),
				})
				msg.Term()
				return
			}
			msg.Nak()
			return
		}

		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, env.Memory, handler)
	return &a
}

func (a *CSVMappingAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP {
		a.Logger.Error("Received non-CFP", "performative", env.Performative)
		return nil
	}

	a.Logger.Info("📨 Received CFP", "sender", env.SenderDID, "cid", env.ConversationID)

	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "10s",
	}

	replyEnv, _ := core.NewEnvelope(
		uuid.New().String(),
		a.Cfg.DID,
		env.SenderDID,
		env.ConversationID,
		core.PROPOSE,
		proposal,
	)
	replyEnv.Signature = a.KP.Sign(replyEnv.Body)
	replyBytes, _ := json.Marshal(replyEnv)

	if err := a.Bus.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes); err != nil {
		a.Logger.Error("Failed to publish proposal", "error", err)
		return err
	}

	// Auto execute
	return a.executeTask(env)
}

func (a *CSVMappingAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	json.Unmarshal(cfpEnv.Body, &task)

	var payload csvMappingTaskPayload
	_ = json.Unmarshal(task.Payload, &payload)

	// Fallback for generic file ingestion path which uses upload_id instead of session_id
	if payload.SessionID == "" {
		var rawMap map[string]interface{}
		_ = json.Unmarshal(task.Payload, &rawMap)
		if uid, ok := rawMap["upload_id"].(string); ok {
			payload.SessionID = uid
		}
	}

	a.Logger.Info("🧠 Processing AI mapping via Redux global wrapper", "session", payload.SessionID, "rows", len(payload.Rows))

	var workflowID pgtype.UUID
	_ = workflowID.Scan(payload.SessionID)

	// Redux configuration: schema + RBAC boundaries for this agent
	wfCfg := agent.WorkflowConfig{
		SchemaString: a.Cfg.WorkflowSchema,
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/status", "/mapped_rows"},
			},
		},
	}

	// Fault-aware LLM callback: receives previous Redux faults so the LLM can self-correct
	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		if len(previousFaults) > 0 {
			a.Logger.Warn("⚠️ Redux faults from previous attempt, retrying LLM", "faults", len(previousFaults))
		}

		// Implement Optimistic Concurrency limit by prefixing a test patch
		var stateObj map[string]interface{}
		_ = json.Unmarshal(baseState, &stateObj)
		var patches []json.RawMessage

		if statusVal, exists := stateObj["status"]; exists {
			b, _ := json.Marshal(statusVal)
			patches = append(patches, []byte(fmt.Sprintf(`{"op": "test", "path": "/status", "value": %s}`, string(b))))
		}

		mapping, err := a.mapRowsUsingLLM(context.Background(), payload.Rows)
		if err != nil {
			return nil, err
		}

		finalRows := parseRows(payload, mapping)
		finalRowsJSON, _ := json.Marshal(finalRows)

		patch1 := `{"op": "add", "path": "/status", "value": "COLUMNS_MAPPED"}`
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/mapped_rows", "value": %s}`, string(finalRowsJSON))

		patches = append(patches, []byte(patch1), []byte(patch2))

		return patches, nil
	}

	// onComplete: publish proof to JetStream only after Redux validates the state
	onComplete := func(nextState []byte) error {
		a.Logger.Info("✅ Redux-validated state received, publishing proof")

		// Extract mapped rows from the validated state for the proof payload
		var validatedState map[string]json.RawMessage
		if err := json.Unmarshal(nextState, &validatedState); err != nil {
			return fmt.Errorf("failed to parse validated state: %w", err)
		}

		proof := core.Proof{
			TaskID:    task.ID,
			Type:      core.ProofAPI,
			Data:      validatedState["mapped_rows"],
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)
		targetTopic := a.Cfg.PublishTo
		a.Logger.Info("🚀 Publishing validated proof to JetStream", "topic", targetTopic, "data_length", len(finalBytes))

		if pubErr := a.Bus.Publish(targetTopic, finalBytes); pubErr != nil {
			a.Logger.Error("Failed to publish proof", "error", pubErr)
			return pubErr
		}
		return nil
	}

	return a.ExecuteGlobalWorkflow(
		context.Background(),
		a.queries,
		workflowID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

func parseRows(payload csvMappingTaskPayload, mapping *LLMColumnMapping) map[string]RawRow {
	finalRows := make(map[string]RawRow)
	for i := 1; i < len(payload.Rows); i++ {
		rec := payload.Rows[i]
		if len(rec) == 0 {
			continue
		}

		row := RawRow{
			SessionID:   payload.SessionID,
			RealmID:     payload.RealmID,
			Date:        cleanArtifacts(fieldAt(rec, mapping.DateColIdx)),
			Description: cleanArtifacts(fieldAt(rec, mapping.DescriptionColIdx)),
			Amount:      cleanArtifacts(fieldAt(rec, mapping.AmountColIdx)),
		}

		if mapping.VendorColIdx != nil {
			row.Vendor = cleanArtifacts(fieldAt(rec, *mapping.VendorColIdx))
		}
		if mapping.CustomerColIdx != nil {
			row.Customer = cleanArtifacts(fieldAt(rec, *mapping.CustomerColIdx))
		}

		if mapping.IsSplitAmount && mapping.DebitColIdx != nil && mapping.CreditColIdx != nil {
			deb := cleanArtifacts(fieldAt(rec, *mapping.DebitColIdx))
			cred := cleanArtifacts(fieldAt(rec, *mapping.CreditColIdx))
			if deb != "" {
				row.Amount = deb
			} else if cred != "" {
				row.Amount = cred
			}
		}

		if row.Description != "" && row.Amount != "" {
			rowID := fmt.Sprintf("row_%d", i)
			finalRows[rowID] = row
		}
	}
	return finalRows
}

func (a *CSVMappingAgent) mapRowsUsingLLM(ctx context.Context, rows [][]string) (*LLMColumnMapping, error) {
	prompt := buildUserPrompt(rows)

	systemInstruction := a.Cfg.SystemPrompt
	fullPrompt := fmt.Sprintf("%s\n\n%s", systemInstruction, prompt)

	respText, err := a.rt.ExecWithPaging(ctx, fullPrompt, nil, nil)
	if err != nil {
		return nil, err
	}

	return extractJSONToMapping(respText)
}

func extractJSONToMapping(respText string) (*LLMColumnMapping, error) {
	firstIdx := strings.Index(respText, "{")
	lastIdx := strings.LastIndex(respText, "}")
	if firstIdx != -1 && lastIdx != -1 && lastIdx > firstIdx {
		respText = respText[firstIdx : lastIdx+1]
	}

	var mapping LLMColumnMapping
	err := json.Unmarshal([]byte(respText), &mapping)
	return &mapping, err
}

func fieldAt(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return rec[idx]
}

func cleanArtifacts(val string) string {
	cleaned := strings.ReplaceAll(val, "*", "")
	return strings.TrimSpace(cleaned)
}

func buildUserPrompt(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString("Analyze the following sample rows from a bank statement CSV and determine the 0-based column indices for Date, Description, Amount, Customer, and Vendor (if distinct from description).\n\n")
	sb.WriteString("Also determine if the amounts are split into separate Debit/Credit columns instead of a single Amount column. If so, set is_split_amount to true and provide those indices instead of amount_col_idx.\n\n")
	sb.WriteString("Crucially, determine the sign convention (is_expense_positive). Look at obvious expenses (like 'AMZN', 'AWS', 'Starbucks', 'Uber'). If their amount is a positive number, set `is_expense_positive` to true. If their amount is negative (e.g. -45.00), set it to false.\n\n")
	sb.WriteString("You MUST assign a `confidence_score` (0 to 1) representing how certain you are of this mapping. If you mainly see header rows and cannot find clear transaction rows to determine the sign convention, return a low confidence score.\n\n")
	sb.WriteString("Sample Data (Up to 20 valid rows):\n")

	var validRows int
	for i, row := range rows {
		cols := 0
		for _, col := range row {
			if strings.TrimSpace(col) != "" {
				cols++
			}
		}
		if cols < 2 {
			continue // Skip empty or single-column title rows
		}

		rowStr := strings.Join(row, " | ")
		sb.WriteString(fmt.Sprintf("Row %d: %s\n", i, rowStr))
		validRows++
		if validRows >= 20 {
			break
		}
	}

	return sb.String()
}
