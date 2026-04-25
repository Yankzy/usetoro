package csvmapping

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/nats-io/nats.go"
)

type LLMColumnMapping struct {
	DateColIdx        int     `json:"date_col_idx"`
	DescriptionColIdx int     `json:"description_col_idx"`
	AmountColIdx      *int    `json:"amount_col_idx"`
	IsSplitAmount     bool    `json:"is_split_amount"`
	DebitColIdx       *int    `json:"debit_col_idx"`
	CreditColIdx      *int    `json:"credit_col_idx"`
	VendorColIdx      *int    `json:"vendor_col_idx"`
	CustomerColIdx    *int    `json:"customer_col_idx"`
	CustomColIdx      *int    `json:"custom_col_idx"`
	IsExpensePositive bool    `json:"is_expense_positive"`
	ConfidenceScore   float64 `json:"confidence_score"`
	IsAmbiguous       bool    `json:"is_ambiguous"`
	AmbiguityReason   string  `json:"ambiguity_reason"`
	PolaritySign      string  `json:"polarity_sign"`
	SourceAccount     string  `json:"source_account"`
}

type CSVMappingTaskPayload struct {
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
	RT      *agent.Runtime
	Queries *database.Queries
}

const AgentName = "csv-mapping-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a CSVMappingAgent
	a.RT = agent.NewRuntime(env.Logger, env.Bus, env.Config)
	a.Queries = env.Queries

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] csv-mapping-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			a.Logger.Error("Transient error processing message, replying with FAILURE", "error", err)
			if strings.Contains(err.Error(), "insufficient funds") {
				_, _ = a.Queries.LogStalledMessage(context.Background(), database.LogStalledMessageParams{
					AgentDid:        env.Config.DID,
					OriginalSubject: msg.Subject,
					Payload:         msg.Data,
					ErrorReason:     "Paywall deadlocked: " + err.Error(),
				})
				msg.Term()
				return
			}

			// Parse original envelope again to reply gracefully
			var origEnv core.Envelope
			if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil {
				a.ReplyFailure(msg, origEnv, err)
			} else {
				msg.Nak()
			}
			return
		}

		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, handler)
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

	var payload CSVMappingTaskPayload
	if err := core.UnmarshalTaskPayload(task.Payload, &payload); err != nil {
		a.Logger.Error("Failed to parse task payload", "error", err)
	}

	a.Logger.Info("🧠 [DEBUG] csv_mapping executeTask started",
		"session_id", payload.SessionID,
		"realm_id", payload.RealmID,
		"input_rows", len(payload.Rows),
	)

	// Fallback for generic file ingestion path which uses upload_id instead of session_id
	if payload.SessionID == "" {
		var rawMap map[string]interface{}
		_ = core.UnmarshalTaskPayload(task.Payload, &rawMap)
		if uid, ok := rawMap["upload_id"].(string); ok {
			payload.SessionID = uid
			a.Logger.Info("🧠 [DEBUG] csv_mapping fell back to upload_id as session_id", "id", uid)
		}
	}

	// IMPORTANT:
	// - `task.ID` is the Orchestrator workflow instance UUID (row id in toro_core.workflows).
	// - `payload.SessionID` is business context (upload/session id) carried inside the payload.
	// Redux tracing + Rollup must use the workflow instance UUID, not the session id.
	a.Logger.Info("🧠 Processing AI mapping via Redux global wrapper",
		"workflow_id", task.ID,
		"session_id", payload.SessionID,
		"rows", len(payload.Rows),
	)

	var workflowID pgtype.UUID
	_ = workflowID.Scan(task.ID)

	// Redux configuration: schema + RBAC boundaries for this agent
	schema := task.WorkflowSchema
	if strings.TrimSpace(schema) == "" {
		schema = a.Cfg.WorkflowSchema
	}

	wfCfg := agent.WorkflowConfig{
		SchemaString: schema,
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/status", "/mapped_rows", "/is_ambiguous", "/ambiguity_reason", "/polarity_sign", "/route"},
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

		mapping, err := a.MapRowsUsingLLM(context.Background(), task, payload.Rows)
		if err != nil {
			return nil, err
		}

		a.Logger.Info("🧠 [DEBUG] LLM Mapping Result",
			"is_ambiguous", mapping.IsAmbiguous,
			"polarity_sign", mapping.PolaritySign,
		)

		finalRows := ParseRows(payload, mapping)
		finalRowsJSON, _ := json.Marshal(finalRows)

		statusVal, _ := json.Marshal("COLUMNS_MAPPED")
		patch1 := fmt.Sprintf(`{"op": "add", "path": "/status", "value": %s}`, string(statusVal))
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/mapped_rows", "value": %s}`, string(finalRowsJSON))

		isAmbiguousVal, _ := json.Marshal(mapping.IsAmbiguous)
		patch3 := fmt.Sprintf(`{"op": "add", "path": "/is_ambiguous", "value": %s}`, string(isAmbiguousVal))

		reasonVal, _ := json.Marshal(mapping.AmbiguityReason)
		patch4 := fmt.Sprintf(`{"op": "add", "path": "/ambiguity_reason", "value": %s}`, string(reasonVal))

		polarityVal, _ := json.Marshal(mapping.PolaritySign)
		patch5 := fmt.Sprintf(`{"op": "add", "path": "/polarity_sign", "value": %s}`, string(polarityVal))

		// Route 1 = Suspended (Ambiguous), Route 0 = Proceed
		route := 0
		if mapping.IsAmbiguous {
			route = 1
		}
		routeVal, _ := json.Marshal(route)
		patch6 := fmt.Sprintf(`{"op": "add", "path": "/route", "value": %s}`, string(routeVal))

		patches = append(patches, []byte(patch1), []byte(patch2), []byte(patch3), []byte(patch4), []byte(patch5), []byte(patch6))

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
			Data:      json.RawMessage(nextState),
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)
		// Agents always return proofs to the Orchestrator inbox.
		// The Orchestrator advances the workflow state machine to the next step.
		targetTopic := workflows.OrchestratorInbox
		a.Logger.Info("🚀 Publishing validated proof to Orchestrator", "topic", targetTopic, "data_length", len(finalBytes))

		if pubErr := a.Bus.Publish(targetTopic, finalBytes); pubErr != nil {
			a.Logger.Error("Failed to publish proof", "error", pubErr)
			return pubErr
		}
		return nil
	}

	return a.ExecuteLocalWorkflow(
		context.Background(),
		task.ID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

func ParseRows(payload CSVMappingTaskPayload, mapping *LLMColumnMapping) map[string]RawRow {
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
		}

		if mapping.AmountColIdx != nil {
			row.Amount = cleanArtifacts(fieldAt(rec, *mapping.AmountColIdx))
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

func (a *CSVMappingAgent) MapRowsUsingLLM(ctx context.Context, task core.TaskDefinition, rows [][]string) (*LLMColumnMapping, error) {
	prompt := BuildUserPrompt(rows)

	respText, err := a.RT.ExecWithPaging(ctx, prompt, task.SystemPrompt, nil, nil)
	a.Logger.Info("🧠 [DEBUG] LLM Mapping Response Received",
		"workflow_id", task.ID,
		"response", respText,
		"error", err,
	)
	if err != nil {
		return nil, err
	}

	return ExtractJSONToMapping(respText)
}

func ExtractJSONToMapping(respText string) (*LLMColumnMapping, error) {
	// Robust extraction: find the first { and the last }
	firstIdx := strings.Index(respText, "{")
	lastIdx := strings.LastIndex(respText, "}")

	if firstIdx == -1 || lastIdx == -1 || lastIdx <= firstIdx {
		return nil, fmt.Errorf("no valid JSON object found in LLM response (first={ at %d, last=} at %d)", firstIdx, lastIdx)
	}

	extracted := strings.TrimSpace(respText[firstIdx : lastIdx+1])

	// Pre-parse validation logging
	sampleLen := 40
	startSample := extracted
	if len(extracted) > sampleLen {
		startSample = extracted[:sampleLen] + "..."
	}
	endSample := extracted
	if len(extracted) > sampleLen {
		endSample = "..." + extracted[len(extracted)-sampleLen:]
	}

	fmt.Printf("🔍 [DEBUG] Extracted JSON for Unmarshal (len=%d): %s --- %s\n", len(extracted), startSample, endSample)

	var mapping LLMColumnMapping
	if err := json.Unmarshal([]byte(extracted), &mapping); err != nil {
		return nil, fmt.Errorf("failed to unmarshal extracted JSON (len=%d): %w", len(extracted), err)
	}

	return &mapping, nil
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

func resolveAmountColumnIndex(rows [][]string, mapping *LLMColumnMapping) (int, bool) {
	if mapping != nil && mapping.AmountColIdx != nil {
		return *mapping.AmountColIdx, true
	}
	idx, ok := inferAmountColumnIndex(rows)
	return idx, ok
}

func inferAmountColumnIndex(rows [][]string) (int, bool) {
	if len(rows) < 2 {
		return 0, false
	}

	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return 0, false
	}

	nonEmptyCounts := make([]int, maxCols)
	numericCounts := make([]int, maxCols)

	for i := 1; i < len(rows); i++ { // Skip header row
		for c := 0; c < maxCols; c++ {
			val := cleanArtifacts(fieldAt(rows[i], c))
			if val == "" {
				continue
			}
			nonEmptyCounts[c]++
			if looksLikeAmountValue(val) {
				numericCounts[c]++
			}
		}
	}

	candidates := make([]int, 0)
	for c := 0; c < maxCols; c++ {
		if nonEmptyCounts[c] == 0 || numericCounts[c] == 0 {
			continue
		}
		ratio := float64(numericCounts[c]) / float64(nonEmptyCounts[c])
		if ratio >= 0.7 {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return 0, false
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}

	header := []string{}
	if len(rows) > 0 {
		header = rows[0]
	}

	bestIdx := -1
	bestScore := -1
	for _, c := range candidates {
		score := amountHeaderScore(cleanArtifacts(fieldAt(header, c)))
		if score > bestScore {
			bestScore = score
			bestIdx = c
		}
	}

	if bestIdx >= 0 && bestScore > 0 {
		return bestIdx, true
	}

	return 0, false
}

func amountHeaderScore(header string) int {
	h := strings.ToLower(strings.TrimSpace(header))
	score := 0
	if strings.Contains(h, "amount") || strings.Contains(h, "amt") {
		score += 3
	}
	if strings.Contains(h, "value") || strings.Contains(h, "sum") || strings.Contains(h, "total") {
		score += 2
	}
	if strings.Contains(h, "paid") || strings.Contains(h, "payment") {
		score += 1
	}
	return score
}

func looksLikeAmountValue(val string) bool {
	s := strings.TrimSpace(val)
	if s == "" {
		return false
	}

	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, "£", "")
	s = strings.ReplaceAll(s, " ", "")

	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
	}

	if s == "" {
		return false
	}

	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func BuildUserPrompt(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString("Analyze the following sample rows from a bank statement CSV and determine the 0-based column indices for Date, Description, Amount, Customer, and Vendor.\n\n")
	sb.WriteString("SPLIT AMOUNTS: If the statement uses separate Debit and Credit columns, you MUST:\n")
	sb.WriteString("1. Set `is_split_amount` to true.\n")
	sb.WriteString("2. Provide the exact indices for `debit_col_idx` and `credit_col_idx`.\n")
	sb.WriteString("3. Set `amount_col_idx` to null (or -1 if strictly required by the schema, but null is preferred).\n\n")
	sb.WriteString("NULL HANDLING: For any optional field (debit, credit, vendor, customer, custom) that is NOT present, you MUST set its index to `null` in the JSON response. Do NOT use 0 as a placeholder for null.\n\n")
	sb.WriteString("AMBIGUITY (Polarity): The data is AMBIGUOUS if the numeric columns lack clear indicators (minus signs, brackets, or separate Debit/Credit columns). This happens even if a 'Category' or 'Type' column exists.\n")
	sb.WriteString("Mark `is_ambiguous: true` if there is only a single 'Amount' column with NO minus signs '-' and NO parentheses '()'.\n")
	sb.WriteString("CRITICAL: Ignore 'Category', 'Status', or 'Account' columns when determining structural ambiguity. If the numbers themselves are all positive without a separate 'Type' signifier, it is AMBIGUOUS.\n")
	sb.WriteString("If this occurs, set `is_ambiguous` to true and explain it in `ambiguity_reason` (e.g., 'structural ambiguity: all amounts are positive without polarity indicators').\n\n")
	sb.WriteString("DO NOT USE DISCRIPTION TO DETERMINE AMBIGUITY. YOU MUST STRICTLY USE THE NUMBERS SIGNS, PARENTHESES, OR DEBIT/CREDIT COLUMNS.\n")
	sb.WriteString("POLARITY SIGN: Determine how expenses/outflows are indicated. Set `polarity_sign` to one of:\n")
	sb.WriteString("- `minus`: Expenses have a negative sign (e.g., -10.00).\n")
	sb.WriteString("- `brackets`: Expenses are in parentheses (e.g., (10.00)).\n")
	sb.WriteString("- `none`: There are no indicators (all numbers are positive).\n\n")
	sb.WriteString("- If polarity is none, then set `is_ambiguous` to true and explain it in `ambiguity_reason` (e.g., 'structural ambiguity: all amounts are positive without polarity indicators').\n\n")
	sb.WriteString("If you can determine the source account (the bank or credit card used), set `source_account` to its name.\n\n")
	sb.WriteString("If there's no bank account but you can clearly the csv is from Shopify or Stripe set the `source_account` to 'Shopify' or 'Stripe'.\n\n")

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
