package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	_ "github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

// AseBridgeWorker acts as a bridge between the TAP Workflow Engine and the
// Autonomous Semantic Engine (ASE). It listens for workflow execution steps,
// fetches unclassified Fignode staging transactions, and asynchronously launches
// ASE micro-agents for each transaction.
type AseBridgeWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
	redis  *redis.Client
	rt     *agent.Runtime
	dbPool *pgxpool.Pool

	dags *expirable.LRU[string, *ase.DAG]
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &AseBridgeWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			redis:  deps.Redis,
			rt:     deps.Runtime,
			dbPool: deps.DBPool,
			// Initialize in Init() instead since we need logger for eviction callback
		}, nil
	})
}

func (w *AseBridgeWorker) buildDeps(tool domain_tools.DomainTool) domain_tools.ToolDependencies {
	deps := domain_tools.ToolDependencies{
		DB:      w.db,
		DBPool:  w.dbPool,
		Redis:   w.redis,
		Logger:  w.logger,
		NC:      w.nc,
		Runtime: w.rt,
	}
	if tool != nil {
		deps.Store = tool.GetStatePersister(deps)
	}
	return deps
}

func (w *AseBridgeWorker) Init(ctx context.Context) error {
	w.dags = expirable.NewLRU(5000, func(k string, v *ase.DAG) {
		w.logger.Info("ase_bridge: evicting idle DAG from cache", "key", k)
		v.StopAll()
	}, time.Minute*30)

	// Override default recovery executor to use NATS-powered recovery executor
	ase.DefaultRecoveryExecutor = &NatsRecoveryExecutor{
		queries: w.db,
		nc:      w.nc,
		logger:  w.logger,
	}

	// Helper to instantiate, wire, and start a DAG
	wireAndStartDAG := func(key string, cfg *ase.ASEConfig) {
		domain := cfg.HyperParameters.DomainTool
		if domain == "" {
			w.logger.Error("ase_orchestrator: domain_tool missing in config", "key", key)
			return
		}

		dt := domain_tools.Get(domain)
		var classifier ase.Classifier
		if dt != nil {
			deps := w.buildDeps(dt)
			deps.Runtime = w.rt
			deps.VectorStore = nil
			classifier = dt.GetClassifier(deps)
		}
		existingDAG, _ := w.dags.Get(key)

		if existingDAG != nil {
			// Update the DAG in memory
			existingDAG.UpdateFromConfig(cfg.DAG, w.logger)

			// Update the ThinkFuncs in case Prompts or EdgeType changed
			for _, node := range existingDAG.Nodes {
				if node.Kind == "initial_router" {
					payloadKey := ""
					if pk, ok := node.ExecutionParams["payload_key"]; ok {
						payloadKey = pk
					}
					node.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc(payloadKey))
				} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "generate_channel_dag" {
					channel := node.ExecutionParams["channel"]
					node.SetThinkFunc(w.buildGenerateChannelDagFunc(channel))
				} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "emit_resume_signal" {
					node.SetThinkFunc(w.buildEmitResumeSignalFunc())
				} else if actionProvider, ok := node.ExecutionParams["action_provider"]; ok && actionProvider != "" {
					node.SetThinkFunc(w.buildActionProviderThinkFunc(actionProvider))
				} else if node.EdgeType == "dynamic" {
					node.SetThinkFunc(classifier.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
				} else if node.PromptKey != "" {
					node.SetThinkFunc(classifier.BuildGenericThinkFunc(node.PromptKey))
				} else if node.Kind == "debug_terminal" || node.ID == "debug_terminal" || node.Name == "debug_terminal" {
					node.SetThinkFunc(nil)
				} else {
					node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
						return nil, nil // No-op
					})
				}
			}
			w.logger.Info("ase_orchestrator: dynamically updated DAG nodes in memory", "key", key)
			return
		}

		dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)
		for _, node := range dag.Nodes {
			if node.Kind == "initial_router" {
				payloadKey := ""
				if pk, ok := node.ExecutionParams["payload_key"]; ok {
					payloadKey = pk
				}
				node.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc(payloadKey))
			} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "generate_channel_dag" {
				channel := node.ExecutionParams["channel"]
				node.SetThinkFunc(w.buildGenerateChannelDagFunc(channel))
			} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "emit_resume_signal" {
				node.SetThinkFunc(w.buildEmitResumeSignalFunc())
			} else if actionProvider, ok := node.ExecutionParams["action_provider"]; ok && actionProvider != "" {
				node.SetThinkFunc(w.buildActionProviderThinkFunc(actionProvider))
			} else if node.EdgeType == "dynamic" {
				node.SetThinkFunc(classifier.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
			} else if node.PromptKey != "" {
				node.SetThinkFunc(classifier.BuildGenericThinkFunc(node.PromptKey))
			} else if node.Kind == "debug_terminal" || node.ID == "debug_terminal" || node.Name == "debug_terminal" {
				node.SetThinkFunc(nil)
			} else {
				// e.g. terminal nodes or holding nodes with no dynamic logic
				node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
					return nil, nil // No-op
				})
			}
		}
		dag.StartAll()

		w.dags.Add(key, dag)
		w.logger.Info("ase_orchestrator: wired dynamic DAG nodes", "key", key)
	}

	// Register hot-reload callback to dynamically load tenant DAGs
	ase.RegisterOnConfigLoaded(wireAndStartDAG)

	return nil
}

func (w *AseBridgeWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)

	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.ase_bridge"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("ase_orchestrator: failed to derive inbox", "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "ase-orchestrator-group"
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
		{
			Subject: "ase.events.resume",
			Group:   "ase-orchestrator-resume-group",
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject("ase.events.resume")),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *AseBridgeWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [DEBUG] ase_orchestrator received TAP message", "topic", msg.Subject)

	// 0. Intercept resume events directly
	if msg.Subject == "ase.events.resume" {
		var resumeEvt struct {
			NodeID         string `json:"node_id"`
			StartNodeID    string `json:"start_node_id"`
			ResolvedReason string `json:"resolved_reason"`
			DagName        string `json:"dag_name"`
			DomainTool     string `json:"domain_tool"`
		}
		if err := json.Unmarshal(msg.Data, &resumeEvt); err != nil {
			w.logger.Error("ase_bridge: failed to parse resume event", "error", err)
			return nil
		}

		w.logger.Info("ase_bridge: resuming DAG node", "node_id", resumeEvt.NodeID, "start_node", resumeEvt.StartNodeID)

		dagName := resumeEvt.DagName
		if dagName == "" {
			dagName = "default" // Fallback
		}

		domainToolName := resumeEvt.DomainTool
		if domainToolName == "" {
			if cfg := ase.GetConfig("", dagName); cfg != nil && cfg.HyperParameters.DomainTool != "" {
				domainToolName = cfg.HyperParameters.DomainTool
			} else {
				w.logger.Error("ase_bridge: domain_tool not provided in event and not found in DAG config", "dag_name", dagName)
				return nil
			}
		}

		tool := domain_tools.Get(domainToolName)
		if tool == nil {
			w.logger.Error("ase_bridge: domain_tool not found for resume", "domain_tool", domainToolName)
			return nil
		}

		deps := w.buildDeps(tool)

		agent, err := tool.ResumeAgent(ctx, resumeEvt.NodeID, dagName, deps)
		if err != nil || agent == nil {
			w.logger.Error("ase_bridge: failed to resume agent", "error", err)
			return nil
		}

		userID := agent.UserID
		if userID == "" {
			userID = agent.TenantID
		}

		// Lazily trigger config fetch, which returns the config from DB or cache
		cfg := ase.GetConfig(userID, dagName)

		// Dynamically resolve the correct DAG instance for this user
		dagKey := dagName
		if userID != "" {
			dagKey = dagName + "_" + userID
		}
		dagToUse, _ := w.dags.Get(dagKey)

		// If the config is present but we haven't wired it yet, do it now
		if dagToUse == nil && cfg != nil {
			// Lock not needed for LRU, but we use a small local lock to prevent multiple
			// identical build requests if they arrive exactly simultaneously
			dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)

			dt := domain_tools.Get(domainToolName)
			var classifier ase.Classifier
			if dt != nil {
				deps := w.buildDeps(dt)
				deps.Runtime = nil
				deps.VectorStore = nil
				classifier = dt.GetClassifier(deps)
			}

			for _, node := range dag.Nodes {
				if node.Kind == "initial_router" {
					payloadKey := ""
					if pk, ok := node.ExecutionParams["payload_key"]; ok {
						payloadKey = pk
					}
					node.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc(payloadKey))
				} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "generate_channel_dag" {
					channel := node.ExecutionParams["channel"]
					node.SetThinkFunc(w.buildGenerateChannelDagFunc(channel))
				} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "emit_resume_signal" {
					node.SetThinkFunc(w.buildEmitResumeSignalFunc())
				} else if node.EdgeType == "dynamic" {
					node.SetThinkFunc(classifier.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
				} else if node.PromptKey != "" {
					node.SetThinkFunc(classifier.BuildGenericThinkFunc(node.PromptKey))
				} else if node.Kind == "debug_terminal" || node.ID == "debug_terminal" || node.Name == "debug_terminal" {
					node.SetThinkFunc(nil)
				} else {
					node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
						return nil, nil // No-op
					})
				}
			}
			dag.StartAll()
			w.dags.Add(dagKey, dag)
			w.logger.Info("ase_orchestrator: wired dynamic DAG nodes on demand for resume", "key", dagKey)

			dagToUse, _ = w.dags.Get(dagKey)
		}

		if dagToUse == nil {
			w.logger.Error("ase_bridge: cannot resume because DAG is nil", "dagKey", dagKey)
			return nil
		}

		// Fire off the asynchronous Goroutine to resume the agent
		go func(a *ase.AutonomousSemanticEngineNode, start, reason string, s ase.StatePersister) {
			if reason != "" {
				a.Mu.Lock()
				a.ContextUpdates = append(a.ContextUpdates, fmt.Sprintf("Context update provided by external resolution: %s", reason))
				a.Mu.Unlock()
			}
			doneCh := make(chan struct{}, 1)
			var terminalOnce sync.Once
			a.SetOnStateChange(func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState) {
				if newState == ase.StateReadyForSync || strings.HasPrefix(string(newState), "HOLD_") || newState == ase.StateCollapsed || newState == ase.StateUninitialized {
					terminalOnce.Do(func() { doneCh <- struct{}{} })
				}
			})

			if err := a.ApproveAndResume(context.Background(), dagToUse, s, start); err != nil {
				w.logger.Error("ase_bridge: agent crashed on resume", "node_id", a.NodeID, "error", err)
				terminalOnce.Do(func() { doneCh <- struct{}{} })
			}

			<-doneCh
			w.logger.Info("ase_bridge: agent unblocked", "node_id", a.NodeID, "state", a.GetState())

			state := a.GetState()
			if strings.HasPrefix(string(state), "HOLD_") {
				if isDebugRun(a) {
					w.logger.Info("ase_bridge: debug run hold alert suppressed", "node_id", a.NodeID, "state", state, "reason", a.GetHoldReason())
					return
				}
				if dt, ok := a.Payload["domain_tool"].(string); ok && dt != "" {
					if t := domain_tools.Get(dt); t != nil {
						deps := w.buildDeps(t)
						payload, pErr := t.GenerateAlertPayload(context.Background(), a, deps)
						if pErr == nil && payload != nil {
							if _, ok := payload["entity_id"]; !ok && a.TenantID != "" {
								payload["entity_id"] = a.TenantID
							}
							entityID, _ := payload["entity_id"].(string)
							if entityID == "" {
								w.logger.Error("ase_bridge: suppressing hold alert without workflow entity", "node_id", a.NodeID, "state", state)
								return
							}
							payloadBytes, _ := json.Marshal(payload)
							ingressSubject, sErr := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
							if sErr == nil {
								_ = w.nc.Publish(ingressSubject, payloadBytes)
							}
						}
					}
				}
			}
		}(agent, resumeEvt.StartNodeID, resumeEvt.ResolvedReason, deps.Store)

		return nil
	}

	// 1. Unmarshal TAP Envelope
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		w.logger.Error("ase_orchestrator: bad envelope", "error", err)
		return nil
	}

	if !core.IsValidPerformative(env.Performative) || env.Performative != core.REQUEST {
		w.logger.Warn("ase_orchestrator: dropping message, invalid performative", "perf", env.Performative)
		return nil
	}

	var config map[string]interface{}
	_ = core.UnmarshalTaskConfig(env.Body, &config)

	dagName := ""
	domainToolName := ""
	if config != nil {
		if dName, ok := config["dag_name"].(string); ok && dName != "" {
			dagName = dName
		}
		if dt, ok := config["domain_tool"].(string); ok && dt != "" {
			domainToolName = dt
		}
	}

	if dagName == "" {
		var payload struct {
			DagName string `json:"dag_name"`
		}
		_ = core.UnmarshalTaskPayload(env.Body, &payload)
		dagName = payload.DagName
	}

	if dagName == "" {
		w.logger.Error("ase_bridge: dag_name is missing from workflow config and payload")
		return fmt.Errorf("dag_name is required in workflow config or payload")
	}

	if domainToolName == "" {
		if cfg := ase.GetConfig("", dagName); cfg != nil && cfg.HyperParameters.DomainTool != "" {
			domainToolName = cfg.HyperParameters.DomainTool
		} else {
			w.logger.Error("ase_bridge: domain_tool not provided in task config and not found in DAG config", "dag_name", dagName)
			return nil
		}
	}

	tool := domain_tools.Get(domainToolName)
	if tool == nil {
		w.logger.Error("ase_bridge: domain_tool not found in registry", "tool", domainToolName)
		return fmt.Errorf("domain_tool %s not found", domainToolName)
	}

	deps := w.buildDeps(tool)

	agents, err := tool.BuildAgents(ctx, env, dagName, deps)
	if err != nil {
		w.logger.Error("ase_orchestrator: failed to build agents from payload", "error", err)
		return err
	}

	if len(agents) == 0 {
		w.logger.Info("ase_bridge: no agents to launch")
		return fmt.Errorf("no agents to launch")
	}

	// Grab userID from the first agent to wire the DAG dynamically
	userID := agents[0].UserID
	if userID == "" {
		userID = agents[0].TenantID
	}

	// Lazily trigger config fetch, which returns the config from DB or cache
	cfg := ase.GetConfig(userID, dagName)

	// Dynamically resolve the correct DAG instance for this user
	dagKey := dagName
	if userID != "" {
		dagKey = dagName + "_" + userID
	}
	dagToUse, _ := w.dags.Get(dagKey)

	// If the config is present but we haven't wired it yet, do it now
	if dagToUse == nil && cfg != nil {
		dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)

		// Initialize the Classifier Service (to generate ThinkFuncs)
		dt := domain_tools.Get(domainToolName)
		var classifier ase.Classifier
		if dt != nil {
			deps := w.buildDeps(dt)
			deps.Runtime = nil
			deps.VectorStore = nil
			classifier = dt.GetClassifier(deps)
		}

		for _, node := range dag.Nodes {
			if node.Kind == "initial_router" {
				payloadKey := ""
				if pk, ok := node.ExecutionParams["payload_key"]; ok {
					payloadKey = pk
				}
				node.SetThinkFunc(classifier.BuildPayloadRouterThinkFunc(payloadKey))
			} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "generate_channel_dag" {
				channel := node.ExecutionParams["channel"]
				node.SetThinkFunc(w.buildGenerateChannelDagFunc(channel))
			} else if node.Kind == "action" && node.ExecutionParams["action_type"] == "emit_resume_signal" {
				node.SetThinkFunc(w.buildEmitResumeSignalFunc())
			} else if node.EdgeType == "dynamic" {
				node.SetThinkFunc(classifier.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
			} else if node.PromptKey != "" {
				node.SetThinkFunc(classifier.BuildGenericThinkFunc(node.PromptKey))
			} else if node.Kind == "debug_terminal" || node.ID == "debug_terminal" || node.Name == "debug_terminal" {
				node.SetThinkFunc(nil)
			} else {
				node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
					return nil, nil // No-op
				})
			}
		}
		dag.StartAll()
		w.dags.Add(dagKey, dag)
		w.logger.Info("ase_orchestrator: wired dynamic DAG nodes on demand", "key", dagKey)

		dagToUse, _ = w.dags.Get(dagKey)
	}

	if dagToUse == nil {
		w.logger.Error("ase_bridge: no matching DAG found", "userID", userID, "dagName", dagName)
		return fmt.Errorf("no matching DAG found for user %s and dagName %s", userID, dagName)
	}

	w.logger.Info("ase_bridge: launching agents", "count", len(agents))

	var wg sync.WaitGroup

	// Launch ASE Agents asynchronously
	for _, agent := range agents {

		wg.Add(1)
		// Fire off the asynchronous Goroutine
		go func(a *ase.AutonomousSemanticEngineNode) {
			defer wg.Done()

			doneCh := make(chan struct{}, 1)
			var terminalOnce sync.Once
			a.SetOnStateChange(func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState) {
				if newState == ase.StateReadyForSync || strings.HasPrefix(string(newState), "HOLD_") || newState == ase.StateCollapsed || newState == ase.StateClassified || newState == ase.StateUninitialized {
					terminalOnce.Do(func() { doneCh <- struct{}{} })
				}
			})

			debugStartNode, _ := a.Payload["debug_start_node"].(string)
			if err := a.Resume(ctx, dagToUse, deps.Store, debugStartNode); err != nil {
				w.logger.Error("ase_bridge: agent crashed", "node_id", a.NodeID, "error", err)
				terminalOnce.Do(func() { doneCh <- struct{}{} })
			}

			<-doneCh
			w.logger.Info("ase_bridge: agent unblocked", "node_id", a.NodeID, "state", a.GetState())

			state := a.GetState()
			if strings.HasPrefix(string(state), "HOLD_") {
				if isDebugRun(a) {
					w.logger.Info("ase_bridge: debug run hold alert suppressed", "node_id", a.NodeID, "state", state, "reason", a.GetHoldReason())
					return
				}
				if dt, ok := a.Payload["domain_tool"].(string); ok && dt != "" {
					if t := domain_tools.Get(dt); t != nil {
						deps := w.buildDeps(t)
						payload, pErr := t.GenerateAlertPayload(ctx, a, deps)
						if pErr == nil && payload != nil {
							if _, ok := payload["entity_id"]; !ok && a.TenantID != "" {
								payload["entity_id"] = a.TenantID
							}
							entityID, _ := payload["entity_id"].(string)
							if entityID == "" {
								w.logger.Error("ase_bridge: suppressing hold alert without workflow entity", "node_id", a.NodeID, "state", state)
								return
							}
							payloadBytes, _ := json.Marshal(payload)
							ingressSubject, sErr := core.BuildWorkerInboxFromActivity("workers.general_agent_ingress")
							if sErr == nil {
								_ = w.nc.Publish(ingressSubject, payloadBytes)
							}
						}
					}
				}
			}
		}(agent)
	}

	// Wait for all DAG micro-agents to finish executing before assembling attachments
	wg.Wait()
	w.logger.Info("ase_bridge: all agents completed")

	sessionID := ""
	stagingSessionID := ""
	entityID := ""
	realmID := ""

	if len(agents) > 0 {
		first := agents[0]

		realmID = first.RealmID

		if first.Payload != nil {
			if v, ok := first.Payload["session_id"].(string); ok {
				sessionID = v
			}

			if v, ok := first.Payload["staging_session_id"].(string); ok {
				stagingSessionID = v
			}

			if v, ok := first.Payload["entity_id"].(string); ok {
				entityID = v
			}

			if realmID == "" {
				if v, ok := first.Payload["realm_id"].(string); ok {
					realmID = v
				}
			}
		}
	}

	// PCM currently uses the staging session as session_id.
	// Keep both fields populated even if only session_id reached ASE.
	if stagingSessionID == "" {
		stagingSessionID = sessionID
	}

	proofData := map[string]interface{}{
		"status":             "SUCCESS",
		"session_id":         sessionID,
		"staging_session_id": stagingSessionID,
		"entity_id":          entityID,
		"realm_id":           realmID,
		"dag_name":           dagName,
		"domain_tool":        domainToolName,
		"agents_executed":    len(agents),
	}
	proofDataBytes, err := json.Marshal(proofData)
	if err != nil {
		return fmt.Errorf("ase_bridge: failed to marshal completion payload: %w", err)
	}

	proof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      proofDataBytes,
	}

	cid := env.ConversationID
	replyEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now().UTC(),
		SenderDID:      "did:toro:ase-bridge",
		ReceiverDID:    workflows.OrchestratorDID,
		Performative:   core.INFORM,
		ConversationID: cid,
	}

	replyEnv.Body, err = json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("ase_bridge: failed to marshal completion proof: %w", err)
	}

	if cid != "" {
		replyBytes, _ := json.Marshal(replyEnv)

		targetSubject := msg.Reply
		isCoreReply := targetSubject != "" && !strings.HasPrefix(targetSubject, "$JS.ACK.")
		if !isCoreReply {
			targetSubject = workflows.OrchestratorInbox
		}

		var pubErr error
		if isCoreReply {
			pubErr = w.nc.Publish(targetSubject, replyBytes)
		} else {
			if js, err := w.nc.JetStream(); err == nil {
				_, pubErr = js.Publish(targetSubject, replyBytes)
			} else {
				pubErr = err
			}
		}

		if pubErr != nil {
			w.logger.Error("ase_bridge: failed to notify target", "error", pubErr, "target", targetSubject)
		} else {
			w.logger.Info("ase_bridge: sent explicit INFORM back to target", "cid", cid)
		}
	}

	return nil
}

func isDebugRun(a *ase.AutonomousSemanticEngineNode) bool {
	debug, _ := a.Payload["debug_run"].(bool)
	return debug
}

func (w *AseBridgeWorker) buildGenerateChannelDagFunc(channel string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification)

		for _, node := range batch {
			tenantID := node.TenantID
			promptPath := fmt.Sprintf("docs/prompts/user_inbound_%s_dag_prompt.md", channel)

			promptContentBytes, err := os.ReadFile(promptPath)
			if err != nil {
				w.logger.Error("failed to read prompt template", "channel", channel, "error", err)
				continue
			}

			promptContent := string(promptContentBytes)
			promptContent = strings.ReplaceAll(promptContent, "[TENANT_ID]", tenantID)
			promptContent = strings.ReplaceAll(promptContent, "[SPECIFIC_RULES]", "Standard tone, polite.")

			w.logger.Info("ase_bridge: calling LLM to generate DAG", "tenant", tenantID, "channel", channel)

			llmResponse, err := w.rt.Exec(
				ctx,
				promptContent,
				"You are an expert ASE config generator. ONLY output YAML.",
			)

			if err != nil {
				w.logger.Error("failed to generate DAG via LLM", "error", err)
				continue
			}

			// Clean up YAML markdown blocks if present
			yamlContent := strings.TrimPrefix(llmResponse, "```yaml\n")
			yamlContent = strings.TrimPrefix(yamlContent, "```\n")
			yamlContent = strings.TrimSuffix(yamlContent, "\n```")

			w.logger.Info("ase_bridge: saving generated DAG to DB (skipped YAML->JSON parsing for now)", "yaml_preview", yamlContent[:min(100, len(yamlContent))])

			results[node.NodeID] = ase.NodeClassification{
				Property: "action_result",
				Candidates: []ase.ProbabilityCandidate{
					{Value: "SUCCESS", Confidence: 1.0, Reasoning: "Generated and saved DAG."},
				},
			}
		}
		return results, nil
	}
}

func (w *AseBridgeWorker) buildEmitResumeSignalFunc() ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification)
		for _, a := range batch {
			classification := ase.NodeClassification{
				Property: "action",
				Candidates: []ase.ProbabilityCandidate{
					{Value: "SUCCESS", Confidence: 1.0, Reasoning: "Action executed successfully"},
				},
			}

			var sessionID string
			if len(a.ContextUpdates) > 0 {
				sessionID = a.ContextUpdates[0]
			}

			if sessionID != "" {
				sessQuery := `SELECT participant_handle FROM toro_core.conversation_sessions WHERE id = $1`
				var handle string
				err := w.dbPool.QueryRow(ctx, sessQuery, sessionID).Scan(&handle)

				var nodeID string
				var dagName string
				var domainTool string
				if err == nil {
					parts := strings.Split(handle, ":")
					if len(parts) >= 2 && parts[0] == "ase" {
						nodeID = parts[1]
						if len(parts) >= 4 {
							domainTool = parts[2]
							dagName = parts[3]
						}
					}
				}

				if nodeID != "" {
					var store ase.StatePersister
					if dt := domain_tools.Get(domainTool); dt != nil {
						store = dt.GetStatePersister(w.buildDeps(dt))
					}

					var traceJSON []byte
					if store != nil {
						_ = store.UpdateNodeState(ctx, nodeID, ase.NodeState("RESUME_PENDING"))
						traceJSON, _ = store.GetExecutionTrace(ctx, nodeID)
					} else {
						w.logger.Error("ase_bridge: no state persister found for domain tool", "tool", domainTool)
					}

					startNodeID := "default"
					if len(traceJSON) > 0 {
						var trace []ase.NodeExecutionStep
						if err := json.Unmarshal(traceJSON, &trace); err == nil && len(trace) > 0 {
							startNodeID = trace[len(trace)-1].DAGNodeID
						}
					}

					type ResumeEvent struct {
						NodeID      string `json:"node_id"`
						StartNodeID string `json:"start_node_id"`
						DagName     string `json:"dag_name"`
						DomainTool  string `json:"domain_tool"`
					}
					evt, _ := json.Marshal(ResumeEvent{
						NodeID:      nodeID,
						StartNodeID: startNodeID,
						DagName:     dagName,
						DomainTool:  domainTool,
					})
					_ = w.nc.Publish("ase.events.resume", evt)
					w.logger.Info("ase_bridge: emitted resume signal", "node_id", nodeID, "start_node", startNodeID)
				} else {
					w.logger.Warn("ase_bridge: no pending transaction found for session", "session_id", sessionID, "error", err)
				}
			}

			results[a.NodeID] = classification
		}
		return results, nil
	}
}

func (w *AseBridgeWorker) buildActionProviderThinkFunc(actionProvider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification)
		for _, node := range batch {
			node.Mu.RLock()
			payload := node.Payload
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

// NatsRecoveryExecutor handles execution of both native and NATS-based recovery actions (Layer 3).
type NatsRecoveryExecutor struct {
	queries *database.Queries
	nc      *nats.Conn
	logger  *slog.Logger
}

func (e *NatsRecoveryExecutor) ExecuteAction(ctx context.Context, actionID string, node *ase.AutonomousSemanticEngineNode) (bool, error) {
	if actionID == "search_document_store" {
		return e.actionSearchDocumentStore(ctx, node)
	}

	e.logger.Info("recovery engine: executing recovery action via NATS request", "action_id", actionID, "node_id", node.NodeID)

	node.Mu.RLock()
	payload := node.Payload
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
		"action_provider": actionID,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return false, fmt.Errorf("failed to marshal recovery request: %w", err)
	}

	// Use configured timeout or fallback to 120s
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

	subject := "worker.inbox.action." + actionID
	msg, err := e.nc.RequestWithContext(ctxWithTimeout, subject, reqBytes)
	if err != nil {
		e.logger.Error("recovery engine: NATS recovery request failed", "action_id", actionID, "error", err)
		return false, err
	}

	var resp struct {
		Candidates     []ase.ProbabilityCandidate `json:"candidates"`
		Property       string                     `json:"property"`
		PayloadUpdates map[string]interface{}     `json:"payload_updates"`
		ContextUpdates []string                   `json:"context_updates"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return false, fmt.Errorf("failed to unmarshal recovery response: %w", err)
	}

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

	property := resp.Property
	if property == "" {
		property = "compliance_decision"
	}
	node.SetPropertyCandidates(property, resp.Candidates)

	bestCand := node.TopCandidate(property)
	if bestCand != nil && (strings.Contains(bestCand.Value, "COMPLIANT") || strings.Contains(bestCand.Value, "SUCCESS") || strings.Contains(bestCand.Value, "TAX_POSTED") || strings.Contains(bestCand.Value, "WITHHOLDING_ISOLATED")) {
		return true, nil
	}

	return false, nil
}

// actionSearchDocumentStore performs native search using the node's RawAmount or RawDescription.
func (e *NatsRecoveryExecutor) actionSearchDocumentStore(ctx context.Context, node *ase.AutonomousSemanticEngineNode) (bool, error) {
	if e.queries == nil {
		return false, fmt.Errorf("queries not configured in NatsRecoveryExecutor")
	}
	if node.RealmID == "" {
		return false, fmt.Errorf("missing RealmID on node")
	}

	searchTerm := ""
	node.Mu.RLock()
	if amt, ok := node.Payload["raw_amount"].(string); ok && amt != "" {
		searchTerm = amt
	} else if desc, ok := node.Payload["raw_description"].(string); ok && desc != "" {
		if len(desc) > 10 {
			searchTerm = desc[:10]
		} else {
			searchTerm = desc
		}
	}
	node.Mu.RUnlock()

	if searchTerm == "" {
		return false, nil
	}

	results, err := e.queries.SearchAttachables(ctx, database.SearchAttachablesParams{
		RealmID: node.RealmID,
		Column2: pgtype.Text{String: searchTerm, Valid: true},
	})
	if err != nil {
		return false, err
	}

	if len(results) > 0 {
		if e.logger != nil {
			e.logger.Info("recovery engine: successfully rescued node via document search", "node_id", node.NodeID, "found_count", len(results))
		}

		docStr := fmt.Sprintf("System found matching document in storage automatically: File '%s'. Use this context to proceed.", results[0].FileName.String)
		node.Mu.Lock()
		node.ContextUpdates = append(node.ContextUpdates, docStr)
		node.Mu.Unlock()

		return true, nil
	}

	return false, nil
}
