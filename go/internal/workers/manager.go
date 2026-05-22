package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
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
	QBOConnector    *connectors.QBOConnector
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
// It recursively searches through known wrapper fields like "data", "input", and "dependencies".
func ExtractRows(data []byte) ([]map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	// 1. Try as direct JSON Array (standard rows format)
	var slice []map[string]interface{}
	if err := json.Unmarshal(data, &slice); err == nil && len(slice) > 0 {
		return slice, nil
	}

	// 2. Try as a standardized Object format
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err == nil {
		// Priority 1: Check for 'mapped_rows', 'body', 'data', or 'rows' (the standard formats)
		for _, key := range []string{"mapped_rows", "body", "data", "rows", "payload", "input"} {
			if nextData, ok := generic[key]; ok && len(nextData) > 0 {
				if rows, err := ExtractRows(nextData); err == nil && len(rows) > 0 {
					return rows, nil
				}
			}
		}

		// Priority 2: Check inside 'dependencies' (handled by Orchestrator merge)
		if depsRaw, ok := generic["dependencies"]; ok && len(depsRaw) > 0 {
			var deps map[string]json.RawMessage
			if err := json.Unmarshal(depsRaw, &deps); err == nil {
				for _, depData := range deps {
					if rows, err := ExtractRows(depData); err == nil && len(rows) > 0 {
						return rows, nil
					}
				}
			}
		}

		// Priority 3: Check if the top-level itself is a map of rows (e.g. { "row_1": {...} })
		// Heuristic: Must NOT be a FIPA control object (no id, perf, src keys)
		if _, hasPerf := generic["perf"]; !hasPerf {
			var rowMap map[string]map[string]interface{}
			if err := json.Unmarshal(data, &rowMap); err == nil && len(rowMap) > 0 {
				rows := make([]map[string]interface{}, 0, len(rowMap))
				for _, v := range rowMap {
					if len(v) > 0 {
						rows = append(rows, v)
					}
				}
				if len(rows) > 0 {
					return rows, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("data does not match standard row formats (array or mapped_rows)")
}

