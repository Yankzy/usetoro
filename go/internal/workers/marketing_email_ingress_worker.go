package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

// MarketingEmailIngressWorker receives inbound marketing payloads from the ingress
// and translates them into a FIPA REQUEST envelope to trigger the marketing workflow.
type MarketingEmailIngressWorker struct {
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &MarketingEmailIngressWorker{
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *MarketingEmailIngressWorker) Init(ctx context.Context) error { return nil }

func (w *MarketingEmailIngressWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("MarketingEmailIngressWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("MarketingEmailIngressWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{
			nats.Durable(durableFromSubject(subject)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		},
	}}
}
func (w *MarketingEmailIngressWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.logger.Info("MarketingEmailIngressWorker: received payload", "subject", msg.Subject)

	var payload map[string]interface{}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("MarketingEmailIngressWorker: failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}
	w.logger.Info("MarketingEmailIngressWorker: payload", "payload", payload)

	// 1. Wrap the raw payload in a TaskDefinition first
	taskDef := core.TaskDefinition{
		Payload: msg.Data, // msg.Data contains {"entity_id": "..."}
	}

	taskDefBytes, err := json.Marshal(taskDef)
	if err != nil {
		w.logger.Error("MarketingEmailIngressWorker: failed to marshal task definition", "error", err)
		msg.Term()
		return nil
	}

	// 2. Put the TaskDefinition inside the Envelope Body
	env := core.Envelope{
		Performative: core.REQUEST,
		Body:         taskDefBytes,
	}

	envBytes, err := json.Marshal(env)
	if err != nil {
		w.logger.Error("MarketingEmailIngressWorker: failed to marshal envelope", "error", err)
		msg.Term()
		return nil
	}

	// The ase_marketing.yml workflow listens on this topic
	targetSubject := "events.marketing.1.trigger"
	if err := w.nc.Publish(targetSubject, envBytes); err != nil {
		w.logger.Error("MarketingEmailIngressWorker: failed to publish to ase_marketing workflow", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("MarketingEmailIngressWorker: triggered ase_marketing workflow", "targetSubject", targetSubject)
	msg.Ack()
	return nil
}
