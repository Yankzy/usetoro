package workers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// SubscriptionConfig defines a single JetStream subscription requirement
type SubscriptionConfig struct {
	Subject string
	Group   string
	Options []nats.SubOpt
}

// Worker defines the interface for all background workers
type Worker interface {
	Init(ctx context.Context) error
	Subscriptions() []SubscriptionConfig
	Handle(ctx context.Context, msg *nats.Msg) error
}

// Manager orchestrates the lifecycle of multiple background workers
type Manager struct {
	logger        *slog.Logger
	workers       []Worker
	nc            *nats.Conn
	subscriptions []*nats.Subscription
}

// NewManager creates a new worker manager
func NewManager(logger *slog.Logger, nc *nats.Conn) *Manager {
	return &Manager{
		logger: logger,
		nc:     nc,
	}
}

// Register adds a worker to the manager
func (m *Manager) Register(w Worker) {
	m.workers = append(m.workers, w)
}

// StartAll subscribes all registered workers and blocks until ctx finishes.
func (m *Manager) StartAll(ctx context.Context) error {
	m.logger.Info("Starting all background workers centrally", "count", len(m.workers))

	if len(m.workers) == 0 {
		<-ctx.Done()
		return nil
	}

	js, err := m.nc.JetStream()
	if err != nil {
		return fmt.Errorf("dispatcher failed to bind jetstream context: %w", err)
	}

	for _, w := range m.workers {
		worker := w

		if err := worker.Init(ctx); err != nil {
			return fmt.Errorf("failed to init worker %T: %w", worker, err)
		}

		for _, subCfg := range worker.Subscriptions() {
			sub, err := js.QueueSubscribe(subCfg.Subject, subCfg.Group, func(msg *nats.Msg) {
				if err := worker.Handle(ctx, msg); err != nil {
					m.logger.Error("worker handle error", "subject", msg.Subject, "error", err)
					msg.Nak()
					return
				}
				msg.Ack()
			}, subCfg.Options...)

			if err != nil {
				return fmt.Errorf("failed to subscribe worker %T to %s: %w", worker, subCfg.Subject, err)
			}

			m.subscriptions = append(m.subscriptions, sub)
			m.logger.Info("Worker subscribed successfully", "type", fmt.Sprintf("%T", worker), "subject", subCfg.Subject, "group", subCfg.Group)
		}
	}

	<-ctx.Done()

	m.logger.Info("Stopping all background workers logically")
	for _, sub := range m.subscriptions {
		_ = sub.Unsubscribe()
	}

	return nil
}
