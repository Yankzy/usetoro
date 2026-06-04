package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
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
	store  *ase.StateStore

	dagsMu sync.RWMutex
	dags   map[string]*ase.DAG
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		// Initialize the ASE global dependencies
		stateStore := ase.NewStateStore(deps.DBPool, deps.Redis)

		return &AseBridgeWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			store:  stateStore,
			dags:   make(map[string]*ase.DAG),
		}, nil
	})
}

func (w *AseBridgeWorker) Init(ctx context.Context) error {
	// Start real-time debug visualizer server
	w.startDebugServer()

	// Initialize the Classifier Service (to generate ThinkFuncs)
	classifierService := ase.NewClassifierService(nil, w.nc, "tasks.accounting.1.batch_categorization", w.logger)
	classifierService.SetDB(w.db) // Pass db for dynamic provider

	// Helper to instantiate, wire, and start a DAG
	wireAndStartDAG := func(key string, cfg *ase.ASEConfig) {
		dag := ase.BuildDAGFromConfig(cfg.DAG, w.logger)
		for _, node := range dag.Nodes {
			if node.EdgeType == "dynamic" {
				node.SetThinkFunc(classifierService.BuildDynamicThinkFunc(node.DynamicEdgeProvider))
			} else if node.PromptKey != "" {
				node.SetThinkFunc(classifierService.BuildGenericThinkFunc(node.PromptKey))
			} else {
				// e.g. terminal nodes or holding nodes with no dynamic logic
				node.SetThinkFunc(func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
					return nil, nil // No-op
				})
			}
		}
		dag.StartAll()

		w.dagsMu.Lock()
		w.dags[key] = dag
		w.dagsMu.Unlock()
		w.logger.Info("ase_orchestrator: wired dynamic DAG nodes", "key", key)
	}

	// Register hot-reload callback to dynamically load tenant DAGs
	ase.SetOnConfigLoaded(wireAndStartDAG)

	// Process any already loaded configs
	for key, cfg := range ase.GetAllConfigs() {
		wireAndStartDAG(key, cfg)
	}

	return nil
}

func (w *AseBridgeWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("ase_orchestrator: no activity_type configured")
		return nil
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
				nats.Durable("ase-orchestrator-worker"),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
		{
			Subject: "ase.events.resume",
			Group:   "ase-orchestrator-resume-group",
			Options: []nats.SubOpt{
				nats.Durable("ase-orchestrator-resume"),
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
			NodeID      string `json:"node_id"`
			StartNodeID string `json:"start_node_id"`
		}
		if err := json.Unmarshal(msg.Data, &resumeEvt); err != nil {
			w.logger.Error("ase_bridge: failed to parse resume event", "error", err)
			return nil
		}
		w.logger.Info("ase_bridge: resuming DAG node", "node_id", resumeEvt.NodeID, "start_node", resumeEvt.StartNodeID)
		
		idUUID, err := uuid.Parse(resumeEvt.NodeID)
		if err != nil {
			return nil
		}
		pgID := pgtype.UUID{Bytes: idUUID, Valid: true}
		dbTx, err := w.db.GetProposedTransactionByID(ctx, pgID)
		if err != nil {
			w.logger.Error("ase_bridge: failed to fetch tx for resume", "error", err)
			return nil
		}
		
		session, err := w.db.GetCleanupSession(ctx, dbTx.SessionID)
		if err != nil {
			return nil
		}
		
		outflowIs := session.OutflowIs
		realmID := ""
		if session.RealmID.Valid {
			realmID = session.RealmID.String
		}
		tenantID := ""
		if session.CreatedBy.Valid {
			tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
		}

		w.dagsMu.RLock()
		dagToUse := w.dags["default"]
		if tenantID != "" && w.dags["tenant_"+tenantID] != nil {
			dagToUse = w.dags["tenant_"+tenantID]
		} else if realmID != "" && w.dags["realm_"+realmID] != nil {
			dagToUse = w.dags["realm_"+realmID]
		}
		w.dagsMu.RUnlock()

		agent := w.mapToAgent(dbTx, outflowIs, tenantID, realmID)
		
		// Fire off the asynchronous Goroutine to resume the agent
		go func(a *ase.AutonomousSemanticEngineNode, start string, sID string) {
			if err := a.ApproveAndResume(context.Background(), dagToUse, w.store, start); err != nil {
				w.logger.Error("ase_bridge: agent crashed on resume", "node_id", a.NodeID, "error", err)
			}
			
			state := a.GetState()
			if state == ase.StateHoldMissingCtx || state == ase.StateHoldAmbiguous {
				w.dispatchToContextGatheringAgent(context.Background(), a, sID)
			}
		}(agent, resumeEvt.StartNodeID, uuid.UUID(session.ID.Bytes).String())
		
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

	// 2. Extract TaskPayload
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil || payload.SessionID == "" {
		w.logger.Error("ase_orchestrator: failed to unmarshal custom payload or missing session_id", "error", err)
		return nil
	}

	// Determine session_id (used to scope the staging transactions)
	sessionID := payload.SessionID
	var pgSessionID pgtype.UUID
	if err := pgSessionID.Scan(sessionID); err != nil {
		w.logger.Error("ase_orchestrator: invalid session_id UUID", "session", sessionID)
		return nil
	}

	w.logger.Info("ase_orchestrator: querying pending transactions", "session_id", sessionID)

	// Fetch session to determine OutflowIs logic
	session, err := w.db.GetCleanupSession(ctx, pgSessionID)
	if err != nil {
		w.logger.Error("ase_orchestrator: failed to fetch session", "error", err)
		return err
	}
	outflowIs := session.OutflowIs
	realmID := ""
	if session.RealmID.Valid {
		realmID = session.RealmID.String
	}
	tenantID := ""
	if session.CreatedBy.Valid {
		tenantID = uuid.UUID(session.CreatedBy.Bytes).String()
	}

	// Dynamically resolve the correct DAG instance for this tenant/realm
	w.dagsMu.RLock()
	dagToUse := w.dags["default"]
	if tenantID != "" && w.dags["tenant_"+tenantID] != nil {
		dagToUse = w.dags["tenant_"+tenantID]
	} else if realmID != "" && w.dags["realm_"+realmID] != nil {
		dagToUse = w.dags["realm_"+realmID]
	}
	w.dagsMu.RUnlock()

	if dagToUse == nil {
		w.logger.Error("ase_bridge: no matching DAG found, not even default", "tenantID", tenantID, "realmID", realmID)
		return nil
	}

	// 3. Query unclassified Fignode transactions
	pendingTxns, err := w.db.GetPendingStagingTransactions(ctx, pgSessionID)
	if err != nil {
		w.logger.Error("ase_orchestrator: failed to fetch pending staging transactions", "error", err)
		return err // Transient error, allow NATS redelivery
	}

	w.logger.Info("ase_bridge: launching agents", "count", len(pendingTxns))

	var wg sync.WaitGroup

	// 4. Launch ASE Agents asynchronously
	for _, txn := range pendingTxns {
		agent := w.mapToAgent(txn, outflowIs, tenantID, realmID)

		wg.Add(1)
		// Fire off the asynchronous Goroutine
		go func(a *ase.AutonomousSemanticEngineNode) {
			defer wg.Done()
			if err := a.Run(ctx, dagToUse, w.store); err != nil {
				w.logger.Error("ase_bridge: agent crashed", "node_id", a.NodeID, "error", err)
			}
			
			state := a.GetState()
			if state == ase.StateHoldMissingCtx || state == ase.StateHoldAmbiguous {
				w.dispatchToContextGatheringAgent(ctx, a, sessionID)
			}
		}(agent)
	}

	// 5. Wait for all agents to finish, then reply to Orchestrator.
	wg.Wait()

	cid := env.ConversationID
	if cid != "" {
		replyEnv := core.Envelope{
			ID:             uuid.New().String(),
			Timestamp:      time.Now().UTC(),
			SenderDID:      "did:toro:ase-bridge",
			ReceiverDID:    workflows.OrchestratorDID,
			Performative:   core.INFORM,
			ConversationID: cid,
			Body:           env.Body, // Echo back the body
		}
		
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

func (w *AseBridgeWorker) dispatchToContextGatheringAgent(ctx context.Context, a *ase.AutonomousSemanticEngineNode, sessionID string) {
	payload := map[string]interface{}{
		"prompt": fmt.Sprintf("The following transaction requires context gathering to resolve ambiguity:\n\nDescription: %s\nAmount: %s\nCash Direction: %s\nHolding Reason: %s\nTenant ID: %s\nRealm ID: %s",
			a.RawDescription, a.RawAmount, a.CashDirection, a.HoldReason, a.TenantID, a.RealmID),
		"node_id":        a.NodeID,
		"session_id":     sessionID,
		"hold_reason":    a.HoldReason,
		"cash_direction": a.CashDirection,
		"description":    a.RawDescription,
		"amount":         a.RawAmount,
		"tenant_id":      a.TenantID,
		"realm_id":       a.RealmID,
	}

	payloadBytes, _ := json.Marshal(payload)

	env := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now().UTC(),
		SenderDID:      "did:toro:ase-bridge",
		ReceiverDID:    "did:toro:context-gathering-agent",
		Performative:   core.REQUEST,
		ConversationID: a.NodeID, // using NodeID to group conversational turns
		Body:           payloadBytes,
	}
	envBytes, _ := json.Marshal(env)

	// Since ContextGatheringAgent is registered with ActivityType "agents.context_gathering",
	// its inbox subject is "worker.inbox.agents.context_gathering"
	targetSubject, _ := core.BuildWorkerInboxFromActivity("agents.context_gathering")
	if targetSubject == "" {
		targetSubject = "worker.inbox.agents.context_gathering"
	}

	err := w.nc.Publish(targetSubject, envBytes)
	if err != nil {
		w.logger.Error("ase_bridge: failed to dispatch to context gathering agent", "node", a.NodeID, "error", err)
	} else {
		w.logger.Info("ase_bridge: dispatched node to context gathering agent", "node", a.NodeID, "reason", a.HoldReason)
	}
}

func (w *AseBridgeWorker) mapToAgent(txn database.FignodeStagingTransaction, outflowIs, tenantID, realmID string) *ase.AutonomousSemanticEngineNode {
	desc := ""
	if txn.RawDescription.Valid {
		desc = txn.RawDescription.String
	}

	direction := "OUTFLOW"
	if txn.CashDirection.Valid && txn.CashDirection.String != "" {
		direction = txn.CashDirection.String
	} else {
		amtStr := strings.ReplaceAll(txn.RawAmount, ",", "")
		amtStr = strings.ReplaceAll(amtStr, "$", "")
		amtStr = strings.TrimSpace(amtStr)
		isNegativeFormat := false
		if strings.HasPrefix(amtStr, "(") && strings.HasSuffix(amtStr, ")") {
			amtStr = strings.Trim(amtStr, "()")
			isNegativeFormat = true
		}
		if amt, err := strconv.ParseFloat(amtStr, 64); err == nil {
			if isNegativeFormat {
				amt = -amt
			}
			isOutflow := false
			if outflowIs == "" || outflowIs == "NEGATIVE" {
				isOutflow = amt < 0
			} else {
				isOutflow = amt > 0
			}
			if isOutflow {
				direction = "OUTFLOW"
			} else {
				direction = "INFLOW"
			}
		}
	}

	agent := ase.NewASENode(tenantID, realmID, desc, direction, txn.RawAmount)
	agent.NodeID = uuid.UUID(txn.ID.Bytes).String()
	agent.SetLogger(w.logger)
	
	if len(txn.AseExecutionTrace) > 0 {
		_ = json.Unmarshal(txn.AseExecutionTrace, &agent.ExecutionTrace)
		// Rehydrate Candidates map to preserve previous properties during Resume
		for _, step := range agent.ExecutionTrace {
			if step.PropertyKey != "" && len(step.Candidates) > 0 {
				agent.Candidates[step.PropertyKey] = step.Candidates
			}
		}
	}
	
	return agent
}
