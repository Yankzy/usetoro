package agent

import (
	"context"
	"encoding/json"
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

// ExecWithPaging replaces naive text calls with an advanced Tool Calling interceptor structurally validating document arrays securely!
func (r *Runtime) ExecWithPaging(ctx context.Context, prompt string, pages []PageContext, fetcher DocumentFetcher) (string, error) {
	client := openai.NewClient()

	localMap, pagesJSON := GenerateLocalContextMap(pages)

	// Inject pages directory
	sysPrompt := ""
	if r.Config.SystemPrompt != "" {
		sysPrompt = r.Config.SystemPrompt + "\n\n"
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

		if len(msg.ToolCalls) == 0 {
			if msg.Content != "" {
				// Base case completion structurally verified
				return msg.Content, nil
			}
			return "", fmt.Errorf("empty response natively generated without tools")
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
					errorMsg := "SYSTEM ERROR: MAX_PAGES_PER_CYCLE reached natively. Aborting fetch. Must explicitly emit outputs directly structurally."
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
