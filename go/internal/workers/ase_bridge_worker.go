package workers

import (
	"context"
	"encoding/json"
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
	dag    *ase.DAG
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		// Initialize the ASE global dependencies
		stateStore := ase.NewStateStore(deps.DBPool, deps.Redis)
		dag := ase.BuildDAGFromConfig(ase.GetConfig().DAG, deps.Logger)

		return &AseBridgeWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			db:     deps.Store.Queries,
			store:  stateStore,
			dag:    dag,
		}, nil
	})
}

func (w *AseBridgeWorker) Init(ctx context.Context) error {
	// Start real-time debug visualizer server
	w.startDebugServer()

	// Initialize the Classifier Service (to generate ThinkFuncs)
	classifierService := ase.NewClassifierService(nil, w.nc, "tasks.accounting.1.batch_categorization", w.logger)
	classifierService.SetDB(w.db) // Pass db for dynamic provider

	// Wire up dynamic ThinkFuncs for every node in the DAG
	w.logger.Info("ase_orchestrator: wiring dynamic DAG nodes")
	for _, node := range w.dag.Nodes {
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

	// Start all the DAG background flush loops
	w.dag.StartAll()

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
	}
}

func (w *AseBridgeWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("📡 [DEBUG] ase_orchestrator received TAP message", "topic", msg.Subject)

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
		// Use empty string fallback for invalid pgtype strings
		desc := ""
		if txn.RawDescription.Valid {
			desc = txn.RawDescription.String
		}

		direction := "OUTFLOW"
		if txn.CashDirection.Valid && txn.CashDirection.String != "" {
			direction = txn.CashDirection.String
		} else {
			// Compute direction manually if missing
			amtStr := strings.ReplaceAll(txn.RawAmount, ",", "")
			amtStr = strings.ReplaceAll(amtStr, "$", "")
			amtStr = strings.TrimSpace(amtStr)
			if amt, err := strconv.ParseFloat(amtStr, 64); err == nil {
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

		agent := ase.NewASENode(
			realmID,
			desc,
			direction,
			txn.RawAmount,
		)

		// Pass the exact transaction ID to the agent so it can update the correct row in StateStore
		agent.NodeID = uuid.UUID(txn.ID.Bytes).String()

		wg.Add(1)
		// Fire off the asynchronous Goroutine
		go func(a *ase.AutonomousSemanticEngineNode) {
			defer wg.Done()
			// Provide a background context since the worker Handle context might cancel
			bgCtx := context.Background()
			if err := a.Run(bgCtx, w.dag, w.store); err != nil {
				w.logger.Error("ase_bridge: agent crashed", "node_id", a.NodeID, "error", err)
			}
		}(agent)
	}

	// 5. Reply to Orchestrator in the background to unblock the workflow
	// Wait for all agents to finish classification or yield to HOLD state.
	go func() {
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
	}()

	return nil
}
