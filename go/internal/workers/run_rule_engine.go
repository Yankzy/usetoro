package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewRuleBootstrapWorker(
			deps.Store.Queries,
			deps.Logger,
			deps.Config,
			deps.RuleEngine,
		), nil
	})
}

type RuleBootstrapWorker struct {
	db         *database.Queries
	logger     *slog.Logger
	cfg        *config.Config
	ruleEngine *accounting.RuleEngineService
}

func NewRuleBootstrapWorker(db *database.Queries, logger *slog.Logger, cfg *config.Config, ruleEngine *accounting.RuleEngineService) *RuleBootstrapWorker {
	return &RuleBootstrapWorker{
		db:         db,
		logger:     logger,
		cfg:        cfg,
		ruleEngine: ruleEngine,
	}
}

func (w *RuleBootstrapWorker) Init(ctx context.Context) error {
	// Start daily background task to process orphaned transactions
	go w.runRuleEngineDaily(ctx)
	return nil
}

func (w *RuleBootstrapWorker) runRuleEngineDaily(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Initial run after short delay to allow system to stabilize
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

func (w *RuleBootstrapWorker) performCleanup(ctx context.Context) {
	w.logger.Info("📅 Running daily rule engine cleanup")
	realms, err := w.db.GetActiveRealms(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch active realms for cleanup", "error", err)
		return
	}

	for _, realmID := range realms {
		if err := w.ruleEngine.ProcessOrphanedTransactions(ctx, realmID); err != nil {
			w.logger.Error("Failed orphaned transaction cleanup", "realm_id", realmID, "error", err)
		}
	}
}

func (w *RuleBootstrapWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		// Fallback to deriving from an activity type if strictly subject isn't set
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

func (w *RuleBootstrapWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	// Guard against poison pills
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("poison pill exceeded retries", "subject", msg.Subject, "worker", "RuleBootstrapWorker")
		msg.Term()
		return nil
	}

	var payload struct {
		RealmID string `json:"realm_id"`
	}

	// 1. Manually unwrap TAP Envelope if present to avoid modifying core unmarshaler
	data := msg.Data
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	// 2. Use protocol-aware unmarshaler to handle internal wrappers (TaskDefinition/Proof/Input)
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil || payload.RealmID == "" {
		w.logger.Warn("could not extract realm_id from payload, ignoring", "subject", msg.Subject, "error", err)
		// Malformed non-retryable; return nil so manager Acks
		return nil
	}

	realmID := payload.RealmID
	w.logger.Info("starting rule engine bootstraps", "realm_id", realmID)

	// Instantiate the Bootstrapper leveraging the ruleEngine package
	bootstrapper := ruleEngine.NewBootstrapper(w.logger, w.db, w.ruleEngine)

	// Execute the full onboarding pipeline (Purchase + Deposit)
	// Returning errors on failure ensures the Manager NAKs and retries
	if err := bootstrapper.RunRuleEngineForPurchases(ctx, realmID); err != nil {
		w.logger.Error("failed rule engine bootstrap", "realm_id", realmID, "error", err)
		return err
	}

	w.logger.Info("successfully completed all rule engine bootstraps", "realm_id", realmID)

	return nil
}
