package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// Runtime represents a single, autonomous agent instance.
type Runtime struct {
	Config core.AgentConfig
	Logger *slog.Logger
	Bus    core.EventBus
	sub    *nats.Subscription
}

// NewRuntime initializes the agent.
func NewRuntime(logger *slog.Logger, bus core.EventBus, cfg core.AgentConfig) *Runtime {
	return &Runtime{
		Config: cfg,
		Logger: logger.With("did", cfg.DID),
		Bus:    bus,
		sub:    nil,
	}
}

// Start begins the event loop.
func (r *Runtime) Start() error {
	// Subscribe to the task queue assigned by the Orchestrator.
	// If TaskQueue is empty this is a no-op (agent relies on inbox subscription in BaseAgent).
	if r.Config.TaskQueue == "" {
		r.Logger.Info("🧠 Agent Runtime: no task queue assigned yet, skipping subscription")
		return nil
	}

	var err error
	r.sub, err = r.Bus.QueueSubscribe(
		r.Config.TaskQueue,
		r.Config.QueueGroup,
		r.handleTrigger,
		nats.Durable(r.Config.DID),
		nats.ManualAck(),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	r.Logger.Info("🧠 Agent Online", "model", r.Config.Model, "task_queue", r.Config.TaskQueue)
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
func (r *Runtime) Exec(ctx context.Context, prompt string, systemPrompt string) (string, error) {
	if systemPrompt == "" {
		systemPrompt = r.Config.SystemPrompt
	}

	fullPrompt := prompt
	if systemPrompt != "" {
		fullPrompt = fmt.Sprintf("SYSTEM: %s\n\n%s", systemPrompt, prompt)
	}
	client := openai.NewClient()

	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(fullPrompt)},
		Model: shared.ChatModel(r.Config.Model),
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

	response, err := r.Exec(ctx, input, r.Config.SystemPrompt)
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

// ExecWithPaging replaces naive text calls with an advanced Tool Calling interceptor structurally validating document arrays securely!
func (r *Runtime) ExecWithPaging(ctx context.Context, prompt string, systemPrompt string, pages []PageContext, fetcher DocumentFetcher) (string, error) {
	if systemPrompt == "" {
		systemPrompt = r.Config.SystemPrompt
	}

	client := openai.NewClient()

	localMap, pagesJSON := GenerateLocalContextMap(pages)

	sysPrompt := ""
	if systemPrompt != "" {
		sysPrompt = systemPrompt + "\n\n"
	}
	sysPrompt += fmt.Sprintf("AVAILABLE PAGES DIRECTORY:\n%s\n\nUse the PAGE_IN tool with a local_ref to fetch uncompressed documents. DO NOT guess paths. You MUST use integers mapping directly specifically from the Context Directory mapping.", string(pagesJSON))

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(sysPrompt),
		openai.UserMessage(prompt),
	}

	pageInTool := openai.ChatCompletionFunctionTool(
		shared.FunctionDefinitionParam{
			Name:        "PAGE_IN",
			Description: openai.String("Fetch the full raw text of a document using its local reference number."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]interface{}{
					"local_ref": map[string]interface{}{
						"type":        "integer",
						"description": "The exact integer local_ref from the available_pages directory.",
					},
				},
				"required": []string{"local_ref"},
			},
		},
	)

	maxPages := 3
	pageCount := 0

	for attempt := 0; attempt < 10; attempt++ {
		// TODO: Change to Response API for OpenAI tracing and logs
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    shared.ChatModel(r.Config.Model),
			Messages: messages,
			Tools:    []openai.ChatCompletionToolUnionParam{pageInTool},
		})

		if err != nil {
			return "", err
		}

		choice := resp.Choices[0]
		msg := choice.Message

		// Format output response structurally
		messages = append(messages, msg.ToParam())
		r.Logger.Info("🧠 [DEBUG] LLM RESPONSE: " + msg.Content)
		// If the model returns content without tool calls, it's a terminal response.
		if len(msg.ToolCalls) == 0 {
			if msg.Content != "" {
				// Base case completion structurally verified
				return msg.Content, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}

		// Tool Interceptor Loop matching strict structural execution paradigms
		for _, toolCall := range msg.ToolCalls {
			if toolCall.Function.Name == "PAGE_IN" {
				// Parse internal arguments strictly
				var args struct {
					LocalRef int `json:"local_ref"`
				}
				_ = json.Unmarshal([]byte(toolCall.Function.Arguments), &args)

				if pageCount >= maxPages {
					errorMsg := "SYSTEM ERROR: MAX_PAGES_PER_CYCLE reached. Aborting fetch. Must explicitly emit outputs directly structurally."
					messages = append(messages, openai.ToolMessage(toolCall.ID, errorMsg))
					continue
				}
				pageCount++

				uuidStr, ok := localMap[args.LocalRef]
				if !ok {
					messages = append(messages, openai.ToolMessage(toolCall.ID, "ERROR: Invalid local_ref. Not found explicitly in target directory."))
					continue
				}

				var docContent string
				var fetchErr error
				if fetcher != nil {
					docContent, fetchErr = fetcher(ctx, uuidStr)
				} else {
					fetchErr = fmt.Errorf("no document fetcher injected explicitly")
				}

				if fetchErr != nil {
					messages = append(messages, openai.ToolMessage(toolCall.ID, "ERROR: Failed resolving source explicitly intrinsically: "+fetchErr.Error()))
					continue
				}

				// Successfully loaded!
				messages = append(messages, openai.ToolMessage(toolCall.ID, docContent))
			} else {
				// Safety fallback handling implicitly
				messages = append(messages, openai.ToolMessage(toolCall.ID, "ERROR: Unknown tool executed intrinsically."))
			}
		}
	}

	return "", fmt.Errorf("exceeded max reasoning loops intrinsically mapped")
}
