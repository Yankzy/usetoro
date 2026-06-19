package workers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/tap/pkg/core"
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

// ToolExposer allows a worker to expose itself as an LLM tool dynamically.
type ToolExposer interface {
	ToolName() string
	ToolDescription() string
	PayloadStruct() any
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

const workerDeliverLimit = 5

func (m *Manager) emitWorkerDLQ(msg *nats.Msg, reason string, md *nats.MsgMetadata) {
	dlqSubject := "worker.dlq"

	payload := map[string]interface{}{
		"subject":   msg.Subject,
		"reason":    reason,
		"data":      base64.StdEncoding.EncodeToString(msg.Data),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if md != nil {
		payload["metadata"] = map[string]interface{}{
			"stream":            md.Stream,
			"consumer":          md.Consumer,
			"stream_sequence":   md.Sequence.Stream,
			"consumer_sequence": md.Sequence.Consumer,
			"num_delivered":     md.NumDelivered,
			"timestamp":         md.Timestamp,
		}
	}
	encoded, _ := json.Marshal(payload)
	if err := m.nc.Publish(dlqSubject, encoded); err != nil {
		m.logger.Error("failed to publish worker DLQ", "error", err)
	}
	if err := msg.Term(); err != nil {
		m.logger.Error("failed to terminate worker msg after DLQ", "error", err)
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
			opts := append([]nats.SubOpt{nats.MaxDeliver(workerDeliverLimit)}, subCfg.Options...)

			subscribeFunc := func() (*nats.Subscription, error) {
				return js.QueueSubscribe(subCfg.Subject, subCfg.Group, func(msg *nats.Msg) {
					defer func() {
						if r := recover(); r != nil {
							m.logger.Error("worker panic recovered", "panic", r, "subject", msg.Subject, "stack", string(debug.Stack()))
							msg.Nak()
						}
					}()

					md, _ := msg.Metadata()
					if md != nil && md.NumDelivered >= workerDeliverLimit {
						m.emitWorkerDLQ(msg, "exceeded max deliveries", md)
						return
					}

					if err := worker.Handle(ctx, msg); err != nil {
						m.logger.Error("worker handle error", "subject", msg.Subject, "error", err)
						msg.Nak()
						return
					}
					msg.Ack()
				}, opts...)
			}

			sub, err := subscribeFunc()
			if err != nil {
				errMsg := err.Error()
				isMismatch := strings.Contains(errMsg, "consumer already exists") ||
					strings.Contains(errMsg, "name already in use") ||
					strings.Contains(errMsg, "subject does not match") ||
					strings.Contains(errMsg, "configuration requests")

				if isMismatch {
					durable := durableFromSubject(subCfg.Subject)
					durables := []string{durable}
					if strings.Contains(subCfg.Subject, "ase_bridge") {
						durables = append(durables, "ase-orchestrator-worker")
					}
					if subCfg.Subject == "ase.events.resume" {
						durables = append(durables, "ase-orchestrator-resume")
					}
					if strings.Contains(subCfg.Subject, "ase_resolution") {
						durables = append(durables, "ase-resolution")
					}
					if strings.Contains(subCfg.Subject, "vcoo") {
						durables = append(durables, "vcoo-worker")
					}
					if strings.Contains(subCfg.Subject, "omni_chat") || strings.Contains(subCfg.Subject, "outgoing.chat") {
						durables = append(durables, "omni-chat-worker")
					}
					if strings.Contains(subCfg.Subject, "telemetry") {
						durables = append(durables, "ase-telemetry-worker")
					}
					if strings.Contains(subCfg.Subject, "qbo_fetch") {
						durables = append(durables, "qbo-fetch")
					}

					streamName, sErr := findStreamForSubject(js, subCfg.Subject)
					if sErr == nil {
						for _, d := range durables {
							m.logger.Warn("Consumer configuration mismatch detected, deleting consumer to recreate...", "durable", d, "stream", streamName, "error", err)
							if delErr := js.DeleteConsumer(streamName, d); delErr != nil {
								m.logger.Debug("failed to delete consumer during recovery", "durable", d, "stream", streamName, "error", delErr)
							}
						}
						sub, err = subscribeFunc()
					} else {
						m.logger.Error("failed to resolve stream for subject during mismatch recovery", "subject", subCfg.Subject, "error", sErr)
					}
				}

				if err != nil {
					return fmt.Errorf("failed to subscribe worker %T to %s: %w", worker, subCfg.Subject, err)
				}
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
	Redis           *redis.Client
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
		// AND must NOT be a wrapper object (no input, payload, data, config, dependencies)
		if _, hasPerf := generic["perf"]; !hasPerf {
			isWrapper := false
			for _, k := range []string{"input", "payload", "data", "config", "dependencies", "mapped_rows"} {
				if _, has := generic[k]; has {
					isWrapper = true
					break
				}
			}
			if !isWrapper {
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
	}

	return nil, fmt.Errorf("data does not match standard row formats (array or mapped_rows)")
}

// InferToolConfigs iterates through all loaded workers, checks if they implement ToolExposer,
// and dynamically infers their JSON schemas. It returns these as ToolConfigs for the LLM agents.
func (m *Manager) InferToolConfigs(cfg *config.Config) []core.ToolConfig {
	var tools []core.ToolConfig
	for _, w := range m.workers {
		_, workerCfg := cfg.Workers.GetForWorker(w)
		if workerCfg.ActivityType == "" {
			continue
		}
		if te, ok := w.(ToolExposer); ok {
			tc := core.ToolConfig{
				Name:         te.ToolName(),
				Description:  te.ToolDescription(),
				ActivityType: workerCfg.ActivityType,
				InputSchema:  inferSchema(te.PayloadStruct()),
			}
			tools = append(tools, tc)
		}
	}
	return tools
}

func inferSchema(v any) string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	props := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := field.Name
		parts := strings.Split(jsonTag, ",")
		if len(parts) > 0 && parts[0] != "" {
			name = parts[0]
		}

		isRequired := true
		for _, p := range parts[1:] {
			if p == "omitempty" {
				isRequired = false
			}
		}

		fieldType := "string"
		switch field.Type.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			fieldType = "number"
		case reflect.Bool:
			fieldType = "boolean"
		case reflect.Slice, reflect.Array:
			fieldType = "array"
		case reflect.Map, reflect.Struct:
			fieldType = "object"
		}

		desc := field.Tag.Get("desc")
		if desc == "" {
			desc = field.Tag.Get("description")
		}

		propMap := map[string]string{"type": fieldType}
		if desc != "" {
			propMap["description"] = desc
		}

		props[name] = propMap
		if isRequired {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	b, _ := json.MarshalIndent(schema, "", "  ")
	return string(b)
}

func findStreamForSubject(js nats.JetStreamContext, subject string) (string, error) {
	namesChan := js.StreamNames()
	var streams []string
	for name := range namesChan {
		streams = append(streams, name)
	}

	for _, streamName := range streams {
		info, err := js.StreamInfo(streamName)
		if err != nil {
			continue
		}
		for _, s := range info.Config.Subjects {
			if subjectIsCovered(subject, s) {
				return streamName, nil
			}
		}
	}
	return "", fmt.Errorf("no stream found covering subject %q", subject)
}

func subjectIsCovered(req, existing string) bool {
	if req == existing {
		return true
	}

	reqTokens := strings.Split(req, ".")
	exTokens := strings.Split(existing, ".")

	for i, exToken := range exTokens {
		if exToken == ">" {
			return true
		}

		if i >= len(reqTokens) {
			return false
		}

		if exToken != "*" && exToken != reqTokens[i] {
			return false
		}
	}

	return len(reqTokens) == len(exTokens)
}
