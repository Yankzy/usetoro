package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// llmGenerator defines the interface for making structured JSON calls to the LLM.
type llmGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error
}

// ClassifierService handles interactions with LLMs and external classification services.
// It dispatches batches of transactions to specialized micro-agents for classification.
type ClassifierService struct {
	llmClient      llmGenerator
	nc             *nats.Conn
	logger         *slog.Logger
	agentTaskQueue string
	db             *database.Queries
	vectorStore    *VectorStore
}

// SetDB injects the database connection needed for dynamic DB edge lookups.
func (cs *ClassifierService) SetDB(db *database.Queries) {
	cs.db = db
}

// SetVectorStore injects the VectorStore for semantic context retrieval.
// When set and VectorMemoryConfig.Enabled is true, batchToRows enriches
// each transaction's company_rules with semantically similar past decisions.
func (cs *ClassifierService) SetVectorStore(vs *VectorStore) {
	cs.vectorStore = vs
}

// NewClassifierService creates a new classification service configured with LLM
// and NATS capabilities.
func NewClassifierService(llmClient *ai.LLMClient, nc *nats.Conn, agentTaskQueue string, logger *slog.Logger) *ClassifierService {
	cs := &ClassifierService{
		nc:             nc,
		agentTaskQueue: agentTaskQueue,
		logger:         logger,
	}
	if llmClient != nil {
		cs.llmClient = llmClient
	}
	return cs
}

// BuildGenericThinkFunc creates a generic LLM Think dispatch function for a DAG node.
// It executes the underlying classifyGeneric using the specified prompt configuration.
func (cs *ClassifierService) BuildGenericThinkFunc(promptKey string) ThinkFunc {
	return func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		return cs.classifyGeneric(ctx, promptKey, batch)
	}
}

// BuildDynamicThinkFunc creates a Think dispatch function that executes programmatic
// logic (like DB lookups) instead of an LLM call.
func (cs *ClassifierService) BuildDynamicThinkFunc(provider string) ThinkFunc {
	return func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		if provider == "db_chart_of_accounts" {
			return cs.dynamicChartOfAccounts(ctx, batch)
		}
		return nil, fmt.Errorf("unknown dynamic_edge_provider: %s", provider)
	}
}

// BuildPayloadRouterThinkFunc creates a Think dispatch function that routes transactions
// based strictly on the value of a property found in the transaction payload.
func (cs *ClassifierService) BuildPayloadRouterThinkFunc(payloadKey string) ThinkFunc {
	return func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		res := make(map[string]NodeClassification)
		for _, node := range batch {
			valStr := ""
			if v, ok := node.Payload[payloadKey].(string); ok {
				valStr = v
			}
			
			res[node.NodeID] = NodeClassification{
				Candidates: []ProbabilityCandidate{
					{Value: valStr, Confidence: 1.0, Reasoning: "Deterministic routing based on transaction payload key: " + payloadKey},
				},
			}
		}
		return res, nil
	}
}

// PropertyResponseMap represents the LLM output structure mapped with dictionary candidates.
type PropertyResponseMap struct {
	Property      string                          `json:"property"`
	CandidatesMap map[string]ProbabilityCandidate `json:"candidates"`
}

func (cs *ClassifierService) classifyGeneric(ctx context.Context, promptKey string, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
	rows := cs.batchToRows(ctx, batch)

	tenantID, realmID, dagName := "", "", ""
	if len(batch) > 0 {
		tenantID = batch[0].TenantID
		realmID = batch[0].RealmID
		dagName = batch[0].DagName
	}

	systemPrompt := GetPrompt(tenantID, realmID, dagName, promptKey)
	if systemPrompt == "" {
		return nil, fmt.Errorf("prompt not found in configuration for key: %s (tenant: %s, realm: %s)", promptKey, tenantID, realmID)
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
		"1. The USER REQUEST provides a map of transactions under the 'rows' key. The keys in this map are unique identifiers for each transaction.\n" +
		"2. Your output MUST be a valid JSON array containing exactly ONE RFC 6902 JSON patch operation.\n" +
		"3. This single patch MUST use exactly \"op\": \"add\" and \"path\": \"/rows\".\n" +
		"4. The \"value\" of the patch MUST be an object where the keys are EXACTLY the unique transaction identifiers from the input.\n" +
		"5. Inside each row's classification object, you MUST return a 'property' string AND a 'candidates' map.\n" +
		"6. The 'candidates' map MUST contain at least 2 numbered candidate entries (e.g. \"1\": {...}, \"2\": {...}) for that row."

	// Keep track of remaining rows to be processed/classified.
	remainingRows := make(map[string]RowPayload, len(rows))
	for k, v := range rows {
		remainingRows[k] = v
	}

	// We removed the local LLM loop; dispatch directly via NATS to the generic agent
	// which executes via the Redux engine circuit breaker.
	genericResp, err := cs.dispatchViaNATS(ctx, systemPrompt, remainingRows, batchCashDirection(batch), tenantID, realmID, dagName)
	if err != nil {
		return nil, err
	}

	results := make(map[string]NodeClassification, len(batch))
	for _, node := range batch {
		if result, ok := genericResp.Rows[node.NodeID]; ok {
			var candidates []ProbabilityCandidate
			for _, c := range result.CandidatesMap {
				candidates = append(candidates, c)
			}
			results[node.NodeID] = NodeClassification{
				Property:   result.Property,
				Candidates: candidates,
			}
		}
	}
	return results, nil
}

func (cs *ClassifierService) dynamicChartOfAccounts(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
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
		return make(map[string]NodeClassification), nil
	}

	// Format the Chart of Accounts for the LLM
	var coaLines []string
	for _, acc := range accounts {
		coaLines = append(coaLines, fmt.Sprintf("- ID: %s | Name: %s | Type: %s | SubType: %s", 
			acc.ErpID, acc.Name, acc.Classification.String, acc.AccountSubType.String))
	}

	promptKey := batch[0].PromptKey
	if promptKey == "" {
		promptKey = "account_selection"
	}
	
	systemPrompt := GetPrompt(tenantID, realmID, dagName, promptKey)
	if systemPrompt == "" {
		return nil, fmt.Errorf("prompt not found in configuration for key: %s (tenant: %s, realm: %s)", promptKey, tenantID, realmID)
	}
	systemPrompt = strings.ReplaceAll(systemPrompt, "{{.ChartOfAccounts}}", strings.Join(coaLines, "\n"))

	rows := cs.batchToRows(ctx, batch)

	genericResp, err := cs.dispatchViaNATS(ctx, systemPrompt, rows, cashDirection, tenantID, realmID, dagName)
	if err != nil {
		return nil, err
	}

	results := make(map[string]NodeClassification, len(batch))
	for _, node := range batch {
		if result, ok := genericResp.Rows[node.NodeID]; ok {
			var candidates []ProbabilityCandidate
			for _, c := range result.CandidatesMap {
				candidates = append(candidates, ProbabilityCandidate{
					Value:      c.Value,
					Confidence: c.Confidence,
					Reasoning:  "LLM COA Selection: " + c.Reasoning,
				})
			}
			results[node.NodeID] = NodeClassification{
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
}

func (cs *ClassifierService) batchToRows(ctx context.Context, batch []*AutonomousSemanticEngineNode) map[string]RowPayload {
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
			vcfg := cs.vectorStore.vectorCfg()
			if vcfg.Enabled && desc != "" && realmID != "" {
				vec, err := cs.vectorStore.GenerateEmbedding(ctx, tenantID, realmID, desc)
				if err != nil {
					cs.logger.Warn("ase: vector embedding failed, skipping semantic retrieval",
						"node_id", node.NodeID,
						"error", err,
					)
				} else {
					similar, err := cs.vectorStore.Search(ctx, tenantID, realmID, vec)
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
		}
	}
	return rows
}

func batchCashDirection(batch []*AutonomousSemanticEngineNode) string {
	if len(batch) > 0 {
		batch[0].Mu.RLock()
		defer batch[0].Mu.RUnlock()
		if v, ok := batch[0].Payload["cash_direction"].(string); ok {
			return v
		}
	}
	return "UNKNOWN"
}

func batchMacroClass(batch []*AutonomousSemanticEngineNode) string {
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

func (cs *ClassifierService) dispatchViaNATS(ctx context.Context, systemPrompt string, rows map[string]RowPayload, cashDirection string, tenantID, realmID, dagName string) (*genericNatsResponse, error) {
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
	if err := cs.nc.Publish(cs.agentTaskQueue, cfpBytes); err != nil {
		return nil, fmt.Errorf("publish cfp: %w", err)
	}

	timeoutDuration := 120 * time.Second
	// Use default config if tenant specific is not available at dispatch context.
	dagToUse := dagName
	if dagToUse == "" {
		dagToUse = "default"
	}
	if cfg := GetConfig(tenantID, realmID, dagToUse); cfg != nil {
		timeoutDuration = time.Duration(cfg.HyperParameters.LLMTimeoutSeconds) * time.Second
	}
	timeout := time.NewTimer(timeoutDuration)
	defer timeout.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for agent reply: %w", ctx.Err())
		case <-timeout.C:
			return nil, fmt.Errorf("timeout waiting for agent reply")
		case reply := <-msgChan:
			var replyEnv core.Envelope
			if err := json.Unmarshal(reply.Data, &replyEnv); err != nil {
				continue
			}

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
