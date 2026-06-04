package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// ClassifierService handles interactions with LLMs and external classification services.
// It dispatches batches of transactions to specialized micro-agents for classification.
type ClassifierService struct {
	llmClient      *ai.LLMClient
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
	return &ClassifierService{
		llmClient:      llmClient,
		nc:             nc,
		agentTaskQueue: agentTaskQueue,
		logger:         logger,
	}
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

// PropertyResponse represents the LLM output structure for a single classification row.
type PropertyResponse struct {
	Property   string                 `json:"property"`
	Candidates []ProbabilityCandidate `json:"candidates"`
}

// BatchPropertyResponse represents the unified JSON output from an LLM batch operation.
type BatchPropertyResponse struct {
	Rows map[string]PropertyResponse `json:"rows"`
}

func (cs *ClassifierService) classifyGeneric(ctx context.Context, promptKey string, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
	rows := cs.batchToRows(ctx, batch)
	
	tenantID, realmID := "", ""
	if len(batch) > 0 {
		tenantID = batch[0].TenantID
		realmID = batch[0].RealmID
	}
	
	systemPrompt := GetPrompt(tenantID, realmID, promptKey)
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

	systemPrompt += "\n\nSince you are processing a batch of rows, output your JSON as a map of row ID to the expected output format. Example:\n{\"rows\": {\"uuid-1\": {\"property\": \"output_property\", \"candidates\": [{\"value\": \"VALUE\", \"confidence\": 0.85, \"reasoning\": \"...\"}]}}}"

	userPrompt, err := json.Marshal(map[string]interface{}{
		"rows":           rows,
		"cash_direction": batchCashDirection(batch),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal generic prompt: %w", err)
	}

	var rowsMap map[string]PropertyResponse
	var validationErr error
	maxRetries := 3
	if cfg := GetConfig(tenantID, realmID); cfg != nil {
		maxRetries = cfg.HyperParameters.MaxLLMRetries
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		attemptSystemPrompt := systemPrompt
		if validationErr != nil {
			attemptSystemPrompt += fmt.Sprintf("\n\nCRITICAL ERROR FROM PREVIOUS ATTEMPT: %v. You must fix this! The sum of 'confidence' scores for the candidates in EACH row MUST equal exactly 1.0. If you return 1 candidate, its confidence MUST be 1.0.", validationErr)
			validationErr = nil // Reset for this attempt
		}

		if cs.llmClient != nil {
			var resp BatchPropertyResponse
			if err := cs.llmClient.GenerateJSON(ctx, attemptSystemPrompt, string(userPrompt), &resp); err != nil {
				return nil, fmt.Errorf("generic llm call: %w", err)
			}
			rowsMap = resp.Rows
		} else {
			genericResp, err := cs.dispatchViaNATS(ctx, attemptSystemPrompt, rows)
			if err != nil {
				return nil, err
			}
			rowsMap = make(map[string]PropertyResponse, len(genericResp.Rows))
			for k, v := range genericResp.Rows {
				b, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal row %s: %w", k, err)
				}
				var pr PropertyResponse
				if err := json.Unmarshal(b, &pr); err != nil {
					return nil, fmt.Errorf("failed to unmarshal row %s into PropertyResponse: %w", k, err)
				}
				rowsMap[k] = pr
			}
		}

		// Validate probabilities
		isValid := true
		for rowID, resp := range rowsMap {
			var sum float64
			for _, c := range resp.Candidates {
				sum += c.Confidence
			}
			if len(resp.Candidates) == 1 && (sum < 0.99 || sum > 1.01) {
				validationErr = fmt.Errorf("row %s has only 1 candidate but confidence is %f (must be 1.0)", rowID, sum)
				isValid = false
				break
			}
			if len(resp.Candidates) > 1 && (sum < 0.98 || sum > 1.02) {
				validationErr = fmt.Errorf("row %s candidates confidence sum is %f (must be 1.0)", rowID, sum)
				isValid = false
				break
			}
		}

		if isValid {
			break
		}

		if attempt == maxRetries-1 {
			return nil, fmt.Errorf("failed to get valid probability distribution after %d attempts: %v", maxRetries, validationErr)
		}
	}

	results := make(map[string]NodeClassification, len(batch))
	for _, node := range batch {
		if result, ok := rowsMap[node.NodeID]; ok {
			results[node.NodeID] = NodeClassification{
				Property:   result.Property,
				Candidates: result.Candidates,
			}
		}
	}
	return results, nil
}

func (cs *ClassifierService) dynamicChartOfAccounts(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
	if cs.db == nil {
		return nil, fmt.Errorf("database not injected for dynamic edge provider")
	}
	results := make(map[string]NodeClassification, len(batch))

	for _, node := range batch {
		accounts, err := cs.db.GetAccountsByRealm(ctx, node.TenantID)
		if err != nil {
			node.mu.Lock()
			node.HoldReason = "dynamic COA lookup failed: " + err.Error()
			node.mu.Unlock()
			continue
		}

		var candidates []ProbabilityCandidate
		if len(accounts) > 0 {
			candidates = append(candidates, ProbabilityCandidate{
				Value:      accounts[0].ErpID,
				Confidence: 1.0,
				Reasoning:  "Dynamically selected from Chart of Accounts DB query.",
			})
		}
		results[node.NodeID] = NodeClassification{
			Property:   PropAccountRef,
			Candidates: candidates,
		}
	}
	return results, nil
}

type RowPayload struct {
	Description  string   `json:"description"`
	Context      []string `json:"context,omitempty"`
	CompanyRules []string `json:"company_rules,omitempty"`
}

func (cs *ClassifierService) batchToRows(ctx context.Context, batch []*AutonomousSemanticEngineNode) map[string]RowPayload {
	rows := make(map[string]RowPayload)
	tenantRules := make(map[string][]database.GetMemoryRulesRow)

	for _, node := range batch {
		node.mu.RLock()
		tenantID := node.TenantID
		realmID := node.RealmID
		desc := node.RawDescription
		ctxUpdates := node.ContextUpdates
		node.mu.RUnlock()

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
		// Reads VectorMemoryConfig at call-time so ase.yml hot-reloads are respected.
		if cs.vectorStore != nil {
			vcfg := cs.vectorStore.vectorCfg(tenantID, realmID)
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
			Context:      ctxUpdates,
			CompanyRules: activeRules,
		}
	}
	return rows
}

func batchCashDirection(batch []*AutonomousSemanticEngineNode) string {
	if len(batch) > 0 {
		batch[0].mu.RLock()
		defer batch[0].mu.RUnlock()
		return batch[0].CashDirection
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
	Rows map[string]interface{} `json:"rows"`
}

func (cs *ClassifierService) dispatchViaNATS(ctx context.Context, systemPrompt string, rows map[string]RowPayload) (*genericNatsResponse, error) {
	reqData := map[string]interface{}{
		"system_prompt": systemPrompt,
		"rows":          rows,
	}
	b, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal nats request: %w", err)
	}

	taskDef := core.TaskDefinition{
		ID:           uuid.New().String(),
		Domain:       "agents.accounting.batch_categorization",
		Payload:      json.RawMessage(b),
		SystemPrompt: systemPrompt,
		Model:        "gpt-5.4-mini",
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
	if cfg := GetConfig("", ""); cfg != nil {
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
				cs.logger.Info("ase_bridge: agent proposed task execution", "cid", replyEnv.ConversationID)
				continue
			case core.FAILURE:
				return nil, fmt.Errorf("agent returned failure via NATS")
			case core.INFORM:
				var proof core.Proof
				if err := json.Unmarshal(replyEnv.Body, &proof); err != nil {
					return nil, fmt.Errorf("failed to unmarshal agent INFORM proof: %w", err)
				}

				// The JSON patch might be wrapped in an array by extractJSONPatches
				var patches []genericNatsResponse
				if err := json.Unmarshal(proof.Data, &patches); err != nil {
					// Fallback to single object
					var single genericNatsResponse
					if err2 := json.Unmarshal(proof.Data, &single); err2 != nil {
						cs.logger.Warn("skipping unmarshalable nats INFORM proof payload", "error", err2)
						return nil, fmt.Errorf("failed to unmarshal JSON patches from proof: %w", err2)
					}
					return &single, nil
				}

				if len(patches) > 0 {
					return &patches[0], nil
				}
				return nil, fmt.Errorf("agent returned empty patches in INFORM")
			default:
				// Ignore other performatives
				continue
			}
		}
	}
}
