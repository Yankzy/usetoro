package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/workflows"
)

// WorkflowWorker handles workflow blueprint management over NATS.
//
// It subscribes to two well-known subjects:
//
//	worker.inbox.workflow-worker.upsert  – write a blueprint to the DB then signal the orchestrator,
//	frontend must include the full subject in the request URL like /ingress?subject=worker.inbox.workflow-worker.upsert
//	worker.inbox.workflow-worker.sync    – signal the orchestrator to re-sync all blueprints
//
// The frontend (or any producer) reaches these subjects via POST /ingress?subject=<subject>.
// Neither subject is a workflow step; they are admin/operational messages that do not
// participate in the DAG.  The manager's JetStream consumer provides delivery guarantees and
// queue-group horizontal safety so only one worker replica processes each message.
type WorkflowWorker struct {
	db     database.Querier
	nc     *nats.Conn
	logger *slog.Logger
}

// UpsertWorkflowPayload is the JSON body expected on the upsert subject.
// It mirrors the WorkflowDef top-level fields so the sender does not need to know
// the internal DB schema — just the canonical YAML field names.
type UpsertWorkflowPayload struct {
	Name         string          `json:"name"`
	TriggerTopic string          `json:"trigger_topic"`
	Definition   json.RawMessage `json:"definition"`
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.DBPool == nil {
			return nil, nil // skip if no DB available
		}
		return &WorkflowWorker{
			db:     database.New(deps.DBPool),
			nc:     deps.Queue,
			logger: deps.Logger,
		}, nil
	})
}

func (w *WorkflowWorker) Init(_ context.Context) error { return nil }

func (w *WorkflowWorker) Subscriptions() []SubscriptionConfig {
	upsertSubject := "worker.inbox.workflow-worker.upsert"
	syncSubject := "worker.inbox.workflow-worker.sync"

	return []SubscriptionConfig{
		{
			Subject: upsertSubject,
			Group:   groupFromSubject(upsertSubject),
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(upsertSubject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
		{
			Subject: syncSubject,
			Group:   groupFromSubject(syncSubject),
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(syncSubject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *WorkflowWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	switch msg.Subject {
	case "worker.inbox.workflow-worker.upsert":
		return w.handleUpsert(ctx, msg)
	case "worker.inbox.workflow-worker.sync":
		return w.handleSync(msg)
	default:
		w.logger.Warn("WorkflowWorker: unknown subject", "subject", msg.Subject)
		msg.Term()
		return nil
	}
}

// handleUpsert writes or updates a workflow blueprint in the DB, then fires
// workflow.admin.sync so the running Orchestrator picks it up immediately.
func (w *WorkflowWorker) handleUpsert(ctx context.Context, msg *nats.Msg) error {
	var payload UpsertWorkflowPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("WorkflowWorker.upsert: invalid JSON payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.Name == "" || payload.TriggerTopic == "" {
		w.logger.Error("WorkflowWorker.upsert: name and trigger_topic are required")
		msg.Term()
		return nil
	}

	_, err := w.db.UpsertWorkflowBlueprint(ctx, database.UpsertWorkflowBlueprintParams{
		Name:         payload.Name,
		TriggerTopic: payload.TriggerTopic,
		Definition:   payload.Definition,
	})
	if err != nil {
		w.logger.Error("WorkflowWorker.upsert: DB upsert failed", "name", payload.Name, "error", err)
		return err // Nak → redelivered
	}

	w.logger.Info("WorkflowWorker.upsert: blueprint saved", "name", payload.Name)

	// Signal the Orchestrator to re-sync. Fire-and-forget: blueprint is already durable
	// in Postgres even if the Orchestrator is temporarily unavailable.
	if err := w.nc.Publish(workflows.WorkflowAdminSyncSubject, []byte(payload.Name)); err != nil {
		w.logger.Warn("WorkflowWorker.upsert: failed to publish admin sync signal", "error", err)
	}

	msg.Ack()
	return nil
}

// handleSync publishes workflow.admin.sync without touching the DB.
// Use this when blueprints are written to the DB by an external tool and you
// just need the live Orchestrator to reload.
func (w *WorkflowWorker) handleSync(msg *nats.Msg) error {
	w.logger.Info("WorkflowWorker.sync: publishing admin sync signal")

	if err := w.nc.Publish(workflows.WorkflowAdminSyncSubject, []byte("")); err != nil {
		w.logger.Error("WorkflowWorker.sync: failed to publish admin sync signal", "error", err)
		return err // Nak → redelivered
	}

	msg.Ack()
	return nil
}
