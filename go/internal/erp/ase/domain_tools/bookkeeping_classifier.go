package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"log/slog"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// BookkeepingClassifier handles interactions with LLMs and external classification services.
// It dispatches batches of transactions to specialized micro-agents for classification.
type BookkeepingClassifier struct {
	rt             *agent.Runtime
	nc             *nats.Conn
	logger         *slog.Logger
	agentTaskQueue string
	db             *database.Queries
	vectorStore    *ase.VectorStore
}

// SetDB injects the database connection needed for dynamic DB edge lookups.
func (cs *BookkeepingClassifier) SetDB(db *database.Queries) {
	cs.db = db
}

// SetVectorStore injects the VectorStore for semantic context retrieval.
// When set and VectorMemoryConfig.Enabled is true, batchToRows enriches
// each transaction's company_rules with semantically similar past decisions.
func (cs *BookkeepingClassifier) SetVectorStore(vs *ase.VectorStore) {
	cs.vectorStore = vs
}

// NewBookkeepingClassifier creates a new classification service configured with LLM
// and NATS capabilities.
func NewBookkeepingClassifier(rt *agent.Runtime, nc *nats.Conn, agentTaskQueue string, logger *slog.Logger) *BookkeepingClassifier {
	cs := &BookkeepingClassifier{
		nc:             nc,
		agentTaskQueue: agentTaskQueue,
		logger:         logger,
	}
	if rt != nil {
		cs.rt = rt
	}
	return cs
}

// BuildGenericThinkFunc creates a generic LLM Think dispatch function for a DAG node.
// It executes the underlying classifyGeneric using the specified prompt configuration.
func (cs *BookkeepingClassifier) BuildGenericThinkFunc(promptKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		return cs.classifyGeneric(ctx, promptKey, batch)
	}
}

// BuildDynamicThinkFunc creates a Think dispatch function that executes programmatic
// logic (like DB lookups) instead of an LLM call.
func (cs *BookkeepingClassifier) BuildDynamicThinkFunc(provider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		if provider == "db_chart_of_accounts" {
			return cs.dynamicChartOfAccounts(ctx, batch)
		}
		return nil, fmt.Errorf("unknown dynamic_edge_provider: %s", provider)
	}
}

// BuildPayloadRouterThinkFunc creates a Think dispatch function that routes transactions
// based strictly on the value of a property found in the transaction payload.
func (cs *BookkeepingClassifier) BuildPayloadRouterThinkFunc(payloadKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		res := make(map[string]ase.NodeClassification)
		for _, node := range batch {
			valStr := ""
			if v, ok := node.Payload[payloadKey].(string); ok {
				valStr = v
			}

			res[node.NodeID] = ase.NodeClassification{
				Candidates: []ase.ProbabilityCandidate{
					{Value: valStr, Confidence: 1.0, Reasoning: "Deterministic routing based on transaction payload key: " + payloadKey},
				},
			}
		}
		return res, nil
	}
}

// PropertyResponseMap represents the LLM output structure mapped with dictionary candidates.
type PropertyResponseMap struct {
	Property      string                              `json:"property"`
	CandidatesMap map[string]ase.ProbabilityCandidate `json:"candidates"`
}

func (cs *BookkeepingClassifier) classifyGeneric(ctx context.Context, promptKey string, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
	rows := cs.batchToRows(ctx, batch)

	tenantID, realmID, dagName := "", "", ""
	if len(batch) > 0 {
		tenantID = batch[0].TenantID
		realmID = batch[0].RealmID
		dagName = batch[0].DagName
	}

	systemPrompt := ase.GetPrompt(tenantID, dagName, promptKey)
	if systemPrompt == "" {
		return nil, fmt.Errorf("prompt not found in configuration for key: %s (user/tenant: %s)", promptKey, tenantID)
	}

	macroClass := batchMacroClass(batch)
	systemPrompt = strings.ReplaceAll(systemPrompt, "{{.CurrentMacroClass}}", macroClass)

	var companyIndustry string
	if len(batch) > 0 && cs.db != nil {
		tenantID := batch[0].TenantID
		info, err := cs.db.GetCompanyInfo(ctx, tenantID)
		if err == nil && info.Industry.Valid {
			companyIndustry = info.Industry.String
		}
	}
	systemPrompt = strings.ReplaceAll(systemPrompt, "{{.CompanyIndustry}}", companyIndustry)

	systemPrompt += "\n\nCRITICAL RULES FOR BATCH PROCESSING:\n" +
		"The USER REQUEST provides a map of transactions under the 'rows' key. The keys in this map are unique identifiers for each transaction. Ensure you classify every item."

	// Keep track of remaining rows to be processed/classified.
	remainingRows := make(map[string]RowPayload, len(rows))
	for k, v := range rows {
		remainingRows[k] = v
	}

	// We removed the local LLM loop; dispatch directly via NATS to the generic agent
	// which executes via the Redux engine circuit breaker.
	debugRun, debugTimeout := debugDispatchConfig(batch)
	genericResp, err := cs.dispatchViaNATS(ctx, systemPrompt, remainingRows, batchCashDirection(batch), tenantID, realmID, dagName, debugRun, debugTimeout)
	if err != nil {
		return nil, err
	}

	results := make(map[string]ase.NodeClassification, len(batch))
	for _, node := range batch {
		if result, ok := genericResp.Rows[node.NodeID]; ok {
			var candidates []ase.ProbabilityCandidate
			for _, c := range result.CandidatesMap {
				candidates = append(candidates, c)
			}
			results[node.NodeID] = ase.NodeClassification{
				Property:   result.Property,
				Candidates: candidates,
			}
		}
	}
	return results, nil
}

func (cs *BookkeepingClassifier) dynamicChartOfAccounts(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
	if cs.db == nil {
		return nil, fmt.Errorf("database not injected for dynamic edge provider")
	}

	if len(batch) == 0 {
		return nil, nil
	}

	// We'll partition the batch by RealmID because Chart of Accounts is realm-specific.
	// In practice, a DAG instance runs per realm, so the batch usually has 1 RealmID.
	realmID := batch[0].RealmID
	tenantID := batch[0].TenantID
	dagName := batch[0].DagName
	cashDirection := batchCashDirection(batch)

	accounts, err := cs.db.GetAccountsByRealm(ctx, realmID)
	if err != nil {
		for _, node := range batch {
			node.Mu.Lock()
			node.HoldReason = "dynamic COA lookup failed: " + err.Error()
			node.Mu.Unlock()
		}
		// Return empty map to let caller handle hold state
		return make(map[string]ase.NodeClassification), nil
	}

	// Format the Chart of Accounts for the LLM
	var deposits []string
	var purchases []string
	var others []string

	for _, acc := range accounts {
		line := fmt.Sprintf("  - ID: %s | Name: %s | Type: %s | SubType: %s",
			acc.ErpID, acc.Name, acc.Classification.String, acc.AccountSubType.String)

		cls := strings.ToLower(acc.Classification.String)
		if cls == "revenue" || cls == "income" {
			deposits = append(deposits, line)
		} else if cls == "expense" || cls == "cost of goods sold" || cls == "asset" {
			purchases = append(purchases, line)
		} else {
			others = append(others, line)
		}
	}

	var coaLines []string
	if len(deposits) > 0 {
		coaLines = append(coaLines, "### DEPOSIT ACCOUNTS (INFLOWS)")
		coaLines = append(coaLines, "Use these primarily for incoming funds like Revenue, Income, or Refunds.")
		coaLines = append(coaLines, deposits...)
		coaLines = append(coaLines, "")
	}
	if len(purchases) > 0 {
		coaLines = append(coaLines, "### PURCHASE ACCOUNTS (OUTFLOWS)")
		coaLines = append(coaLines, "Use these primarily for outgoing funds like Expenses, Cost of Goods Sold, or Asset Purchases.")
		coaLines = append(coaLines, purchases...)
		coaLines = append(coaLines, "")
	}
	if len(others) > 0 {
		coaLines = append(coaLines, "### OTHER ACCOUNTS (LIABILITIES, EQUITY, ETC)")
		coaLines = append(coaLines, others...)
		coaLines = append(coaLines, "")
	}

	promptKey := batch[0].PromptKey
	if promptKey == "" {
		promptKey = "terminal"
	}

	systemPrompt := ase.GetPrompt(tenantID, dagName, promptKey)
	if systemPrompt == "" {
		return nil, fmt.Errorf("prompt not found in configuration for key: %s (user/tenant: %s)", promptKey, tenantID)
	}
	systemPrompt = strings.ReplaceAll(systemPrompt, "{{.ChartOfAccounts}}", strings.Join(coaLines, "\n"))

	rows := cs.batchToRows(ctx, batch)

	debugRun, debugTimeout := debugDispatchConfig(batch)
	genericResp, err := cs.dispatchViaNATS(ctx, systemPrompt, rows, cashDirection, tenantID, realmID, dagName, debugRun, debugTimeout)
	if err != nil {
		return nil, err
	}

	results := make(map[string]ase.NodeClassification, len(batch))
	for _, node := range batch {
		if result, ok := genericResp.Rows[node.NodeID]; ok {
			var candidates []ase.ProbabilityCandidate
			for _, c := range result.CandidatesMap {
				candidates = append(candidates, ase.ProbabilityCandidate{
					Value:      c.Value,
					Confidence: c.Confidence,
					Reasoning:  "LLM COA Selection: " + c.Reasoning,
				})
			}
			results[node.NodeID] = ase.NodeClassification{
				Property:   PropAccountRef,
				Candidates: candidates,
			}
		}
	}
	return results, nil
}

type RowPayload struct {
	Description  string   `json:"description"`
	Amount       string   `json:"amount"`
	Context      []string `json:"context,omitempty"`
	CompanyRules []string `json:"company_rules,omitempty"`
	Trace        []string `json:"trace,omitempty"`
}

func (cs *BookkeepingClassifier) batchToRows(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) map[string]RowPayload {
	rows := make(map[string]RowPayload)
	tenantRules := make(map[string][]database.GetMemoryRulesRow)

	for _, node := range batch {
		node.Mu.RLock()
		tenantID := node.TenantID
		realmID := node.RealmID
		desc := ""
		if v, ok := node.Payload["raw_description"].(string); ok {
			desc = v
		} else if v, ok := node.Payload["prompt"].(string); ok {
			desc = v
		}

		amount := ""
		if v, ok := node.Payload["raw_amount"].(string); ok {
			amount = v
		}

		ctxUpdates := node.ContextUpdates

		var traceStrs []string
		for _, step := range node.ExecutionTrace {
			if step.SelectedEdge != "" {
				traceStrs = append(traceStrs, fmt.Sprintf("At node '%s', classified as '%s'", step.DAGNodeID, step.SelectedEdge))
			}
		}

		node.Mu.RUnlock()

		// --- Keyword-matched memory rules (existing mechanism) ---
		dbRules, ok := tenantRules[tenantID]
		if !ok && cs.db != nil {
			fetched, err := cs.db.GetMemoryRules(ctx, tenantID)
			if err == nil {
				dbRules = fetched
			}
			tenantRules[tenantID] = dbRules
		}

		var activeRules []string
		descLower := strings.ToLower(desc)
		for _, r := range dbRules {
			if strings.ToUpper(r.EntityValue) == "GLOBAL" || strings.Contains(descLower, strings.ToLower(r.EntityValue)) {
				activeRules = append(activeRules, r.Instruction)
			}
		}

		// --- Semantic retrieval (vector memory layer) ---
		// Reads VectorMemoryConfig at call-time so hot-reloads from the database are respected.
		if cs.vectorStore != nil {
			vcfg := cs.vectorStore.VectorCfg()
			if vcfg.Enabled && desc != "" && realmID != "" {
				vec, err := cs.vectorStore.GenerateEmbedding(ctx, tenantID, realmID, desc)
				if err != nil {
					cs.logger.Warn("ase: vector embedding failed, skipping semantic retrieval",
						"node_id", node.NodeID,
						"error", err,
					)
				} else {
					similar, err := cs.vectorStore.Search(ctx, tenantID, realmID, "", vec)
					if err != nil {
						cs.logger.Warn("ase: vector search failed, skipping semantic retrieval",
							"node_id", node.NodeID,
							"error", err,
						)
					} else {
						for _, m := range similar {
							activeRules = append(activeRules, m.RawText)
						}
					}
				}
			}
		}

		rows[node.NodeID] = RowPayload{
			Description:  desc,
			Amount:       amount,
			Context:      ctxUpdates,
			CompanyRules: activeRules,
			Trace:        traceStrs,
		}
	}
	return rows
}

func batchCashDirection(batch []*ase.AutonomousSemanticEngineNode) string {
	if len(batch) > 0 {
		batch[0].Mu.RLock()
		defer batch[0].Mu.RUnlock()
		if v, ok := batch[0].Payload["cash_direction"].(string); ok {
			return v
		}
	}
	return "UNKNOWN"
}

func batchMacroClass(batch []*ase.AutonomousSemanticEngineNode) string {
	if len(batch) > 0 {
		if top := batch[0].TopCandidate("macro_classifier"); top != nil {
			return top.Value
		}
	}
	return "UNKNOWN"
}

type genericNatsResponse struct {
	Rows map[string]PropertyResponseMap `json:"rows"`
}

func (cs *BookkeepingClassifier) dispatchViaNATS(ctx context.Context, systemPrompt string, rows map[string]RowPayload, cashDirection string, tenantID, realmID, dagName string, debugRun bool, debugTimeoutSeconds int) (*genericNatsResponse, error) {
	reqData := map[string]interface{}{
		"system_prompt":  systemPrompt,
		"rows":           rows,
		"cash_direction": cashDirection,
	}
	b, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal nats request: %w", err)
	}

	workflowSchema := `{
		"type": "object",
		"properties": {
			"rows": {
				"type": "object",
				"patternProperties": {
					"^.*$": {
						"type": "object",
						"properties": {
							"property": { "type": "string" },
							"candidates": {
								"type": "object",
								"patternProperties": {
									"^.*$": {
										"type": "object",
										"properties": {
											"value": { "type": "string" },
											"confidence": { "type": "number", "minimum": 0, "maximum": 1 },
											"reasoning": { "type": "string" }
										},
										"required": ["value", "confidence"]
									}
								}
							}
						},
						"required": ["property", "candidates"]
					}
				}
			}
		},
		"required": ["rows"]
	}`

	taskDef := core.TaskDefinition{
		ID:             uuid.New().String(),
		Domain:         "agents.accounting.batch_categorization",
		Payload:        json.RawMessage(b),
		SystemPrompt:   systemPrompt,
		Model:          "gpt-5.4-mini",
		WorkflowSchema: workflowSchema,
	}

	replyDID := fmt.Sprintf("did:toro:reply:%s", uuid.New().String())
	replySubject := fmt.Sprintf("agents.%s.inbox", replyDID)

	msgChan := make(chan *nats.Msg, 10)
	sub, err := cs.nc.ChanSubscribe(replySubject, msgChan)
	if err != nil {
		return nil, fmt.Errorf("subscribe reply: %w", err)
	}
	defer sub.Unsubscribe()

	cfpEnv, err := core.NewEnvelope(
		uuid.New().String(),
		replyDID,
		"",
		uuid.New().String(),
		core.CFP,
		taskDef,
	)
	if err != nil {
		return nil, fmt.Errorf("build cfp: %w", err)
	}

	cfpBytes, _ := json.Marshal(cfpEnv)
	cs.logger.Info("pcm classifier: dispatching batch request", "task_queue", cs.agentTaskQueue, "reply_subject", replySubject, "task_id", taskDef.ID, "batch_size", len(rows), "prompt_key", dagName)
	if err := cs.nc.Publish(cs.agentTaskQueue, cfpBytes); err != nil {
		return nil, fmt.Errorf("publish cfp: %w", err)
	}

	timeoutDuration := 120 * time.Second
	// Use default config if tenant specific is not available at dispatch context.
	dagToUse := dagName
	if dagToUse == "" {
		dagToUse = "default"
	}
	if cfg := ase.GetConfig(tenantID, dagToUse); cfg != nil {
		timeoutDuration = time.Duration(cfg.HyperParameters.LLMTimeoutSeconds) * time.Second
	}
	if debugTimeoutSeconds > 0 {
		timeoutDuration = time.Duration(debugTimeoutSeconds) * time.Second
	} else if debugRun {
		timeoutDuration = 15 * time.Second
	}
	timeout := time.NewTimer(timeoutDuration)
	defer timeout.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for agent reply: %w", ctx.Err())
		case <-timeout.C:
			return nil, fmt.Errorf("timeout waiting for agent reply (task_queue=%s reply_subject=%s task_id=%s)", cs.agentTaskQueue, replySubject, taskDef.ID)
		case reply := <-msgChan:
			var replyEnv core.Envelope
			if err := json.Unmarshal(reply.Data, &replyEnv); err != nil {
				cs.logger.Warn("pcm classifier: ignoring malformed reply", "reply_subject", replySubject, "error", err)
				continue
			}
			cs.logger.Info("pcm classifier: received reply", "reply_subject", replySubject, "performative", replyEnv.Performative, "sender", replyEnv.SenderDID)

			switch replyEnv.Performative {
			case core.PROPOSE:
				continue
			case core.FAILURE:
				return nil, fmt.Errorf("agent returned failure via NATS")
			case core.INFORM:
				var proof core.Proof
				if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
					return nil, fmt.Errorf("failed to unmarshal agent INFORM proof: %w", err)
				}

				var outputBytes []byte
				var proofData struct {
					Output string `json:"output"`
				}
				// Check if the agent returned the raw LLM string inside {"output": "..."}
				if err := json.Unmarshal(proof.Data, &proofData); err == nil && proofData.Output != "" {
					outputStr := strings.TrimSpace(proofData.Output)
					if strings.HasPrefix(outputStr, "```") {
						outputStr = strings.TrimPrefix(outputStr, "```json")
						outputStr = strings.TrimPrefix(outputStr, "```")
						outputStr = strings.TrimSuffix(outputStr, "```")
						outputStr = strings.TrimSpace(outputStr)
					}
					if strings.HasPrefix(outputStr, "{") && strings.HasSuffix(outputStr, "}") {
						outputStr = "[" + outputStr + "]"
					}
					outputBytes = []byte(outputStr)
				} else {
					// Otherwise, it's already a raw JSON patch array from generic_batch_agent
					outputBytes = proof.Data
				}

				patch, err := jsonpatch.DecodePatch(outputBytes)
				if err != nil {
					return nil, fmt.Errorf("failed to decode json patch from output: %w", err)
				}

				// Apply to a document that already has a /rows key so that either "add" or "replace" operations succeed
				modifiedJSON, err := patch.Apply([]byte(`{"rows":{}}`))
				if err != nil {
					return nil, fmt.Errorf("failed to apply json patch: %w", err)
				}

				var natsResp genericNatsResponse
				if err := json.Unmarshal(modifiedJSON, &natsResp); err != nil {
					return nil, fmt.Errorf("failed to unmarshal modified json into genericNatsResponse: %w", err)
				}

				return &natsResp, nil
			default:
				// Ignore other performatives
				continue
			}
		}
	}
}

func debugDispatchConfig(batch []*ase.AutonomousSemanticEngineNode) (bool, int) {
	if len(batch) == 0 {
		return false, 0
	}
	debugRun, _ := batch[0].Payload["debug_run"].(bool)
	seconds, _ := batch[0].Payload["debug_llm_timeout_seconds"].(int)
	return debugRun, seconds
}
