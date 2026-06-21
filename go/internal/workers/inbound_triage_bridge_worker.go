package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
)

// InboundTriageBridgeWorker executes an inbound email/message through a specific
// ASE DAG to extract intent, resolve context, and route the resolution.
type InboundTriageBridgeWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	db     *database.Queries
	store  *ase.StateStore
	dbPool *pgxpool.Pool

	dagsMu sync.RWMutex
	dags   map[string]*ase.DAG
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		stateStore := ase.NewStateStore(deps.DBPool, deps.Redis)

		return &InboundTriageBridgeWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			store:  stateStore,
			dbPool: deps.DBPool,
			dags:   make(map[string]*ase.DAG),
		}, nil
	})
}

func (w *InboundTriageBridgeWorker) wireNode(node *ase.DAGNode, classifierService *ase.ClassifierService) {
	if node.EdgeType == "dynamic" {
		node.SetThinkFunc(classifierService.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
	} else if node.PromptKey != "" {
		node.SetThinkFunc(classifierService.BuildGenericThinkFunc(node.PromptKey))
	} else if string(node.Kind) == "action" && node.ExecutionParams["action_type"] == "resume_bookkeeping_dag" {
		node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
			results := make(map[string]ase.NodeClassification)
			for _, a := range batch {
				// Default success classification
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
					// The sessionID here is a toro_core.conversation_sessions ID.
					// The transaction ID was embedded in the participant_handle as "ase-engine:<tx_id>".
					sessQuery := `SELECT participant_handle FROM toro_core.conversation_sessions WHERE id = $1`
					var handle string
					err := w.dbPool.QueryRow(ctx, sessQuery, sessionID).Scan(&handle)
					
					var nodeID string
					if err == nil && len(handle) > 11 && handle[:11] == "ase-engine:" {
						nodeID = handle[11:]
					}

					if nodeID != "" {
						// Mark as RESUME_PENDING
						updateQ := `UPDATE fignode.staging_transactions SET status = $2, updated_at = NOW() WHERE id = $1`
						_, _ = w.dbPool.Exec(ctx, updateQ, nodeID, "RESUME_PENDING")

						// Fetch ase_execution_trace to determine the correct start_node_id
						var traceJSON []byte
						traceQ := `SELECT ase_execution_trace FROM fignode.staging_transactions WHERE id = $1`
						_ = w.dbPool.QueryRow(ctx, traceQ, nodeID).Scan(&traceJSON)

						startNodeID := "default"
						if len(traceJSON) > 0 {
							var trace []ase.NodeExecutionStep
							if err := json.Unmarshal(traceJSON, &trace); err == nil && len(trace) > 0 {
								startNodeID = trace[len(trace)-1].DAGNodeID
							}
						}

						// Emit resume signal
						type ResumeEvent struct {
							NodeID      string `json:"node_id"`
							StartNodeID string `json:"start_node_id"`
						}
						evt, _ := json.Marshal(ResumeEvent{NodeID: nodeID, StartNodeID: startNodeID})
						_ = w.nc.Publish("ase.events.resume", evt)
						w.logger.Info("inbound_triage_bridge: emitted resume signal", "node_id", nodeID, "start_node", startNodeID)
					} else {
						w.logger.Warn("inbound_triage_bridge: no pending transaction found for session", "session_id", sessionID, "error", err)
					}
				}

				results[a.NodeID] = classification
			}
			return results, nil
		})
	} else {
		node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
			return nil, nil // No-op
		})
	}
}

func (w *InboundTriageBridgeWorker) Init(ctx context.Context) error {
	classifierService := ase.NewClassifierService(nil, w.nc, "tasks.accounting.1.batch_categorization", w.logger)
	classifierService.SetDB(w.db)

	wireAndStartDAG := func(key string, cfg *ase.ASEConfig) {
		w.dagsMu.RLock()
		existingDAG := w.dags[key]
		w.dagsMu.RUnlock()

		if existingDAG != nil {
			existingDAG.UpdateFromConfig(cfg.DAG, w.logger)
			for _, node := range existingDAG.Nodes {
				w.wireNode(node, classifierService)
			}
			w.logger.Info("inbound_triage_bridge: dynamically updated DAG nodes in memory", "key", key)
			return
		}

		dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)
		for _, node := range dag.Nodes {
			w.wireNode(node, classifierService)
		}
		dag.StartAll()

		w.dagsMu.Lock()
		w.dags[key] = dag
		w.dagsMu.Unlock()
		w.logger.Info("inbound_triage_bridge: wired dynamic DAG nodes", "key", key)
	}

	// Register hot-reload callback to dynamically load tenant DAGs
	ase.RegisterOnConfigLoaded(wireAndStartDAG)

	for key, cfg := range ase.GetAllConfigs() {
		wireAndStartDAG(key, cfg)
	}

	return nil
}

func (w *InboundTriageBridgeWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "worker.inbox.inbound_triage_bridge",
			Group:   "inbound-triage-bridge-group",
			Options: []nats.SubOpt{
				nats.Durable("worker-inbox-inbound_triage_bridge-durable"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *InboundTriageBridgeWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [DEBUG] inbound_triage_bridge received message")

	// Payload expected to come from general_agent_ingress_worker
	var payload struct {
		SessionID string `json:"session_id"`
		EntityID  string `json:"entity_id"`
		Prompt    string `json:"prompt"`
		DagName   string `json:"dag_name"`
	}

	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("inbound_triage_bridge: bad payload", "error", err)
		return nil
	}

	tenantID := payload.EntityID
	if tenantID != "" {
		parsedEntityID, err := uuid.Parse(tenantID)
		if err == nil {
			users, err := w.db.GetUsersByEntityID(ctx, pgtype.UUID{Bytes: parsedEntityID, Valid: true})
			if err == nil && len(users) > 0 {
				tenantID = uuid.UUID(users[0].ID.Bytes).String()
			}
		}
	}
	dagName := payload.DagName
	if dagName == "" {
		dagName = "default_inbound_email"
	}

	// Fetch or trigger DAG config load
	cfg := ase.GetConfig(tenantID, "", dagName)
	if cfg == nil && dagName != "default_inbound_email" {
		w.logger.Info("inbound_triage_bridge: specific channel DAG not found, falling back to default_inbound_email", "dagName", dagName)
		dagName = "default_inbound_email"
		cfg = ase.GetConfig(tenantID, "", dagName)
	}

	w.dagsMu.RLock()
	dagKey := "tenant_" + tenantID + "_" + dagName
	if tenantID == "" {
		dagKey = "global_" + dagName
	}
	dagToUse := w.dags[dagKey]
	w.dagsMu.RUnlock()

	if dagToUse == nil && cfg != nil {
		w.dagsMu.Lock()
		if w.dags[dagKey] == nil {
			dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)

			// Initialize the Classifier Service (to generate ThinkFuncs)
			classifierService := ase.NewClassifierService(nil, w.nc, "tasks.triage.1.intent_classification", w.logger)
			classifierService.SetDB(w.db)

			for _, node := range dag.Nodes {
				w.wireNode(node, classifierService)
			}
			dag.StartAll()
			w.dags[dagKey] = dag
			w.logger.Info("inbound_triage_bridge: wired dynamic DAG nodes on demand", "key", dagKey)
		}
		dagToUse = w.dags[dagKey]
		w.dagsMu.Unlock()
	}

	if dagToUse == nil {
		w.logger.Error("inbound_triage_bridge: no matching DAG found", "tenantID", tenantID, "dagName", dagName)
		return nil
	}

	// Create ASENode representing the email payload
	agent := ase.NewASENode(tenantID, "", dagName, payload.Prompt, "INFLOW", "0.00")
	if payload.SessionID != "" {
		agent.ContextUpdates = []string{payload.SessionID}
	}
	agent.SetLogger(w.logger)

	// Run agent through triage DAG
	go func(a *ase.AutonomousSemanticEngineNode) {
		if err := a.Run(context.Background(), dagToUse, w.store); err != nil {
			w.logger.Error("inbound_triage_bridge: agent crashed", "node_id", a.NodeID, "error", err)
		}

		w.logger.Info("inbound_triage_bridge: agent finished triage", "node_id", a.NodeID, "state", a.GetState())

		// Handle the response back to the original caller if needed
		// e.g. publish the TopCandidate output to OmniChat

		// If intent is resolved and route is triggered, the DAG nodes themselves
		// will emit the necessary resume signals (ase.events.resume)
	}(agent)

	return nil
}
