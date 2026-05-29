TAP Agent Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Generate a complete, compilable `agent.go` file for a new internal TAP agent, plus a matching `defaults.yml` snippet.

Agent Specification

- Agent Display Name: "[AGENT_DISPLAY_NAME]"
- Package Name: `[PACKAGE_NAME]` (directory: `tap/agents/[PACKAGE_NAME]/`)
- `internal_module` key: `"[INTERNAL_MODULE_KEY]"`
- `activity_type`: `"[ACTIVITY_TYPE]"` (must start with `agents.`)
- Purpose / Business Logic:
  [Describe exact behavior and side effects.]
- Input Envelope Shape:
  [Describe what arrives in `env.Body` and expected performative(s).]
- Output Proof Shape:
  [Describe what to publish in `core.Proof.Data` and when.]
- Uses Redux (`ExecuteGlobalWorkflow`): `[YES|NO]`
- If Redux=YES, allowed state paths for this agent DID:
  `["/status", "/...optional_paths..."]`
- Dependencies required in `defaults.yml`:
  - `database: true|false`
  - `db_queries: true|false`
  - `entity_resolver: true|false`
- System Prompt:
  [Provide a concrete system prompt, or "none" if not needed.]

Framework Contracts You MUST Follow

1. Registration + Constructor

```go
import "github.com/Yankzy/usetoro/tap/agents"

func init() {
    agents.Register("[INTERNAL_MODULE_KEY]", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable
```

2. Use `agent.BaseAgent` (do not hand-roll subscription wiring)

```go
// package: github.com/Yankzy/usetoro/tap/pkg/agent
func NewBaseAgent(
    logger *slog.Logger,
    bus core.EventBus,
    cfg core.AgentConfig,
    handler nats.MsgHandler,
) *BaseAgent
```

Important runtime behavior:
- `BaseAgent` derives DID, queue group, durable name, and canonical task queue routing from `activity_type`.
- Do NOT hardcode queue group/durable names inside the agent.
- Public task queue routing is orchestrator-owned.

3. Constructor Environment + Dependencies

```go
// package: github.com/Yankzy/usetoro/tap/pkg/core
type Environment struct {
    Logger         *slog.Logger
    Bus            EventBus
    Config         AgentConfig
    Queries        *database.Queries
    DBPool         *pgxpool.Pool
    EntityResolver *ai.EntityResolver
}
```

Dependency rules:
- If you use `env.Queries`, require `db_queries: true`.
- If you use `env.DBPool`, require `database: true`.
- If you use `env.EntityResolver`, require `entity_resolver: true`.

4. Handler Rules (Ack/Nak/Term)

Your handler must:
- implement poison-pill guard (`msg.Metadata().NumDelivered > 3` -> `msg.Term()`)
- unmarshal `core.Envelope`
- validate performative using `core.IsValidPerformative`
- ignore unsupported performatives by returning `nil`
- return `error` only for transient failures

Suggested poison-pill guard and FAILURE reply:

```go
meta, metaErr := msg.Metadata()
if metaErr == nil && meta.NumDelivered > 3 {
    a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
    msg.Term()
    return
}

if err := a.handleCFP(msg); err != nil {
    // Reply gracefully with FAILURE to the sender and Ack the NATS msg to prevent loops
    var origEnv core.Envelope
    if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil {
        a.ReplyFailure(msg, origEnv, err)
    } else {
        msg.Nak()
    }
    return
}
```

5. Orchestrator Handshake Compatibility

Current orchestrator behavior may not always send a follow-up `ACCEPT_PROPOSAL` for negotiated steps yet.

For `CFP` flows, implement this pattern:
- parse CFP
- send `PROPOSE` to `core.BuildAgentInbox(env.SenderDID)`
- execute from the same CFP envelope immediately

Also support `ACCEPT_PROPOSAL` by routing it to the same execute path for forward compatibility.

6. TAP Envelope + Proof Contracts

```go
type Envelope struct {
    ID             string          `json:"id"`
    Timestamp      time.Time       `json:"ts"`
    SenderDID      string          `json:"src"`
    ReceiverDID    string          `json:"dst,omitempty"`
    Performative   Performative    `json:"perf"`
    ConversationID string          `json:"cid,omitempty"`
    Body           json.RawMessage `json:"body"`
    Signature      string          `json:"sig"`
}

type Proof struct {
    TaskID    string          `json:"task_id"`
    Type      ProofType       `json:"type"`
    Timestamp int64           `json:"ts"`
    Data      json.RawMessage `json:"data"`
    Signature string          `json:"sig"`
}
```

Completion convention:
- publish `core.Envelope{perf: inform, cid: original conversation id, body: core.Proof}` to `workflows.OrchestratorInbox` (`"orchestrator.inbox"`).

7. TaskDefinition Shape (Orchestrator dispatch body)

```go
type TaskDefinition struct {
    ID             string
    Domain         string
    Complexity     core.TaskComplexity
    Reward         int64
    Currency       string
    Payload        json.RawMessage
    WorkflowSchema string
    SystemPrompt   string
    ExpiresAt      int64
}
```

Notes:
- `task.ID` is the workflow instance UUID.

8. agent.Runtime (LLM Bridge)

Agents use `agent.Runtime` to interact with LLMs. Initialize it in `NewAgent` with `agent.NewRuntime(env.Logger, env.Bus, env.Config)`.

Primary execution methods incoming via `a.RT`:
- `a.RT.Exec(ctx, prompt, systemPrompt)`: Simple text-in, text-out reasoning.
- `a.RT.ExecWithPaging(ctx, prompt, systemPrompt, pages, fetcher)`: Advanced tool-calling execution. Use `""` for systemPrompt if using the defaults.

9. Redux Path (only if `Uses Redux = YES`)

Use Redux when you need to perform validated state updates.

- **`ExecuteGlobalWorkflow(...)`**: STATEFUL. Use this to update a global workflow instance in the database. Requires `workflowID` parsed from `task.ID`.
- **`ExecuteLocalWorkflow(...)`**: STATELESS / VALIDATED. Use this to perform locally validated processing (schema + RBAC) and return a proof, without persisting to a global workflow row.

Both take:
- `agent.WorkflowConfig` with `SchemaString` + strict `RBAC` allowed prefixes
- retry-aware LLM callback signature:

```go
func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error)
```

`onComplete` semantics (important):
- called only after Redux reduction succeeds.
- use `onComplete` for publishing proof envelopes to `workflows.OrchestratorInbox`.

10. One-shot Learning Example (from `tap/agents/csv_mapping/agent.go`)

Use this as a style anchor. Mirror the flow and structure, then swap in your own domain types.

```go
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
				a.Cfg.DID: task.RBACPolicy,
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

		// Optimistic Concurrency Control (OCC)
		if statusVal, exists := stateObj["status"]; exists {
			b, _ := json.Marshal(statusVal)
			patches = append(patches, []byte(fmt.Sprintf(`{"op": "test", "path": "/status", "value": %s}`, string(b))))
		}

		llmPatches, err := a.MapRowsUsingLLM(context.Background(), task, payload.Rows)
		if err != nil {
			return nil, err
		}

		a.Logger.Info("🧠 [DEBUG] LLM Patches Result", "patch_count", len(llmPatches))

		// Find /columns_mapped patch to run ParseRows
		var mapping *LLMColumnMapping
		for _, p := range llmPatches {
			var patchObj struct {
				Path  string          `json:"path"`
				Value json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(p, &patchObj); err == nil {
				if patchObj.Path == "/columns_mapped" {
					mapping = &LLMColumnMapping{}
					if err := json.Unmarshal(patchObj.Value, mapping); err != nil {
						return nil, fmt.Errorf("failed to parse /columns_mapped value: %w", err)
					}
					break
				}
			}
		}

		if mapping == nil {
			return nil, fmt.Errorf("LLM did not provide a /columns_mapped patch")
		}

		finalRows := ParseRows(payload, mapping)
		finalRowsJSON, _ := json.Marshal(finalRows)
		mappedRowsPatch := []byte(fmt.Sprintf(`{"op": "add", "path": "/mapped_rows", "value": %s}`, string(finalRowsJSON)))

		patches = append(patches, llmPatches...)
		patches = append(patches, mappedRowsPatch)

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

func (a *CSVMappingAgent) MapRowsUsingLLM(ctx context.Context, task core.TaskDefinition, rows [][]string) ([]json.RawMessage, error) {
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

	return ExtractJSONPatches(respText)
}

func ExtractJSONPatches(respText string) ([]json.RawMessage, error) {
	firstIdx := strings.Index(respText, "[")
	lastIdx := strings.LastIndex(respText, "]")

	if firstIdx == -1 || lastIdx == -1 || lastIdx <= firstIdx {
		return nil, fmt.Errorf("no valid JSON array found in LLM response (first=[ at %d, last=] at %d)", firstIdx, lastIdx)
	}

	extracted := strings.TrimSpace(respText[firstIdx : lastIdx+1])

	var patches []json.RawMessage
	if err := json.Unmarshal([]byte(extracted), &patches); err != nil {
		return nil, fmt.Errorf("failed to unmarshal extracted JSON array: %w", err)
	}

	return patches, nil
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
```

Critical behaviors to copy from this one-shot:
- `CFP` -> `PROPOSE` -> execute immediately.
- Use `core.UnmarshalTaskPayload` for generic payload extraction.
- Redux callback applies constrained RFC6902 patches.
- `onComplete` publishes proof to `workflows.OrchestratorInbox`.
- `ReplyFailure` on errors to prevent NATS stalling.

11. `defaults.yml` Snippet Rules

Generate a matching snippet for `go/internal/config/defaults.yml`.
Include:
- `name`, `model`, `engine: internal`, `internal_module`, `activity_type`
- `dependencies` including `db_queries`

Do NOT include:
- `system_prompt` (this is now defined at the workflow step level)
- `workflow_schema` (this is now defined at the workflow step level)
- `task_queue`
- queue group
- durable name

Template:

```yaml
- did: "did:toro:agent:csv_mapping_1"
  name: "CSV Mapping Agent"
  model: "gpt-5.4-mini"
  engine: "internal"
  internal_module: "csv-mapping-agent"
  activity_type: "agents.accounting.map_csv"
  task_queue: "tasks.accounting.1.map_csv"
  system_prompt: |
    You are an expert data analyst parsing raw bank statement CSV headers. Analyze the columns and map them to standard accounting fields. You MUST reply with valid JSON.
    The JSON must strictly conform to this schema:
    {
      "date_col_idx": <int>,
      "description_col_idx": <int>,
      "amount_col_idx": <int or null>,
        "is_split_amount": <bool>,
        "debit_col_idx": <int or null>,
        "credit_col_idx": <int or null>,
        "vendor_col_idx": <int or null>,
        "customer_col_idx": <int or null>,
        "custom_col_idx": <int or null>,
        "confidence_score": <float 0 to 1>,
        "is_ambiguous": <bool>,
        "ambiguity_reason": <string or null>,
        "reasoning": <string explaining the mapping choices>
      }

      SPLIT AMOUNTS: If the statement uses separate Debit and Credit columns, set is_split_amount to true, provide debit_col_idx and credit_col_idx, and set amount_col_idx to null.
      NULL HANDLING: For any optional field that is NOT present, you MUST set its index to null. Do NOT use 0 as a placeholder.

      AMBIGUITY (Polarity): The data is ambiguous if you cannot definitively determine the cash direction (Inflow vs Outflow). This happens when there is only a single 'Amount' column with NO polarity indicators (no signs '-' or parentheses '()'). If this occurs, set is_ambiguous to true and provide an ambiguity_reason using precise language (e.g., 'cannot distinguish Cash Inflow from Outflow').

      Use precise accounting language: refer to cash flow directions as "Cash Inflow" and "Cash Outflow", never as "Income" or "Expense". Flag ambiguity if inflow vs outflow cannot be distinguished.

      Crucially, determine the sign convention (is_expense_positive). Look at obvious outflows (like 'AMZN', 'AWS', 'Starbucks', 'Uber', or rent/tax payments). If these outflows are represented by positive numbers, set is_expense_positive to true. If they are negative, set it to false.
  dependencies:
    database: false
    db_queries: true
    entity_resolver: false
```

Quality Bar

- Must compile without placeholder tokens.
- Use exact signatures from this prompt.
- No pseudocode or TODO stubs.
- Include all necessary imports only.
- Keep logic deterministic and production-safe.

What to Return

Return exactly two blocks:
1. `agent.go` source
2. `defaults.yml` snippet

Return nothing else.
````

Placeholder Values

```env
AGENT_DISPLAY_NAME="OCR_Agent"
PACKAGE_NAME="ocr_agent"
INTERNAL_MODULE_KEY="ocr_agent"
ACTIVITY_TYPE="ocr"
DID_SUFFIX="ocr_agent"
OPTIONAL_JSON_SCHEMA_STRING_IF_REDUX=""
SYSTEM_PROMPT="You are an OCR agent that analyzes incoming MMS images, receipts, and other documents, and extracts structured context from them."
```
