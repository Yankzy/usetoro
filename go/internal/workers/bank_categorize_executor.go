package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
)



// ClassifierResolver defines an abstraction for dynamically resolving an ase.Classifier.
type ClassifierResolver func(ctx context.Context, companyID string) (ase.Classifier, error)

// BankCategorizationASEExecutorConfig provides configuration options for BankCategorizationASEExecutor.
type BankCategorizationASEExecutorConfig struct {
	Logger      *slog.Logger
	Classifier  ase.Classifier
	Resolver    ClassifierResolver
	DAGConfig   *ase.DAGConfig
	ItemTimeout time.Duration
}

// BankCategorizationASEExecutor executes residual bank categorization requests through the Go ASE engine.
type BankCategorizationASEExecutor struct {
	logger      *slog.Logger
	classifier  ase.Classifier
	resolver    ClassifierResolver
	dagConfig   *ase.DAGConfig
	itemTimeout time.Duration
}

// Ensure BankCategorizationASEExecutor satisfies BankCategorizationExecutor.
var _ BankCategorizationExecutor = (*BankCategorizationASEExecutor)(nil)

// NewBankCategorizationASEExecutor constructs a new generic bank categorization executor.
// It fails closed if neither Classifier nor Resolver is provided.
func NewBankCategorizationASEExecutor(cfg BankCategorizationASEExecutorConfig) (*BankCategorizationASEExecutor, error) {
	if cfg.Classifier == nil && cfg.Resolver == nil {
		return nil, errors.New("bank categorization executor requires a non-nil ase.Classifier or ClassifierResolver")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	timeout := cfg.ItemTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	return &BankCategorizationASEExecutor{
		logger:      logger,
		classifier:  cfg.Classifier,
		resolver:    cfg.Resolver,
		dagConfig:   cfg.DAGConfig,
		itemTimeout: timeout,
	}, nil
}

// Classifier returns the underlying ase.Classifier if configured.
func (e *BankCategorizationASEExecutor) Classifier() ase.Classifier {
	return e.classifier
}

// NewBankCategorizationASEExecutorWithClassifier constructs an executor directly with an injected classifier.
func NewBankCategorizationASEExecutorWithClassifier(logger *slog.Logger, classifier ase.Classifier) (*BankCategorizationASEExecutor, error) {
	return NewBankCategorizationASEExecutor(BankCategorizationASEExecutorConfig{
		Logger:     logger,
		Classifier: classifier,
	})
}

// NewBankCategorizationASEExecutorFromDomainTool constructs an executor by looking up a named domain tool.
func NewBankCategorizationASEExecutorFromDomainTool(logger *slog.Logger, toolName string, deps domain_tools.ToolDependencies) (*BankCategorizationASEExecutor, error) {
	if strings.TrimSpace(toolName) == "" {
		return nil, errors.New("domain tool name cannot be empty")
	}
	tool := domain_tools.Get(toolName)
	if tool == nil {
		return nil, fmt.Errorf("domain tool %q not registered", toolName)
	}
	if _, ok := tool.(*domain_tools.NatsDomainProxy); ok && deps.NC == nil {
		return nil, fmt.Errorf("domain tool %q requires a non-nil NATS connection for proxy communication", toolName)
	}
	classifier := tool.GetClassifier(deps)
	if classifier == nil {
		return nil, fmt.Errorf("domain tool %q returned nil classifier", toolName)
	}
	return NewBankCategorizationASEExecutor(BankCategorizationASEExecutorConfig{
		Logger:     logger,
		Classifier: classifier,
	})
}

type errorTracker struct {
	mu  sync.Mutex
	err error
}

func (et *errorTracker) record(err error) {
	if err == nil {
		return
	}
	et.mu.Lock()
	defer et.mu.Unlock()
	if et.err == nil {
		et.err = err
	}
}

func (et *errorTracker) get() error {
	et.mu.Lock()
	defer et.mu.Unlock()
	return et.err
}

func trackThinkFunc(fn ase.ThinkFunc, tracker *errorTracker) ase.ThinkFunc {
	if fn == nil {
		return nil
	}
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		res, err := fn(ctx, batch)
		if err != nil {
			tracker.record(err)
			return nil, err
		}
		return res, nil
	}
}

func bankDAGCandidatePaths() []string {
	candidatePaths := []string{
		"internal/erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
		"go/internal/erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
		"../erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
		"../../internal/erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
		"../../go/internal/erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
	}
	if envPath := strings.TrimSpace(os.Getenv("TORO_ASE_BANK_DAG_PATH")); envPath != "" {
		candidatePaths = append([]string{envPath}, candidatePaths...)
	}
	return candidatePaths
}

// ParseBankCategorizationDAGConfig parses raw YAML bytes into an ase.DAGConfig.
func ParseBankCategorizationDAGConfig(yamlBytes []byte) (ase.DAGConfig, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return ase.DAGConfig{}, fmt.Errorf("failed to parse bank categorization YAML: %w", err)
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		return ase.DAGConfig{}, fmt.Errorf("missing dag key in bank categorization YAML")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		return ase.DAGConfig{}, fmt.Errorf("failed to marshal dag structure: %w", err)
	}

	var cfg ase.DAGConfig
	if err := json.Unmarshal(j, &cfg); err != nil {
		return ase.DAGConfig{}, fmt.Errorf("failed to unmarshal into DAGConfig: %w", err)
	}

	return cfg, nil
}

func loadBankCategorizationDAGConfigFromPaths(paths []string) (ase.DAGConfig, error) {
	var yamlBytes []byte
	for _, p := range paths {
		if b, readErr := os.ReadFile(p); readErr == nil && len(b) > 0 {
			yamlBytes = b
			break
		}
	}

	if len(yamlBytes) == 0 {
		return ase.DAGConfig{}, fmt.Errorf("canonical bank categorization DAG config %q not found in candidate paths: %v", BankCategorizeDagID+".yml", paths)
	}

	return ParseBankCategorizationDAGConfig(yamlBytes)
}

func loadBankCategorizationDAGConfig(customCfg *ase.DAGConfig) (ase.DAGConfig, error) {
	if customCfg != nil {
		return *customCfg, nil
	}
	return loadBankCategorizationDAGConfigFromPaths(bankDAGCandidatePaths())
}

// extractRequiredEvidence extracts required evidence strings if and only if
// they are explicitly provided by the semantic classifier or ASE execution trace/payload.
// The generic executor must never fabricate or hardcode domain-specific evidence requirements.
func extractRequiredEvidence(agent *ase.AutonomousSemanticEngineNode) []string {
	agent.Mu.RLock()
	defer agent.Mu.RUnlock()

	var evidence []string
	if agent.Payload != nil {
		if raw, ok := agent.Payload["required_evidence"]; ok && raw != nil {
			switch val := raw.(type) {
			case []string:
				for _, s := range val {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						evidence = append(evidence, trimmed)
					}
				}
			case []any:
				for _, item := range val {
					if s, ok := item.(string); ok {
						if trimmed := strings.TrimSpace(s); trimmed != "" {
							evidence = append(evidence, trimmed)
						}
					}
				}
			case string:
				if trimmed := strings.TrimSpace(val); trimmed != "" {
					evidence = append(evidence, trimmed)
				}
			}
		}

		// Check document_required (set by ASE AnnotationTemplate or domain tools)
		if len(evidence) == 0 {
			if raw, ok := agent.Payload["document_required"]; ok && raw != nil {
				if s, ok := raw.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						evidence = append(evidence, trimmed)
					}
				}
			}
		}
	}

	// Check candidate for property "required_evidence"
	if len(evidence) == 0 {
		if cands, ok := agent.Candidates["required_evidence"]; ok && len(cands) > 0 {
			for _, cand := range cands {
				if trimmed := strings.TrimSpace(cand.Value); trimmed != "" && !strings.HasPrefix(trimmed, "HOLD") {
					evidence = append(evidence, trimmed)
				}
			}
		}
	}

	if len(evidence) == 0 {
		return nil
	}
	return evidence
}

// extractAseNodeID extracts the terminal or final DAG node identity from the agent's actual execution trace.
// If no execution trace is present or node identity is blank, it returns nil.
func extractAseNodeID(agent *ase.AutonomousSemanticEngineNode) *string {
	agent.Mu.RLock()
	defer agent.Mu.RUnlock()

	if len(agent.ExecutionTrace) == 0 {
		return nil
	}

	lastStep := agent.ExecutionTrace[len(agent.ExecutionTrace)-1]
	nodeID := strings.TrimSpace(lastStep.DAGNodeID)
	if nodeID == "" {
		return nil
	}
	return &nodeID
}

func (e *BankCategorizationASEExecutor) compileDAG(classifier ase.Classifier, tracker *errorTracker) (*ase.DAG, error) {
	cfg, err := loadBankCategorizationDAGConfig(e.dagConfig)
	if err != nil {
		return nil, err
	}

	dag := ase.BuildDAGFromConfig(cfg, e.logger)

	// 1. Entry node: bank_direction_router
	entryNode := dag.GetNode("bank_direction_router")
	if entryNode == nil {
		return nil, fmt.Errorf("entry node bank_direction_router not found in compiled DAG")
	}
	entryNode.SetThinkFunc(trackThinkFunc(classifier.BuildPayloadRouterThinkFunc("direction"), tracker))

	// 2. Outflow macro classifier
	outflowNode := dag.GetNode("bank_macro_classifier_outflow")
	if outflowNode != nil {
		outflowNode.SetThinkFunc(trackThinkFunc(classifier.BuildGenericThinkFunc("bank_macro_classifier_outflow"), tracker))
	}

	// 3. Inflow macro classifier
	inflowNode := dag.GetNode("bank_macro_classifier_inflow")
	if inflowNode != nil {
		inflowNode.SetThinkFunc(trackThinkFunc(classifier.BuildGenericThinkFunc("bank_macro_classifier_inflow"), tracker))
	}

	// 4. Account resolver
	resolverNode := dag.GetNode("account_resolver")
	if resolverNode != nil {
		resolverNode.SetThinkFunc(trackThinkFunc(classifier.BuildDynamicThinkFunc("account_resolver"), tracker))
	}

	dag.StartAll()
	return dag, nil
}

// Execute processes a BankCategorizeRequest through the dedicated Go ASE DAG.
func (e *BankCategorizationASEExecutor) Execute(ctx context.Context, req BankCategorizeRequest) (BankCategorizeResponse, error) {
	if err := req.Validate(); err != nil {
		return BankCategorizeResponse{}, fmt.Errorf("invalid bank categorize request: %w", err)
	}

	classifier := e.classifier
	if classifier == nil && e.resolver != nil {
		resolved, err := e.resolver(ctx, req.CompanyID)
		if err != nil {
			return BankCategorizeResponse{}, fmt.Errorf("failed to resolve classifier for company %s: %w", req.CompanyID, err)
		}
		classifier = resolved
	}
	if classifier == nil {
		return BankCategorizeResponse{}, errors.New("no classifier available for execution")
	}

	tracker := &errorTracker{}
	dag, err := e.compileDAG(classifier, tracker)
	if err != nil {
		return BankCategorizeResponse{}, fmt.Errorf("failed to compile bank categorization DAG: %w", err)
	}
	defer dag.StopAll()

	outcomes := make([]BankCategorizeOutcome, 0, len(req.BankItems))

	for _, item := range req.BankItems {
		if err := ctx.Err(); err != nil {
			return BankCategorizeResponse{}, err
		}

		outcome, err := e.executeItem(ctx, dag, req, item, tracker)
		if err != nil {
			return BankCategorizeResponse{}, err
		}
		outcomes = append(outcomes, outcome)
	}

	resp := BankCategorizeResponse{
		SchemaVersion:  BankCategorizeSchemaVersion,
		RequestID:      req.RequestID,
		IdempotencyKey: req.IdempotencyKey,
		SessionID:      req.SessionID,
		StateRevision:  req.StateRevision,
		DagID:          BankCategorizeDagID,
		DagRunID:       uuid.New().String(),
		Status:         "COMPLETED",
		Outcomes:       outcomes,
	}

	if err := resp.Validate(); err != nil {
		return BankCategorizeResponse{}, fmt.Errorf("executor generated invalid response: %w", err)
	}

	return resp, nil
}

func (e *BankCategorizationASEExecutor) executeItem(
	ctx context.Context,
	dag *ase.DAG,
	req BankCategorizeRequest,
	item BankCategorizeItem,
	tracker *errorTracker,
) (BankCategorizeOutcome, error) {
	payload := map[string]any{
		"company_id":            req.CompanyID,
		"session_id":            req.SessionID,
		"state_revision":        req.StateRevision,
		"persistence_revision":  req.PersistenceRevision,
		"bank_item_id":          item.BankItemID,
		"bank_account_id":       item.BankAccountID,
		"residual_amount_units": item.ResidualAmountUnits,
		"original_amount_units": item.OriginalAmountUnits,
		"direction":             item.Direction,
		"date":                  item.Date,
		"currency":              item.Currency,
		"description":           item.Description,
		"reference":             item.Reference,
		"provenance_refs":       item.ProvenanceRefs,
		"bank_account_name":     item.BankAccountName,
		"institution_name":      item.InstitutionName,
		"counterparty_name":     item.CounterpartyName,
	}

	agent := ase.NewASENode(req.CompanyID, BankCategorizeDagID, payload)
	agent.UserID = req.CompanyID
	agent.TenantID = req.CompanyID
	agent.RealmID = req.CompanyID

	done := make(chan struct{})
	agent.SetOnStateChange(func(n *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState) {
		if newState == ase.StateClassified ||
			newState == ase.NodeState("HOLD") ||
			strings.HasPrefix(string(newState), "HOLD") ||
			newState == ase.StateCollapsed {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})

	dag.EntryNode.Accept(agent)

	select {
	case <-done:
	case <-ctx.Done():
		return BankCategorizeOutcome{}, ctx.Err()
	case <-time.After(e.itemTimeout):
		return BankCategorizeOutcome{}, fmt.Errorf("timeout waiting for ASE agent completion for item %s", item.BankItemID)
	}

	// Check for operational classifier failure.
	if opErr := tracker.get(); opErr != nil {
		return BankCategorizeOutcome{}, fmt.Errorf("operational classifier error: %w", opErr)
	}
	if holdReason := agent.GetHoldReason(); strings.HasPrefix(holdReason, "think phase failed:") {
		return BankCategorizeOutcome{}, fmt.Errorf("operational think phase failure: %s", holdReason)
	}

	state := agent.GetState()
	aseNodeID := extractAseNodeID(agent)
	requiredEvidence := extractRequiredEvidence(agent)
	evidenceRefs := item.ProvenanceRefs
	if evidenceRefs == nil {
		evidenceRefs = []string{}
	}

	var candidateSource *string
	if cs, ok := agent.Payload["candidate_source"].(string); ok && cs != "" {
		candidateSource = &cs
	}
	var constrainedMacro *string
	if cm, ok := agent.Payload["constrained_macro"].(string); ok && cm != "" {
		constrainedMacro = &cm
	}
	var candidateCodes []string
	if cc, ok := agent.Payload["candidate_codes"].([]string); ok {
		candidateCodes = cc
	}

	if state == ase.StateClassified {
		var accountCode string
		var conf float64 = 1.0
		rationale := "Semantically classified by account_resolver"

		if top := agent.TopCandidate("account_code"); top != nil {
			accountCode = top.Value
			conf = top.Confidence
			if top.Reasoning != "" {
				rationale = top.Reasoning
			}
		} else if rawCode, ok := agent.Payload["account_code"].(string); ok && rawCode != "" {
			accountCode = rawCode
		}

		if accountCode == "" || strings.HasPrefix(accountCode, "HOLD") {
			holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
			if accountCode != "" {
				holdReason = accountCode
			} else if hr := agent.GetHoldReason(); hr != "" {
				holdReason = hr
			} else if raw, ok := agent.Payload["hold_reason"].(string); ok && raw != "" {
				holdReason = raw
			}
			if r, ok := agent.Payload["rationale"].(string); ok && r != "" {
				rationale = r
			}
			outcome := BankCategorizeOutcome{
				BankItemID:       item.BankItemID,
				Status:           "HOLD",
				HoldReason:       &holdReason,
				Rationale:        &rationale,
				RequiredEvidence: requiredEvidence,
				EvidenceRefs:     evidenceRefs,
				AseNodeID:        aseNodeID,
				CandidateSource:  candidateSource,
				ConstrainedMacro: constrainedMacro,
				CandidateCodes:   candidateCodes,
			}
			if err := outcome.Validate(); err != nil {
				return BankCategorizeOutcome{}, fmt.Errorf("invalid outcome for item %s: %w", item.BankItemID, err)
			}
			return outcome, nil
		}

		terminalProperty := "account_code"
		outcome := BankCategorizeOutcome{
			BankItemID:       item.BankItemID,
			Status:           "CLASSIFIED",
			AccountCode:      &accountCode,
			Confidence:       &conf,
			Rationale:        &rationale,
			EvidenceRefs:     evidenceRefs,
			AseNodeID:        aseNodeID,
			TerminalProperty: &terminalProperty,
			CandidateSource:  candidateSource,
			ConstrainedMacro: constrainedMacro,
			CandidateCodes:   candidateCodes,
		}
		if err := outcome.Validate(); err != nil {
			return BankCategorizeOutcome{}, fmt.Errorf("invalid outcome for item %s: %w", item.BankItemID, err)
		}
		return outcome, nil
	}

	// Terminal HOLD outcome
	holdReason := "HOLD_INSUFFICIENT_EVIDENCE"
	if hr := agent.GetHoldReason(); hr != "" && !strings.HasPrefix(hr, "auto_advance disabled") {
		holdReason = hr
	} else if top := agent.TopCandidate("account_code"); top != nil && strings.HasPrefix(top.Value, "HOLD") {
		holdReason = top.Value
	} else if top := agent.TopCandidate("macro_class"); top != nil && strings.HasPrefix(top.Value, "HOLD") {
		holdReason = top.Value
	} else if top := agent.TopCandidate("direction"); top != nil && strings.HasPrefix(top.Value, "HOLD") {
		holdReason = top.Value
	} else if raw, ok := agent.Payload["hold_reason"].(string); ok && raw != "" {
		holdReason = raw
	}

	rationale := "Transaction held for operator review by Go ASE DAG"
	if r, ok := agent.Payload["rationale"].(string); ok && r != "" {
		rationale = r
	} else if top := agent.TopCandidate("account_code"); top != nil && top.Reasoning != "" {
		rationale = top.Reasoning
	} else if top := agent.TopCandidate("macro_class"); top != nil && top.Reasoning != "" {
		rationale = top.Reasoning
	} else if top := agent.TopCandidate("direction"); top != nil && top.Reasoning != "" {
		rationale = top.Reasoning
	}

	outcome := BankCategorizeOutcome{
		BankItemID:       item.BankItemID,
		Status:           "HOLD",
		HoldReason:       &holdReason,
		Rationale:        &rationale,
		RequiredEvidence: requiredEvidence,
		EvidenceRefs:     evidenceRefs,
		AseNodeID:        aseNodeID,
		CandidateSource:  candidateSource,
		ConstrainedMacro: constrainedMacro,
		CandidateCodes:   candidateCodes,
	}

	if err := outcome.Validate(); err != nil {
		return BankCategorizeOutcome{}, fmt.Errorf("invalid outcome for item %s: %w", item.BankItemID, err)
	}

	return outcome, nil
}
