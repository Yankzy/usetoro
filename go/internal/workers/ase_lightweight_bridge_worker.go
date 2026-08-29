package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/nats-io/nats.go"
	"gopkg.in/yaml.v3"
)

// AseLightweightBridgeWorker is a container-native, zero-database bridge worker.
// It receives mock transactions over NATS, builds in-memory agents, executes the DAG
// inside the protocol container, and publishes execution reports back over NATS.
type AseLightweightBridgeWorker struct {
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	rt      *agent.Runtime
	dags    *expirable.LRU[string, *ase.DAG]
	configs *expirable.LRU[string, *ase.ASEConfig]
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &AseLightweightBridgeWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			rt:     deps.Runtime,
		}, nil
	})
}

func (w *AseLightweightBridgeWorker) Init(ctx context.Context) error {
	w.dags = expirable.NewLRU(1000, func(k string, v *ase.DAG) {
		w.logger.Info("ase_lightweight_bridge: evicting idle DAG from cache", "key", k)
		v.StopAll()
	}, time.Minute*30)
	w.configs = expirable.NewLRU[string, *ase.ASEConfig](100, nil, time.Minute*30)
	return nil
}

func (w *AseLightweightBridgeWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)

	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.ase_lightweight_bridge"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("ase_lightweight_bridge: failed to derive inbox", "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "ase-lightweight-bridge-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// LightweightExecutionReport is the structured payload returned to the test client.
type LightweightExecutionReport struct {
	TransactionID   string                     `json:"transaction_id"`
	EdgeKey         string                     `json:"edge_key"`
	CashDirection   string                     `json:"cash_direction"`
	RawDescription  string                     `json:"raw_description"`
	RawAmount       string                     `json:"raw_amount"`
	Currency        string                     `json:"currency"`
	FinalState      ase.NodeState              `json:"final_state"`
	HoldReason      string                     `json:"hold_reason,omitempty"`
	Candidates      []ase.ProbabilityCandidate `json:"candidates,omitempty"`
	SelectedEdge    string                     `json:"selected_edge,omitempty"`
	TargetChildNode string                     `json:"target_child_node,omitempty"`
	ExpectedChild   string                     `json:"expected_child,omitempty"`
	MatchedExpected bool                       `json:"matched_expected"`
	ExecutionSteps  []ase.NodeExecutionStep    `json:"execution_steps"`
	Duration        time.Duration              `json:"duration"`
}

func (w *AseLightweightBridgeWorker) loadConfig(dagName string) (*ase.ASEConfig, error) {
	if cfg, ok := w.configs.Get(dagName); ok && cfg != nil {
		return cfg, nil
	}

	fileName := dagName + ".yml"
	var foundPath string

	filepath.Walk("/app", func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == fileName {
			foundPath = path
			return filepath.SkipDir // Stop searching once found
		}
		return nil
	})

	if foundPath == "" {
		// Fallback to searching the current working directory tree
		cwd, _ := os.Getwd()
		filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.Name() == fileName {
				foundPath = path
				return filepath.SkipDir
			}
			return nil
		})
	}

	if foundPath != "" {
		data, err := os.ReadFile(foundPath)
		if err == nil {
			// Convert YAML to JSON so it properly respects the `json` struct tags on ASEConfig
			var raw map[string]any
			if err := yaml.Unmarshal(data, &raw); err == nil {
				if jsonBytes, err := json.Marshal(raw); err == nil {
					var cfg ase.ASEConfig
					if err := json.Unmarshal(jsonBytes, &cfg); err == nil {
						w.configs.Add(dagName, &cfg)
						w.logger.Info("ase_lightweight_bridge: loaded DAG configuration directly from disk (Zero-DB)", "path", foundPath, "nodes_count", len(cfg.DAG.Nodes), "entry_node", cfg.DAG.EntryNode)
						return &cfg, nil
					} else {
						w.logger.Warn("ase_lightweight_bridge: json unmarshal failed for path", "path", foundPath, "error", err)
					}
				}
			} else {
				w.logger.Warn("ase_lightweight_bridge: yaml unmarshal failed for path", "path", foundPath, "error", err)
			}
		}
	}

	// Fallback to in-memory ASE GetConfig
	if cfg := ase.GetConfig("", dagName); cfg != nil {
		return cfg, nil
	}

	return nil, fmt.Errorf("DAG config '%s' not found on disk or memory", dagName)
}

func (w *AseLightweightBridgeWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [LIGHTWEIGHT BRIDGE] Received NATS execution request", "subject", msg.Subject)

	// Keep JetStream message alive during long-running DAG evaluation
	keepAliveDoneCh := make(chan struct{})
	defer close(keepAliveDoneCh)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepAliveDoneCh:
				return
			case <-ticker.C:
				msg.InProgress()
			}
		}
	}()

	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error("ase_lightweight_bridge: malformed envelope", "error", err)
		return nil
	}

	var reqBody struct {
		Config struct {
			DagName    string `json:"dag_name"`
			DomainTool string `json:"domain_tool"`
		} `json:"config"`
		Input struct {
			MockTransaction struct {
				EdgeKey           string `json:"edge_key"`
				ParentNode        string `json:"parent_node"`
				CashDirection     string `json:"cash_direction"`
				ExpectedChildNode string `json:"expected_child_node"`
				RawDescription    string `json:"raw_description"`
				RawAmount         string `json:"raw_amount"`
				Currency          string `json:"currency"`
				CounterpartyName  string `json:"counterparty_name"`
				ICENumber         string `json:"ice_number"`
				Notes             string `json:"notes"`
			} `json:"mock_transaction"`
			EdgeKey           string `json:"edge_key"`
			ParentNode        string `json:"parent_node"`
			CashDirection     string `json:"cash_direction"`
			ExpectedChildNode string `json:"expected_child_node"`
			RawDescription    string `json:"raw_description"`
			RawAmount         string `json:"raw_amount"`
			Currency          string `json:"currency"`
			CounterpartyName  string `json:"counterparty_name"`
			ICENumber         string `json:"ice_number"`
			TargetEdgeKey     string `json:"target_edge_key"`
			TimeoutSeconds    int    `json:"timeout_seconds"`
		} `json:"input"`
	}

	if err := json.Unmarshal(env.Body, &reqBody); err != nil {
		w.logger.Error("ase_lightweight_bridge: failed to unmarshal envelope body", "error", err)
		return nil
	}

	dagName := reqBody.Config.DagName
	if dagName == "" {
		dagName = "pcm_bank_cash_accounting_dag"
	}

	cfg, err := w.loadConfig(dagName)
	if err != nil {
		w.logger.Error("ase_lightweight_bridge: failed to resolve DAG config", "dag_name", dagName, "error", err)
		return nil
	}

	// Resolve mock transaction fields
	tx := reqBody.Input.MockTransaction
	if tx.EdgeKey == "" && reqBody.Input.EdgeKey != "" {
		tx.EdgeKey = reqBody.Input.EdgeKey
		tx.ParentNode = reqBody.Input.ParentNode
		tx.CashDirection = reqBody.Input.CashDirection
		tx.ExpectedChildNode = reqBody.Input.ExpectedChildNode
		tx.RawDescription = reqBody.Input.RawDescription
		tx.RawAmount = reqBody.Input.RawAmount
		tx.Currency = reqBody.Input.Currency
		tx.CounterpartyName = reqBody.Input.CounterpartyName
		tx.ICENumber = reqBody.Input.ICENumber
	}

	currency := tx.Currency
	if currency == "" {
		currency = "MAD"
	}

	var icePtr *string
	if tx.ICENumber != "" {
		val := tx.ICENumber
		icePtr = &val
	}

	nodeID := uuid.New().String()
	userUUID := uuid.New().String()

	payload := map[string]any{
		"raw_description":        tx.RawDescription,
		"description":            tx.RawDescription,
		"cash_direction":         tx.CashDirection,
		"raw_amount":             tx.RawAmount,
		"currency":               currency,
		"operation_date":         time.Now().Format("2006-01-02"),
		"domain_tool":            "pcm_cash_accounting",
		"session_id":             uuid.New().String(),
		"staging_transaction_id": nodeID,
		"statement_type":         "BANK_STATEMENT",
		"account_code":           "514100",
		"enrichment_envelope": enrichment.AnnotatedMoroccanTransactionEnvelope{
			Counterparty: enrichment.CounterpartyInfo{
				NormalizedName: tx.CounterpartyName,
				Identifiers: enrichment.TaxIdentifiers{
					ICE: icePtr,
				},
			},
			PCGMAccounting: enrichment.PCGMInfo{
				AccountLabel: tx.Notes,
			},
		},
		"normalized_merchant": tx.CounterpartyName,
		"ice_number":          tx.ICENumber,
		"target_edge_key":     tx.EdgeKey,
	}

	// Manually build in-memory synthetic agent to completely bypass GetConfig and DB
	expectedProperties := 4
	if cfg != nil && cfg.HyperParameters.ExpectedProperties > 0 {
		expectedProperties = cfg.HyperParameters.ExpectedProperties
	}
	
	agentNode := &ase.AutonomousSemanticEngineNode{
		NodeID:            nodeID,
		UserID:            userUUID,
		TenantID:          userUUID,
		DagName:           dagName,
		Payload:           payload,
		CurrentState:      ase.StateUninitialized,
		CurrentEntropy:    float64(expectedProperties) * 1.0,
		UnifiedConfidence: 0.0,
		PropertyEntropies: make(map[string]float64),
		Candidates:        make(map[string][]ase.ProbabilityCandidate),
		ExecutionTrace:    make([]ase.NodeExecutionStep, 0),
		LifetimeProbes:    0,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	// Note: We bypass logger and channels setup here since it's just for lightweight eval
	agentNode.SetLogger(w.logger.With("component", "ase.node", "node_id", nodeID))

	// Build DAG
	dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)

	domain := cfg.HyperParameters.DomainTool
	if domain == "" {
		w.logger.Error("ase_lightweight_bridge: domain_tool missing in config", "dag_name", dagName)
		return nil
	}

	dt := domain_tools.Get(domain)
	var classifier ase.Classifier
	if dt != nil {
		deps := domain_tools.ToolDependencies{
			Logger:  w.logger,
			NC:      w.nc,
			Runtime: w.rt,
		}
		classifier = dt.GetClassifier(deps)
	}

	for _, n := range dag.Nodes {
		if n.Kind == "initial_router" {
			payloadKey := ""
			if pk, ok := n.ExecutionParams["payload_key"]; ok {
				payloadKey = pk
			}
			if classifier != nil {
				n.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc(payloadKey))
			}
		} else if n.EdgeType == "dynamic" {
			if classifier != nil {
				n.SetThinkFunc(classifier.BuildDynamicThinkFunc(n.DynamicEdgeProvider))
			}
		} else if n.PromptKey != "" {
			if classifier != nil {
				n.SetThinkFunc(classifier.BuildGenericThinkFunc(n.PromptKey))
			}
		} else if n.Kind == "terminal" || n.Kind == "debug_terminal" || n.ID == "debug_terminal" {
			n.SetThinkFunc(nil)
		} else if actionProvider, ok := n.ExecutionParams["action_provider"]; ok && actionProvider != "" {
			n.SetThinkFunc(w.buildActionProviderThinkFunc(actionProvider))
		} else {
			n.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
				return nil, nil // No-op
			})
		}
	}

	dag.StartAll()
	defer dag.StopAll()

	// In-memory persister
	memStore := &inMemoryWorkerStore{
		agents: make(map[string]*ase.AutonomousSemanticEngineNode),
		traces: make(map[string][]ase.NodeExecutionStep),
		states: make(map[string]ase.NodeState),
		holds:  make(map[string]string),
	}

	start := time.Now()
	doneCh := make(chan struct{}, 1)
	var terminalOnce sync.Once

	agentNode.SetOnStateChange(func(n *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState) {
		if newState != ase.StateUninitialized && newState != ase.StateTriaging && newState != ase.StateThinking && newState != ase.StateActivating {
			terminalOnce.Do(func() { doneCh <- struct{}{} })
		}
	})

	if err := agentNode.Resume(ctx, dag, memStore, ""); err != nil {
		w.logger.Error("ase_lightweight_bridge: failed to resume agent", "error", err)
		return nil
	}

	timeoutDur := 60 * time.Second
	if reqBody.Input.TimeoutSeconds > 0 {
		timeoutDur = time.Duration(reqBody.Input.TimeoutSeconds) * time.Second
	}

	select {
	case <-doneCh:
	case <-time.After(timeoutDur):
		w.logger.Warn("ase_lightweight_bridge: execution timed out waiting for DAG node completion", "timeout", timeoutDur)
	}

	duration := time.Since(start)

	// Harvest results
	var topCandidates []ase.ProbabilityCandidate
	selectedEdge := ""
	targetChildNode := ""

	agentNode.Mu.RLock()
	if cands, ok := agentNode.Candidates["bank_transaction_intent"]; ok && len(cands) > 0 {
		topCandidates = cands
		selectedEdge = cands[0].Value
	}
	finalState := agentNode.GetState()
	holdReason := agentNode.HoldReason
	trace := append([]ase.NodeExecutionStep(nil), agentNode.ExecutionTrace...)
	agentNode.Mu.RUnlock()

	for _, step := range trace {
		if step.DAGNodeID == tx.ParentNode || strings.HasPrefix(step.DAGNodeID, "bank_transaction_classifier") {
			selectedEdge = step.SelectedEdge
			if len(step.Candidates) > 0 {
				topCandidates = step.Candidates
			}
			if nodeCfg, exists := cfg.DAG.Nodes[step.DAGNodeID]; exists {
				if childID, childExists := nodeCfg.Children[step.SelectedEdge]; childExists {
					targetChildNode = childID
				} else if nodeCfg.DefaultChild != "" {
					targetChildNode = nodeCfg.DefaultChild
				}
			}
		}
	}

	matchedExpected := false
	if selectedEdge == tx.EdgeKey && strings.EqualFold(targetChildNode, tx.ExpectedChildNode) {
		matchedExpected = true
	} else if selectedEdge == tx.EdgeKey {
		matchedExpected = true
	}

	report := LightweightExecutionReport{
		TransactionID:   agentNode.NodeID,
		EdgeKey:         tx.EdgeKey,
		CashDirection:   tx.CashDirection,
		RawDescription:  tx.RawDescription,
		RawAmount:       tx.RawAmount,
		Currency:        currency,
		FinalState:      finalState,
		HoldReason:      holdReason,
		Candidates:      topCandidates,
		SelectedEdge:    selectedEdge,
		TargetChildNode: targetChildNode,
		ExpectedChild:   tx.ExpectedChildNode,
		MatchedExpected: matchedExpected,
		ExecutionSteps:  trace,
		Duration:        duration,
	}

	reportBytes, err := json.Marshal(report)
	if err != nil {
		w.logger.Error("ase_lightweight_bridge: failed to marshal report", "error", err)
		return nil
	}

	// Reply to sender via NATS
	var replySubject string
	if env.SenderDID != "" {
		if strings.HasPrefix(env.SenderDID, "did:toro:") {
			replySubject = fmt.Sprintf("agents.%s.inbox", env.SenderDID)
		} else {
			replySubject = env.SenderDID
		}
	} else if msg.Reply != "" {
		replySubject = msg.Reply
	}

	if replySubject != "" {
		replyEnv, err := core.NewEnvelope(
			uuid.NewString(),
			"did:toro:worker:ase-lightweight-bridge",
			env.SenderDID,
			env.ID,
			core.INFORM,
			json.RawMessage(reportBytes),
		)
		if err == nil {
			envBytes, _ := json.Marshal(replyEnv)
			_ = w.nc.Publish(replySubject, envBytes)
			w.logger.Info("📡 [LIGHTWEIGHT BRIDGE] Published execution report reply to NATS", "reply_subject", replySubject)
		}
	}

	return nil
}

func (w *AseLightweightBridgeWorker) buildActionProviderThinkFunc(actionProvider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification)
		for _, node := range batch {
			node.Mu.RLock()
			payload := node.Payload
			candidates := node.Candidates
			tenantID := node.TenantID
			realmID := node.RealmID
			dagName := node.DagName
			nodeID := node.NodeID
			ctxUpdates := node.ContextUpdates
			node.Mu.RUnlock()

			req := map[string]interface{}{
				"node_id":         nodeID,
				"tenant_id":       tenantID,
				"realm_id":        realmID,
				"dag_name":        dagName,
				"payload":         payload,
				"context_updates": ctxUpdates,
				"action_provider": actionProvider,
				"candidates":      candidates,
			}
			reqBytes, err := json.Marshal(req)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request for action %s: %w", actionProvider, err)
			}

			// Respect configured llm_timeout_seconds from DAG configs
			timeoutDuration := 120 * time.Second
			userID := node.UserID
			if userID == "" {
				userID = tenantID
			}
			if cfg := ase.GetConfig(userID, dagName); cfg != nil && cfg.HyperParameters.LLMTimeoutSeconds > 0 {
				timeoutDuration = time.Duration(cfg.HyperParameters.LLMTimeoutSeconds) * time.Second
			}

			ctxWithTimeout, cancel := context.WithTimeout(ctx, timeoutDuration)
			defer cancel()

			subject := "worker.inbox.action." + actionProvider
			msg, err := w.nc.RequestWithContext(ctxWithTimeout, subject, reqBytes)
			if err != nil {
				w.logger.Error("failed NATS request to action provider", "action_provider", actionProvider, "error", err)
				return nil, fmt.Errorf("failed NATS request to action provider %s: %w", actionProvider, err)
			}

			var resp struct {
				Candidates     []ase.ProbabilityCandidate `json:"candidates"`
				Property       string                     `json:"property"`
				PayloadUpdates map[string]interface{}     `json:"payload_updates"`
				ContextUpdates []string                   `json:"context_updates"`
			}
			if err := json.Unmarshal(msg.Data, &resp); err != nil {
				return nil, fmt.Errorf("failed to unmarshal action provider response: %w", err)
			}

			// Apply updates to the node if any
			node.Mu.Lock()
			if len(resp.PayloadUpdates) > 0 {
				if node.Payload == nil {
					node.Payload = make(map[string]interface{})
				}
				for k, v := range resp.PayloadUpdates {
					node.Payload[k] = v
				}
			}
			if len(resp.ContextUpdates) > 0 {
				node.ContextUpdates = append(node.ContextUpdates, resp.ContextUpdates...)
			}
			node.Mu.Unlock()

			results[nodeID] = ase.NodeClassification{
				Candidates: resp.Candidates,
				Property:   resp.Property,
			}
		}
		return results, nil
	}
}

// inMemoryWorkerStore implements ase.StatePersister in memory.
type inMemoryWorkerStore struct {
	mu     sync.RWMutex
	agents map[string]*ase.AutonomousSemanticEngineNode
	traces map[string][]ase.NodeExecutionStep
	states map[string]ase.NodeState
	holds  map[string]string
}

func (s *inMemoryWorkerStore) PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[node.NodeID] = node
	s.states[node.NodeID] = node.GetState()
	s.traces[node.NodeID] = append([]ase.NodeExecutionStep(nil), node.ExecutionTrace...)
	return nil
}

func (s *inMemoryWorkerStore) PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[node.NodeID] = node
	s.states[node.NodeID] = node.GetState()
	s.holds[node.NodeID] = node.HoldReason
	s.traces[node.NodeID] = append([]ase.NodeExecutionStep(nil), node.ExecutionTrace...)
	return nil
}

func (s *inMemoryWorkerStore) PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[node.NodeID] = node
	s.states[node.NodeID] = ase.StateReadyForSync
	s.traces[node.NodeID] = append([]ase.NodeExecutionStep(nil), node.ExecutionTrace...)
	return nil
}

func (s *inMemoryWorkerStore) CacheActiveAgent(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[node.NodeID] = node
	return nil
}

func (s *inMemoryWorkerStore) GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState)) (*ase.AutonomousSemanticEngineNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, ok := s.agents[nodeID]
	if !ok {
		return nil, fmt.Errorf("agent %s not found in memory", nodeID)
	}
	if onStateChange != nil {
		agent.SetOnStateChange(onStateChange)
	}
	return agent, nil
}

func (s *inMemoryWorkerStore) RemoveCachedAgent(ctx context.Context, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, nodeID)
	return nil
}

func (s *inMemoryWorkerStore) AcquireLock(ctx context.Context, nodeID string) (bool, error) {
	return true, nil
}

func (s *inMemoryWorkerStore) ReleaseLock(ctx context.Context, nodeID string) error {
	return nil
}

func (s *inMemoryWorkerStore) GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trace, ok := s.traces[nodeID]
	if !ok {
		return []byte("[]"), nil
	}
	return json.Marshal(trace)
}

func (s *inMemoryWorkerStore) UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[nodeID] = state
	if agent, ok := s.agents[nodeID]; ok {
		agent.CurrentState = state
	}
	return nil
}
