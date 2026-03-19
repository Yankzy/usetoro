package agent

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// Runtime represents a single, autonomous agent instance.
type Runtime struct {
	Config AgentConfig
	Logger *slog.Logger
	Bus    EventBus
	Memory MemoryStore
	sub    *nats.Subscription
}

// NewRuntime initializes the agent.
func NewRuntime(logger *slog.Logger, bus EventBus, cfg AgentConfig, mem MemoryStore) *Runtime {
	return &Runtime{
		Config: cfg,
		Logger: logger.With("did", cfg.DID),
		Bus:    bus,
		Memory: mem,
		sub:    nil,
	}
}

// Start begins the event loop.
func (r *Runtime) Start() error {
	var err error
	r.sub, err = r.Bus.QueueSubscribe(
		r.Config.Subscription.Subject,
		r.Config.Subscription.QueueGroup,
		r.handleTrigger,
		nats.BindStream("GATE"),
		nats.Durable(r.Config.DID),
		nats.ManualAck(),
	)

	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	r.Logger.Info("🧠 Agent Online", "model", r.Config.Model)
	return nil
}

// Stop unsubscribes.
func (r *Runtime) Stop() error {
	if r.sub != nil {
		return r.sub.Drain()
	}
	return nil
}

// Exec provides the standardized static LLM execution signature.
func (r *Runtime) Exec(ctx context.Context, prompt string) (string, error) {
	client := openai.NewClient()
	
	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)},
		Model: shared.ChatModelGPT5_4,
	})
	
	if err != nil {
		return "", err
	}
	
	return resp.OutputText(), nil
}

func (r *Runtime) handleTrigger(msg *nats.Msg) {
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("PANIC in agent runtime", "error", rec, "stack", string(debug.Stack()))
			msg.Nak()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	input := string(msg.Data)
	realmID := msg.Header.Get("Toro-Realm-ID")

	memoryContext := ""
	if r.Memory != nil && realmID != "" {
		mem, err := r.Memory.Recall(ctx, realmID, input)
		if err == nil && mem != "" {
			memoryContext = mem
			r.Logger.Info("🧠 Memory Injected", "rules", memoryContext)
		} else if err != nil {
			r.Logger.Warn("Memory recall warning", "error", err)
		}
	}

	fullPrompt := input
	if memoryContext != "" {
		fullPrompt = fmt.Sprintf("USER INPUT: %s\n\n[IMPORTANT CONTEXT RULES]:\n%s", input, memoryContext)
	}
	
	if r.Config.SystemPrompt != "" {
		fullPrompt = fmt.Sprintf("SYSTEM: %s\n\n%s", r.Config.SystemPrompt, fullPrompt)
	}

	response, err := r.Exec(ctx, fullPrompt)
	if err != nil {
		r.Logger.Error("❌ Reasoning failed", "error", err)
		msg.Nak()
		return
	}

	if msg.Reply != "" {
		r.Bus.Publish(msg.Reply, []byte(response))
	}

	msg.Ack()
}
