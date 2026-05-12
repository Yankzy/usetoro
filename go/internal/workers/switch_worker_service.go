package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/tidwall/gjson"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

const switchWorkerDID = "did:toro:switch-worker"

type SwitchWorkerService struct {
	nc     *nats.Conn
	logger *slog.Logger
	cfg    *config.Config
}

// NewSwitchWorkerService wires the switch worker dispatcher.
func NewSwitchWorkerService(nc *nats.Conn, logger *slog.Logger, cfg *config.Config) (*SwitchWorkerService, error) {
	if nc == nil {
		return nil, fmt.Errorf("switch worker requires nats connection")
	}
	if cfg == nil {
		return nil, fmt.Errorf("switch worker requires config")
	}
	return &SwitchWorkerService{
		nc:     nc,
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (s *SwitchWorkerService) Init(context.Context) error {
	return nil
}

func (s *SwitchWorkerService) Subscriptions() []SubscriptionConfig {
	_, workerCfg := s.cfg.Workers.GetForWorker(s)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		s.logger.Error("switch worker: missing activity_type configuration")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		derived, err := core.BuildWorkerInboxFromActivity(activityType)
		if err != nil {
			s.logger.Error("switch worker: failed to derive subject", "error", err)
			return nil
		}
		subject = derived
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

func (s *SwitchWorkerService) Handle(ctx context.Context, msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		s.logger.Error("switch worker: failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.REQUEST {
		return nil
	}

	var task core.TaskDefinition
	if err := json.Unmarshal(env.Body, &task); err != nil {
		return fmt.Errorf("switch worker: failed to parse task payload: %w", err)
	}

	var swPayload struct {
		Config SwitchConfig    `json:"config"`
		Input  json.RawMessage `json:"input"`
	}

	// 1. Resolve raw data from envelope (handle FIPA Proof wrapper)
	rawData := task.Payload
	var proof core.Proof
	if err := json.Unmarshal(task.Payload, &proof); err == nil && len(proof.Data) > 0 && proof.Type != "" {
		rawData = proof.Data
	}

	// 2. Unmarshal into our payload struct. 
	// This captures { "config": ..., "input": ... } if present.
	if err := json.Unmarshal(rawData, &swPayload); err != nil {
		return fmt.Errorf("switch worker: malformed payload: %w", err)
	}

	// 3. If swPayload.Input is still empty, it might be a raw payload (no orchestrator wrapper)
	if len(swPayload.Input) == 0 {
		swPayload.Input = rawData
	}

	// Preserve whether original input was an array
	isOriginalArray := gjson.ParseBytes(swPayload.Input).IsArray()

	// Switch requires an array for internal iteration logic
	input := swPayload.Input
	if !isOriginalArray {
		input = append(append([]byte("["), input...), ']')
	}

	output, err := Switch(input, swPayload.Config)
	if err != nil {
		return fmt.Errorf("switch worker: evaluation failed: %w", err)
	}


	// If it was a single object, unwrap the result array to restore standard format
	if !isOriginalArray {
		var results []json.RawMessage
		if err := json.Unmarshal(output, &results); err == nil && len(results) == 1 {
			output = results[0]
		}
	}

	proofEnv, err := core.NewEnvelope(
		uuid.New().String(),
		switchWorkerDID,
		workflows.OrchestratorDID,
		env.ConversationID,
		core.INFORM,
		json.RawMessage(output),
	)
	if err != nil {
		return fmt.Errorf("switch worker: failed to build response envelope: %w", err)
	}

	js, err := s.nc.JetStream()
	if err != nil {
		return fmt.Errorf("switch worker: jetstream context failure: %w", err)
	}

	respBytes, _ := json.Marshal(proofEnv)
	_, err = js.Publish(workflows.OrchestratorInbox, respBytes)
	if err != nil {
		return fmt.Errorf("switch worker: failed to publish to orchestrator: %w", err)
	}

	s.logger.Debug("switch worker: routed payload", "cid", env.ConversationID)
	return nil
}

func ensureJSONArray(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("[]")
	}
	if gjson.ParseBytes(raw).IsArray() {
		return raw
	}
	return append(append([]byte("["), raw...), ']')
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewSwitchWorkerService(deps.Queue, deps.Logger, deps.Config)
	})
}
