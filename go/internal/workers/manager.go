package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
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
				defer func() {
					if r := recover(); r != nil {
						m.logger.Error("worker panic recovered", "panic", r, "subject", msg.Subject, "stack", string(debug.Stack()))
						msg.Nak()
					}
				}()

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

// Dependencies contains the standard dependencies injected into workers.
type Dependencies struct {
	Logger          *slog.Logger
	Config          *config.Config
	Queue           *nats.Conn
	Store           *store.Store
	DBPool          *pgxpool.Pool
	Pinecone        *vector.PineconeClient
	Embedder        *vector.Embedder
	EntityResolver  *ai.EntityResolver
	CoAMapper       *ai.CoAMapper
	AttachService   *accounting.AttachableService
	ProviderFactory erp.ProviderFactory
	RuleEngine      *accounting.RuleEngineService
	LLMClient       *ai.LLMClient
	FetchEntityFn   func(ctx context.Context, tenantID, realmID, entityType, entityID, op string) error
}

type WorkerFactory func(deps Dependencies) (Worker, error)

var registry []WorkerFactory

// RegisterFactory registers a factory function inside the worker registry.
func RegisterFactory(factory WorkerFactory) {
	registry = append(registry, factory)
}

// LoadFromRegistry invokes all registered factories and adds them to the manager.
// Skip initialization if a factory returns (nil, nil) (e.g. for conditional dependencies).
func (m *Manager) LoadFromRegistry(deps Dependencies) error {
	for _, factory := range registry {
		w, err := factory(deps)
		if err != nil {
			return err
		}
		if w != nil {
			m.Register(w)
		}
	}
	return nil
}

// ExtractRows is a helper to extract a slice of maps from a json.RawMessage,
// supporting both JSON Arrays and JSON Objects (where values are extracted).
func ExtractRows(data []byte) ([]map[string]interface{}, error) {
	// 1. Try as Array
	var slice []map[string]interface{}
	if err := json.Unmarshal(data, &slice); err == nil {
		return slice, nil
	}

	// 2. Try as Map
	var m map[string]map[string]interface{}
	if err := json.Unmarshal(data, &m); err == nil {
		rows := make([]map[string]interface{}, 0, len(m))
		for _, v := range m {
			rows = append(rows, v)
		}
		return rows, nil
	}

	return nil, fmt.Errorf("data is neither a JSON array nor a JSON object")
}
