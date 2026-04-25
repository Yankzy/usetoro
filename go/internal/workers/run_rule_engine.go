// Package workers provides background workers for various Toro tasks.
package workers

import (
	"context"
	"encoding/json"
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

// Init initializes the worker, starting the background daily cleanup process.
func (w *RuleBootstrapWorker) Init(ctx context.Context) error {
	// Start daily background task to process transactions that weren't caught by real-time rules.
	go w.runRuleEngineDaily(ctx)
	return nil
}

// runRuleEngineDaily manages the 24-hour cycle for transaction cleanup.
func (w *RuleBootstrapWorker) runRuleEngineDaily(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Initial run after a short delay (30s) to avoid resource contention during startup.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
		w.performCleanup(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.performCleanup(ctx)
		}
	}
}

// performCleanup iterates through all active realms and triggers transaction categorization
// for any records that lack an assigned account (orphaned transactions).
func (w *RuleBootstrapWorker) performCleanup(ctx context.Context) {
	w.logger.Info("📅 Running daily rule engine cleanup")
	realms, err := w.db.GetActiveRealms(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch active realms for cleanup", "error", err)
		return
	}

	for _, realmID := range realms {
		// ProcessOrphanedTransactions applies current rules to transactions that haven't been categorized yet.
		if err := w.ruleEngine.ProcessOrphanedTransactions(ctx, realmID); err != nil {
			w.logger.Error("Failed orphaned transaction cleanup", "realm_id", realmID, "error", err)
		}
	}
}

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
// It extracts the realm_id from the payload and triggers the bootstrapping logic
// which analyzes historical data to generate categorization rules.
func (w *RuleBootstrapWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// 1. Poison Pill Protection: Avoid infinite retries if a message repeatedly fails.
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject, "worker", "RuleBootstrapWorker")
		msg.Term() // Terminate the message so it's not redelivered.
		return nil
	}

	var payload struct {
		RealmID string `json:"realm_id"`
	}

	// 2. TAP Envelope Unwrapping: Messages in Toro are often wrapped in a TAP Envelope
	// which contains metadata like ConversationID.
	data := msg.Data
	var env core.Envelope
	convID := ""
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
		convID = env.ConversationID
	}

	// 3. Payload Extraction: Use protocol-aware unmarshaler to handle Task/Input wrappers.
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil || payload.RealmID == "" {
		w.logger.Warn("could not extract realm_id from payload, ignoring", "subject", msg.Subject, "error", err)
		// Return nil to Ack the message, as retrying won't fix a malformed payload.
		return nil
	}

	realmID := payload.RealmID
	w.logger.Info("starting rule engine bootstraps", "realm_id", realmID)

	// 4. Bootstrapper Initialization: The bootstrapper performs the heavy lifting of
	// analyzing historical transactions and creating rules based on consensus.
	bootstrapper := ruleEngine.NewBootstrapper(w.logger, w.db, w.ruleEngine)

	// 5. Config Injection: Apply latest rank and usage thresholds from hot-reloaded config.
	if currentCfg := config.GetGlobal(); currentCfg != nil {
		bootstrapper.WithConfig(currentCfg.RuleEngine.TargetRank, currentCfg.RuleEngine.MinUsageCount)
	}

	// 6. Execution: Run the analysis for Purchases.
	// Note: Deposit onboarding should be handled here as well if required by the workflow.
	if err := bootstrapper.RunRuleEngineForPurchases(ctx, realmID); err != nil {
		w.logger.Error("failed rule engine bootstrap", "realm_id", realmID, "error", err)
		return err // Return error to trigger NATS NAK and retry.
	}

	w.logger.Info("successfully completed all rule engine bootstraps", "realm_id", realmID)

	// 7. Completion Proof: Notify the orchestrator that the task is finished.
	if convID != "" {
		if pubErr := w.publishCompletionProof(realmID, convID); pubErr != nil {
			w.logger.Error("failed to publish completion proof", "realm_id", realmID, "error", pubErr)
		}
	}

	return nil
}

// publishCompletionProof sends a 'Proof' message back to the orchestrator via NATS.
// This allows the workflow to proceed to the next step.
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
