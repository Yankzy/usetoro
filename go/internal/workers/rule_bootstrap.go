package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/jackc/pgx/v5/pgtype"
)

func init() {
	// Register the RuleBootstrapWorker factory with the worker manager.
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewRuleBootstrapWorker(
			deps.Store.Queries,
			deps.Logger,
			deps.Config,
			deps.RuleEngine,
			deps.Queue,
		), nil
	})
}

// RuleBootstrapWorker is responsible for orchestrating the rule engine bootstrapping process.
// It handles both manual triggers (via NATS messages) and automated daily cleanup tasks
// to ensure ERP transactions are correctly categorized.
type RuleBootstrapWorker struct {
	db         *database.Queries
	logger     *slog.Logger
	cfg        *config.Config
	ruleEngine *accounting.RuleEngineService
	queue      *nats.Conn
}

// NewRuleBootstrapWorker creates a new instance of RuleBootstrapWorker with its dependencies.
func NewRuleBootstrapWorker(db *database.Queries, logger *slog.Logger, cfg *config.Config, ruleEngine *accounting.RuleEngineService, queue *nats.Conn) *RuleBootstrapWorker {
	return &RuleBootstrapWorker{
		db:         db,
		logger:     logger,
		cfg:        cfg,
		ruleEngine: ruleEngine,
		queue:      queue,
	}
}

// RuleBootstrapWorkerPayload defines the expected JSON payload for LLM tool invocation.
type RuleBootstrapWorkerPayload struct {
	RealmID   string `json:"realm_id" desc:"The ID of the realm (company) to run rule bootstrap for"`
	EntityID  string `json:"entity_id,omitempty" desc:"Optional ID of the entity"`
	SessionID string `json:"session_id,omitempty" desc:"Optional cleanup session ID context"`
}

// ToolName returns the unique LLM tool name for this worker.
func (w *RuleBootstrapWorker) ToolName() string {
	return "TriggerRuleBootstrap"
}

// ToolDescription provides the context for the LLM.
func (w *RuleBootstrapWorker) ToolDescription() string {
	return "Triggers the accounting rule engine bootstrapping process. This evaluates recent user classifications to automatically generate new deterministic rules."
}

// PayloadStruct returns a typed instance to automatically generate a JSON schema.
func (w *RuleBootstrapWorker) PayloadStruct() any {
	return RuleBootstrapWorkerPayload{}
}

// Init initializes the worker, starting the background daily cleanup process.
func (w *RuleBootstrapWorker) Init(ctx context.Context) error {
	// Start daily background task to process transactions that weren't caught by real-time rules.
	// ticker := time.NewTicker(24 * time.Hour)
	// defer ticker.Stop()

	// // Initial run after a short delay (30s) to avoid resource contention during startup.
	// select {
	// case <-ctx.Done():
	// 	return nil
	// case <-time.After(1 * time.Minute):
	// 	w.performCleanup(ctx)
	// }

	// for {
	// 	select {
	// 	case <-ctx.Done():
	// 		return nil
	// 	case <-ticker.C:
	// 		w.performCleanup(ctx)
	// 	}
	// }
	return nil
}

// performCleanup iterates through all active realms and triggers transaction categorization
// for any records that lack an assigned account (orphaned transactions).
// func (w *RuleBootstrapWorker) performCleanup(ctx context.Context) {
// 	w.logger.Info("📅 Running daily rule engine cleanup")
// 	realms, err := w.db.GetActiveRealms(ctx)
// 	if err != nil {
// 		w.logger.Error("Failed to fetch active realms for cleanup", "error", err)
// 		return
// 	}

// 	for _, realmID := range realms {
// 		// ProcessOrphanedTransactions applies current rules to transactions that haven't been categorized yet.
// 		if err := w.ruleEngine.ProcessOrphanedTransactions(ctx, realmID); err != nil {
// 			w.logger.Error("Failed orphaned transaction cleanup", "realm_id", realmID, "error", err)
// 		}
// 	}
// }

// Subscriptions returns the NATS subscription configuration for this worker.
// It listens on subjects defined in the worker configuration, typically related to rule bootstrapping.
func (w *RuleBootstrapWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		// Fallback subject if not explicitly configured.
		subject, _ = core.BuildWorkerInboxFromActivity("workers.rule_bootstrap")
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
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

// Handle processes incoming rule bootstrapping requests.
func (w *RuleBootstrapWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// 1. Poison Pill Protection
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject, "worker", "RuleBootstrapWorker")
		msg.Term()
		return nil
	}

	// 2. Simple, flat struct (AI-friendly) thanks to recursive UnmarshalTaskPayload
	var payload struct {
		RealmID   string `json:"realm_id"`
		EntityID  string `json:"entity_id"`
		SessionID string `json:"session_id"`
	}

	// 3. TAP Envelope Unwrapping
	data := msg.Data
	var env core.Envelope
	convID := ""
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		if env.Performative != core.REQUEST && env.Performative != core.ACCEPT_PROPOSAL {
			w.logger.Warn("rule bootstrap worker: dropping message, perf mismatch", "perf_val", env.Performative)
			msg.Term()
			return nil
		}
		data = env.Body
		convID = env.ConversationID
	}

	// 4. Payload Extraction (This recursively peels all Task, Proof, and Input wrappers!)
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		w.logger.Warn("could not extract payload, ignoring", "subject", msg.Subject, "error", err)
		return nil
	}

	// 5. Resolve the definitive Realm ID using the shared utility
	realmID, err := ResolveRealmID(ctx, w.db, payload.EntityID, payload.SessionID, payload.RealmID)
	if err != nil {
		w.logger.Warn("could not resolve realm_id, ignoring",
			"subject", msg.Subject,
			"error", err,
			"raw_payload", string(data))
		return nil
	}

	w.logger.Info("starting rule engine bootstraps", "realm_id", realmID)

	// Guard: Skip bootstrapping if there are no transactions to categorize.
	// Rules are idempotent, so skipping when there's nothing to evaluate saves compute.
	if payload.SessionID != "" {
		var sessionUUID pgtype.UUID
		if err := sessionUUID.Scan(payload.SessionID); err == nil {
			count, err := w.db.CountEnrichedTransactionsBySession(ctx, sessionUUID)
			if err == nil && count == 0 {
				w.logger.Info("no ENRICHED transactions in session, skipping bootstrap",
					"realm_id", realmID, "session_id", payload.SessionID)
				if convID != "" {
					if pubErr := w.publishCompletionProof(realmID, convID); pubErr != nil {
						w.logger.Error("failed to publish completion proof", "error", pubErr)
					}
				}
				return nil
			}
		}
	}

	// 6. Bootstrapper Initialization
	bootstrapper := ruleEngine.NewBootstrapper(w.logger, w.db, w.ruleEngine)

	// 7. Config Injection
	if currentCfg := config.GetGlobal(); currentCfg != nil {
		bootstrapper.WithConfig(currentCfg.RuleEngine.TargetRank, currentCfg.RuleEngine.MinUsageCount)
	}

	// 8. Execution — Run the advanced 1:1 feature-coverage pipeline
	// RunAdvancedBootstrapWithLegacy preserves backward compatibility by running
	// the legacy split-review + 1-to-1 consensus rules first, then layers on the
	// advanced analyzers (exact match, amounts, temporal, regex, allocations, etc.)
	if err := bootstrapper.RunAdvancedBootstrapWithLegacy(ctx, realmID); err != nil {
		w.logger.Error("failed rule engine bootstrap", "realm_id", realmID, "error", err)
		return err // NAK and retry
	}

	w.logger.Info("successfully completed all rule engine bootstraps", "realm_id", realmID)

	// 9. Completion Proof
	if convID != "" {
		if pubErr := w.publishCompletionProof(realmID, convID); pubErr != nil {
			w.logger.Error("failed to publish completion proof", "realm_id", realmID, "error", pubErr)
		}
	}

	return nil
}

// publishCompletionProof sends a 'Proof' message back to the orchestrator via NATS.
func (w *RuleBootstrapWorker) publishCompletionProof(realmID, cid string) error {
	if w.queue == nil {
		return nil
	}

	result := map[string]string{
		"realm_id": realmID,
		"status":   "rule_engine_bootstrap.completed",
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}

	// Create a Proof object which is a standard Toro acknowledgment of task completion.
	proof := core.Proof{
		TaskID:    realmID,
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      resultBytes,
	}

	// Wrap the proof in a TAP Envelope for routing.
	env, err := core.NewEnvelope(uuid.New().String(), "did:toro:rule-bootstrap-worker", workflows.OrchestratorDID, cid, core.INFORM, proof)
	if err != nil {
		return err
	}
	final, err := json.Marshal(env)
	if err != nil {
		return err
	}

	js, err := w.queue.JetStream()
	if err != nil {
		return err
	}
	// Publish to the orchestrator's inbox.
	_, err = js.Publish(workflows.OrchestratorInbox, final)
	return err
}

// ResolveRealmID checks if a realm_id is already provided. If not, it attempts to
// resolve it by querying the database using the sessionID or entityID.
func ResolveRealmID(ctx context.Context, db *database.Queries, entityID, sessionID, providedRealmID string) (string, error) {
	// 1. Return immediately if it was already provided explicitly
	if providedRealmID != "" {
		return providedRealmID, nil
	}

	// 2. Try to resolve via Session ID (most specific context)
	if sessionID != "" {
		var su pgtype.UUID
		if err := su.Scan(sessionID); err == nil {
			realmID, err := db.GetRealmIDFromSession(ctx, su)
			if err == nil && realmID.Valid && realmID.String != "" {
				return realmID.String, nil
			}
		}
	}

	// 3. Fallback to Entity ID (global context)
	if entityID != "" {
		var eu pgtype.UUID
		if err := eu.Scan(entityID); err == nil {
			tenantID, err := db.GetRealmIDFromEntity(ctx, eu)
			if err == nil && tenantID.Valid && tenantID.String != "" {
				return tenantID.String, nil
			}
		}
	}

	return "", fmt.Errorf("missing realm_id and unable to resolve from entity_id (%s) or session_id (%s)", entityID, sessionID)
}
