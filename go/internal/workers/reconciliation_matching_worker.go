package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	accountingservice "github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
)

const (
	reconciliationGenerateMatchesActivity = "workers.accounting.bank_reconciliation.generate_matches"
	reconciliationManualMatchActivity     = "workers.accounting.bank_reconciliation.manual_match"
	reconciliationConfirmMatchActivity    = "workers.accounting.bank_reconciliation.confirm_match"
	reconciliationResolveReviewActivity   = "workers.accounting.bank_reconciliation.resolve_review"
)

type ReconciliationMatchingWorker struct {
	service *accountingservice.ReconciliationMatchingService
	logger  *slog.Logger
	nc      *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.DBPool == nil {
			return nil, nil
		}
		return &ReconciliationMatchingWorker{service: accountingservice.NewReconciliationMatchingService(deps.DBPool), logger: deps.Logger.With("worker", "reconciliation_matching"), nc: deps.Queue}, nil
	})
}

func (w *ReconciliationMatchingWorker) Init(context.Context) error { return nil }
func (w *ReconciliationMatchingWorker) Stop()                      {}
func (w *ReconciliationMatchingWorker) Subscriptions() []SubscriptionConfig {
	result := make([]SubscriptionConfig, 0, 4)
	for _, activity := range []string{reconciliationGenerateMatchesActivity, reconciliationManualMatchActivity, reconciliationConfirmMatchActivity, reconciliationResolveReviewActivity} {
		subject, err := core.BuildWorkerInboxFromActivity(activity)
		if err != nil {
			panic(err)
		}
		result = append(result, SubscriptionConfig{Subject: subject, Group: "worker-inbox-reconciliation-matching", Options: []nats.SubOpt{nats.DeliverAll(), nats.AckExplicit()}})
	}
	return result
}

func (w *ReconciliationMatchingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil || env.Performative != core.REQUEST {
		return fmt.Errorf("reconciliation matching requires a workflow request envelope")
	}
	activity := matchingActivityForSubject(msg.Subject)
	var result interface{}
	var err error
	switch activity {
	case reconciliationGenerateMatchesActivity:
		var command accountingservice.GenerateOneToOneCandidatesCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.GenerateOneToOneCandidates(ctx, command)
		}
	case reconciliationManualMatchActivity:
		var command accountingservice.CreateManualGroupedMatchCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.CreateManualGroupedMatch(ctx, command)
		}
	case reconciliationConfirmMatchActivity:
		var command accountingservice.ConfirmMatchCandidateCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.ConfirmCandidate(ctx, command)
		}
	case reconciliationResolveReviewActivity:
		var command accountingservice.ResolveReconciliationReviewCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.ResolveReviewItem(ctx, command)
		}
	default:
		err = fmt.Errorf("unsupported matching subject %q", msg.Subject)
	}
	if err != nil {
		return err
	}
	return publishWorkflowResult(w.nc, msg.Reply, env, "reconciliation_matching", result)
}

func matchingActivityForSubject(subject string) string {
	for _, activity := range []string{reconciliationGenerateMatchesActivity, reconciliationManualMatchActivity, reconciliationConfirmMatchActivity, reconciliationResolveReviewActivity} {
		candidate, _ := core.BuildWorkerInboxFromActivity(activity)
		if subject == candidate {
			return activity
		}
	}
	return ""
}
