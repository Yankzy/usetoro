package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/llm"
	"github.com/nats-io/nats.go"
	"github.com/sashabaranov/go-openai"
)

// Runtime represents a single, autonomous agent instance.
// It holds the state (Context Window) and connections to the outside world.
type Runtime struct {
	Config AgentConfig
	Logger *slog.Logger
	Bus    EventBus
	LLM    llm.Client
	Memory MemoryStore

	// Runtime State
	state State
	sub   *nats.Subscription
}

// NewRuntime initializes the agent.
// It injects dependencies via interfaces to allow for easier testing and swapping of components.
func NewRuntime(logger *slog.Logger, bus EventBus, cfg AgentConfig, llm llm.Client, mem MemoryStore) *Runtime {
	return &Runtime{
		Config: cfg,
		Logger: logger.With("did", cfg.DID),
		Bus:    bus,
		LLM:    llm,
		Memory: mem,
		state: State{
			// Initialize with the Persona/System Prompt
			History: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: cfg.SystemPrompt,
				},
			},
		},
		sub: nil,
	}
}

// Start begins the event loop. It subscribes to the NATS trigger subject.
func (r *Runtime) Start() error {
	var err error

	// QueueSubscribe ensures load balancing if we run multiple replicas of the Protocol service.
	// "GATE" is the stream name where we expect events to live.
	r.sub, err = r.Bus.QueueSubscribe(
		r.Config.Subscription.Subject,
		r.Config.Subscription.QueueGroup,
		r.handleTrigger,
		nats.BindStream("GATE"),
		nats.Durable(r.Config.DID), // Durable consumer ensures we don't miss messages if we restart
		nats.ManualAck(),           // We only ACK after successful processing
	)

	if err != nil {
		return fmt.Errorf("failed to subscribe to NATS subject %s: %w", r.Config.Subscription.Subject, err)
	}

	r.Logger.Info("🧠 Agent Online", "model", r.Config.Model)
	return nil
}

// Stop unsubscribes and cleans up resources.
func (r *Runtime) Stop() error {
	if r.sub != nil {
		// In production, we might want to Drain() instead of Unsubscribe() to finish in-flight msg
		return r.sub.Drain()
	}
	return nil
}

// handleTrigger is the entry point for the "Reasoning Loop"
func (r *Runtime) handleTrigger(msg *nats.Msg) {
	// Panic Recovery Middleware
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("PANIC in agent runtime", "error", rec, "stack", string(debug.Stack()))
			// NAK ensures the message is retried (or dead-lettered)
			msg.Nak()
		}
	}()

	// Strict timeout for the entire lifecycle of one event
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	input := string(msg.Data)

	// --- 1. MEMORY RECALL LAYER (RAG) ---
	// We check if we have any specific rules for this tenant/input before thinking.

	// Extract Realm ID from NATS Headers (Set by the Go Gate)
	// We use Realm ID (QBO Company ID) as the primary scope for the Agent's memory.
	realmID := msg.Header.Get("Toro-Realm-ID")

	memoryContext := ""
	if r.Memory != nil && realmID != "" {
		// Recall checks Postgres for user corrections (e.g., "Home Depot is Repairs")
		mem, err := r.Memory.Recall(ctx, realmID, input)
		if err == nil && mem != "" {
			memoryContext = mem
			r.Logger.Info("🧠 Memory Injected", "rules", memoryContext)
		} else if err != nil {
			// Log warning but don't fail; memory is an enhancement, not a blocker
			r.Logger.Warn("Memory recall warning", "error", err)
		}
	}

	// Inject Rules into the User Prompt
	fullPrompt := input
	if memoryContext != "" {
		fullPrompt = fmt.Sprintf("USER INPUT: %s\n\n[IMPORTANT CONTEXT RULES]:\n%s", input, memoryContext)
	}

	// ---------------------------

	// Add to Context Window
	r.state.History = append(r.state.History, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: fullPrompt,
	})

	// Run the ReAct Loop
	response, err := r.runReasoningLoop(ctx)
	if err != nil {
		r.Logger.Error("❌ Reasoning failed", "error", err)
		// NAK the message so NATS redelivers it to another worker (Exponential Backoff handled by NATS)
		msg.Nak()
		return
	}

	// --- 2. RESPONSE LAYER ---

	// If the NATS message has a Reply-To subject (Request-Reply pattern), send the answer back
	if msg.Reply != "" {
		r.Bus.Publish(msg.Reply, []byte(response))
	} else {
		// Otherwise, maybe publish to a standardized output stream?
		// r.Bus.Publish(fmt.Sprintf("agent.output.%s", r.Config.DID), []byte(response))
	}

	// Acknowledge success to NATS
	msg.Ack()
}

// runReasoningLoop executes the "Think -> Tool -> Think" cycle
func (r *Runtime) runReasoningLoop(ctx context.Context) (string, error) {
	// Prepare tool definitions for OpenAI
	tools := r.buildTools()

	// Safety: Max 5 turns to prevent infinite loops (cost protection)
	for i := 0; i < 5; i++ {
		// A. Call LLM (The Brain)
		req := openai.ChatCompletionRequest{
			Model:       r.Config.Model,
			Messages:    r.state.History,
			Tools:       tools,
			Temperature: 0, // Deterministic for banking
		}

		resp, err := r.LLM.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", fmt.Errorf("llm api error: %w", err)
		}

		msg := resp.Choices[0].Message

		// Append the assistant's thought/response to history
		r.state.History = append(r.state.History, msg)

		// B. Check if LLM wants to run a tool
		if len(msg.ToolCalls) == 0 {
			// No tools, we are done. Return the final text.
			return msg.Content, nil
		}

		// C. Execute Tools (The Hands)
		for _, toolCall := range msg.ToolCalls {
			r.Logger.Info("🛠️ Executing Skill", "skill", toolCall.Function.Name)

			// Call Python/External worker via NATS Request-Reply
			resultJSON, err := r.callRemoteSkill(ctx, toolCall.Function.Name, toolCall.Function.Arguments)
			if err != nil {
				// We assume tool failure is "recoverable" by the LLM (it might retry or apologize)
				resultJSON = fmt.Sprintf("Error executing tool: %v", err)
				r.Logger.Error("Tool execution failed", "skill", toolCall.Function.Name, "error", err)
			}

			// Add Tool Result to History (Closing the loop)
			r.state.History = append(r.state.History, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    resultJSON,
				ToolCallID: toolCall.ID,
			})
		}
		// Loop continues to next iteration (LLM sees tool output and decides next step)
	}

	return "", fmt.Errorf("agent loop exceeded max turns")
}

// callRemoteSkill bridges the Go Brain to the Python Hands via NATS
func (r *Runtime) callRemoteSkill(ctx context.Context, name string, args string) (string, error) {
	var subject string

	// Map the LLM Function Name to the NATS Subject from Config
	for _, t := range r.Config.Tools {
		if t.Name == name {
			subject = t.Subject
			break
		}
	}
	if subject == "" {
		return "", fmt.Errorf("unknown tool mapping: %s", name)
	}

	// NATS Request (RPC) to Python Worker
	// We use the Context to enforce timeout (e.g. OCR shouldn't take > 10s)
	msg, err := r.Bus.RequestWithContext(ctx, subject, []byte(args))
	if err != nil {
		return "", err
	}

	return string(msg.Data), nil
}

// buildTools converts our internal config into OpenAI's expected format
func (r *Runtime) buildTools() []openai.Tool {
	var tools []openai.Tool
	for _, t := range r.Config.Tools {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				// In a real impl, you'd parse parameters from the YAML schema.
				// For now, we allow loose JSON inputs.
				Parameters: json.RawMessage(`{"type": "object", "properties": {}, "additionalProperties": true}`),
			},
		})
	}
	return tools
}
