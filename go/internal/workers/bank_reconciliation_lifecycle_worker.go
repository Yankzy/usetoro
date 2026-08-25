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
	reconciliationBaselineActivity = "workers.accounting.bank_reconciliation.baseline"
	reconciliationPrepareActivity  = "workers.accounting.bank_reconciliation.prepare"
	reconciliationReviseActivity   = "workers.accounting.bank_reconciliation.revise"
	reconciliationCloseActivity    = "workers.accounting.bank_reconciliation.close"
	reconciliationCorrectActivity  = "workers.accounting.bank_reconciliation.correct"
)

type BankReconciliationLifecycleWorker struct {
	service *accountingservice.BankReconciliationService
	logger  *slog.Logger
	nc      *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.DBPool == nil {
			return nil, nil
		}
		return &BankReconciliationLifecycleWorker{service: accountingservice.NewBankReconciliationService(deps.DBPool), logger: deps.Logger.With("worker", "bank_reconciliation_lifecycle"), nc: deps.Queue}, nil
	})
}

func (w *BankReconciliationLifecycleWorker) Init(context.Context) error { return nil }
func (w *BankReconciliationLifecycleWorker) Stop()                      {}
func (w *BankReconciliationLifecycleWorker) Subscriptions() []SubscriptionConfig {
	activities := []string{reconciliationBaselineActivity, reconciliationPrepareActivity, reconciliationReviseActivity, reconciliationCloseActivity, reconciliationCorrectActivity}
	result := make([]SubscriptionConfig, 0, len(activities))
	for _, activity := range activities {
		subject, err := core.BuildWorkerInboxFromActivity(activity)
		if err != nil {
			panic(err)
		}
		result = append(result, SubscriptionConfig{Subject: subject, Group: "worker-inbox-bank-reconciliation-lifecycle", Options: []nats.SubOpt{nats.DeliverAll(), nats.AckExplicit()}})
	}
	return result
}

func (w *BankReconciliationLifecycleWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil || env.Performative != core.REQUEST {
		return fmt.Errorf("reconciliation lifecycle requires a workflow request envelope")
	}
	activity := reconciliationActivityForSubject(msg.Subject)
	var result accountingservice.ReconciliationStateResult
	var err error
	switch activity {
	case reconciliationBaselineActivity:
		var command accountingservice.CreateMigratedBaselineCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.CreateMigratedBaseline(ctx, command)
		}
	case reconciliationPrepareActivity:
		var command accountingservice.PrepareReconciliationPeriodCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.PreparePeriod(ctx, command)
		}
	case reconciliationReviseActivity:
		var command accountingservice.ReviseOpenReconciliationCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.ReviseOpenState(ctx, command)
		}
	case reconciliationCloseActivity:
		var command accountingservice.CloseReconciliationPeriodCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.ClosePeriod(ctx, command)
		}
	case reconciliationCorrectActivity:
		var command accountingservice.CorrectClosedReconciliationCommand
		if err = core.UnmarshalTaskPayload(env.Body, &command); err == nil {
			result, err = w.service.CorrectClosedPeriod(ctx, command)
		}
	default:
		err = fmt.Errorf("unsupported reconciliation lifecycle subject %q", msg.Subject)
	}
	if err != nil {
		return err
	}
	return publishWorkflowResult(w.nc, msg.Reply, env, "bank_reconciliation_lifecycle", result)
}

func reconciliationActivityForSubject(subject string) string {
	for _, activity := range []string{reconciliationBaselineActivity, reconciliationPrepareActivity, reconciliationReviseActivity, reconciliationCloseActivity, reconciliationCorrectActivity} {
		candidate, _ := core.BuildWorkerInboxFromActivity(activity)
		if subject == candidate {
			return activity
		}
	}
	return ""
}
