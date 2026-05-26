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

type contextKey string

const ctxKeyModel contextKey = "tap_model_override"

// WithModel returns a context with a per-call model override.
func WithModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, ctxKeyModel, model)
}

func modelFromContext(ctx context.Context, fallback string) string {
	if v, ok := ctx.Value(ctxKeyModel).(string); ok && v != "" {
		return v
	}
	return fallback
}

func (r *Runtime) effectiveModel(ctx context.Context) string {
	return modelFromContext(ctx, r.Config.Model)
}

// Start begins the event loop.
func (r *Runtime) Start() error {
	if r.Config.TaskQueue == "" {
		r.Logger.Info("Agent Runtime: no task queue assigned yet, skipping subscription")
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

	r.Logger.Info("Agent Online", "model", r.Config.Model, "task_queue", r.Config.TaskQueue)
	return nil
}

// Stop unsubscribes.
func (r *Runtime) Stop() error {
	if r.sub != nil {
		return r.sub.Drain()
	}
	return nil
}

func (r *Runtime) resolveClient(ctx context.Context) (ProviderConfig, openai.Client, error) {
	model := r.effectiveModel(ctx)
	pc, err := ResolveModel(model)
	if err != nil {
		return pc, openai.Client{}, fmt.Errorf("resolve model %q: %w", model, err)
	}
	client := openai.NewClient(pc.ClientOptions()...)
	return pc, client, nil
}

// Exec provides the standardized static LLM execution signature.
func (r *Runtime) Exec(ctx context.Context, prompt string, systemPrompt string) (string, error) {
	if systemPrompt == "" {
		systemPrompt = r.Config.SystemPrompt
	}

	mapper := NewUUIDMapper()

	fullPrompt := prompt
	if systemPrompt != "" {
		fullPrompt = fmt.Sprintf("SYSTEM: %s\n\n%s", systemPrompt, prompt)
	}
	fullPrompt = mapper.Obfuscate(fullPrompt)

	pc, client, err := r.resolveClient(ctx)
	if err != nil {
		return "", err
	}

	modelToCall := r.effectiveModel(ctx)
	r.Logger.Info("Executing LLM", "model", modelToCall, "provider", pc.Name, "paradigm", pc.Paradigm, "base_url", pc.BaseURL)

	switch pc.Paradigm {
	case ParadigmResponses:
		resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(fullPrompt)},
			Model:           shared.ChatModel(r.effectiveModel(ctx)),
			MaxOutputTokens: openai.Int(16384),
		})
		if err != nil {
			return "", err
		}
		return mapper.Restore(resp.OutputText()), nil

	case ParadigmChat:
		return r.execChatCompletion(ctx, fullPrompt, systemPrompt, client, mapper)

	default:
		return "", fmt.Errorf("paradigm %q not yet implemented", pc.Paradigm)
	}
}

// execChatCompletion uses the Chat Completions API for providers that do not support the Responses API.
func (r *Runtime) execChatCompletion(ctx context.Context, userContent, systemContent string, client openai.Client, mapper *UUIDMapper) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(userContent),
	}
	if systemContent != "" {
		messages = append([]openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemContent),
		}, messages...)
	}

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(r.effectiveModel(ctx)),
		Messages:            messages,
		MaxCompletionTokens: openai.Int(16384),
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("chat completion: no choices returned")
	}
	return mapper.Restore(resp.Choices[0].Message.Content), nil
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
		r.Logger.Error("Reasoning failed", "error", err)
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

	mapper := NewUUIDMapper()
	prompt = mapper.Obfuscate(prompt)
	systemPrompt = mapper.Obfuscate(systemPrompt)

	pc, client, err := r.resolveClient(ctx)
	if err != nil {
		return "", err
	}

	switch pc.Paradigm {
	case ParadigmResponses:
		return r.execWithPagingResponses(ctx, client, prompt, systemPrompt, pages, fetcher, mapper)
	case ParadigmChat:
		return r.execWithPagingChat(ctx, client, prompt, systemPrompt, pages, fetcher, mapper)
	default:
		return "", fmt.Errorf("paradigm %q not yet implemented", pc.Paradigm)
	}
}

func (r *Runtime) execWithPagingResponses(ctx context.Context, client openai.Client, prompt, systemPrompt string, pages []PageContext, fetcher DocumentFetcher, mapper *UUIDMapper) (string, error) {
	localMap, pagesJSON := GenerateLocalContextMap(pages)

	sysPrompt := ""
	if systemPrompt != "" {
		sysPrompt = systemPrompt + "\n\n"
	}
	sysPrompt += fmt.Sprintf("AVAILABLE PAGES DIRECTORY:\n%s\n\nUse the PAGE_IN tool with a local_ref to fetch uncompressed documents. DO NOT guess paths. You MUST use integers mapping directly specifically from the Context Directory mapping.", string(pagesJSON))

	maxPages := 3
	pageCount := 0

	var lastResponseID string
	var toolOutputs responses.ResponseInputParam

	for attempt := 0; attempt < 10; attempt++ {
		params := responses.ResponseNewParams{
			Model:           shared.ChatModel(r.effectiveModel(ctx)),
			MaxOutputTokens: openai.Int(16384),
			Tools: []responses.ToolUnionParam{
				responses.ToolParamOfFunction(
					"PAGE_IN",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"local_ref": map[string]any{
								"type":        "integer",
								"description": "The exact integer local_ref from the available_pages directory.",
							},
						},
						"required": []string{"local_ref"},
					},
					false,
				),
			},
		}

		if attempt == 0 {
			params.Input = responses.ResponseNewParamsInputUnion{
				OfString: openai.String(sysPrompt + "\n" + prompt),
			}
		} else {
			params.PreviousResponseID = openai.String(lastResponseID)
			params.Input = responses.ResponseNewParamsInputUnion{
				OfInputItemList: toolOutputs,
			}
		}

		resp, err := client.Responses.New(ctx, params)
		if err != nil {
			return "", err
		}

		lastResponseID = resp.ID
		r.Logger.Info("[DEBUG] LLM RESPONSE API", "response_id", resp.ID, "status", resp.Status)

		toolOutputs = nil
		hasToolCalls := false

		for _, item := range resp.Output {
			if item.Type == "function_call" {
				hasToolCalls = true
				call := item.AsFunctionCall()

				if call.Name == "PAGE_IN" {
					var args struct {
						LocalRef int `json:"local_ref"`
					}
					_ = json.Unmarshal([]byte(call.Arguments), &args)

					var outputMsg string

					if pageCount >= maxPages {
						outputMsg = "SYSTEM ERROR: MAX_PAGES_PER_CYCLE reached. Aborting fetch. Must explicitly emit outputs directly structurally."
					} else {
						pageCount++
						uuidStr, ok := localMap[args.LocalRef]
						if !ok {
							outputMsg = "ERROR: Invalid local_ref. Not found explicitly in target directory."
						} else {
							var docContent string
							var fetchErr error
							if fetcher != nil {
								docContent, fetchErr = fetcher(ctx, uuidStr)
							} else {
								fetchErr = fmt.Errorf("no document fetcher injected explicitly")
							}

							if fetchErr != nil {
								outputMsg = "ERROR: Failed resolving source explicitly intrinsically: " + fetchErr.Error()
							} else {
								outputMsg = mapper.Obfuscate(docContent)
							}
						}
					}

					toolOutputs = append(toolOutputs, responses.ResponseInputItemParamOfFunctionCallOutput(call.CallID, outputMsg))
				} else {
					toolOutputs = append(toolOutputs, responses.ResponseInputItemParamOfFunctionCallOutput(call.CallID, "ERROR: Unknown tool executed intrinsically."))
				}
			}
		}
		if !hasToolCalls {
			outputText := mapper.Restore(resp.OutputText())
			if outputText != "" {
				return outputText, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}
	}

	return "", fmt.Errorf("exceeded max reasoning loops intrinsically mapped")
}

// execWithPagingChat implements the tool-calling paging loop using the Chat Completions API
// (messages-array paradigm) for providers that do not support the Responses API.
func (r *Runtime) execWithPagingChat(ctx context.Context, client openai.Client, prompt, systemPrompt string, pages []PageContext, fetcher DocumentFetcher, mapper *UUIDMapper) (string, error) {
	localMap, pagesJSON := GenerateLocalContextMap(pages)

	sysPrompt := systemPrompt
	if sysPrompt != "" {
		sysPrompt += "\n\n"
	}
	sysPrompt += fmt.Sprintf("AVAILABLE PAGES DIRECTORY:\n%s\n\nUse the PAGE_IN tool with a local_ref to fetch uncompressed documents. DO NOT guess paths. You MUST use integers mapping directly specifically from the Context Directory mapping.", string(pagesJSON))

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(sysPrompt),
		openai.UserMessage(prompt),
	}

	tools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        "PAGE_IN",
			Description: openai.String("Fetch uncompressed document content by local_ref from the available pages directory."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"local_ref": map[string]any{
						"type":        "integer",
						"description": "The exact integer local_ref from the available_pages directory.",
					},
				},
				"required": []string{"local_ref"},
			},
		}),
	}

	maxPages := 3
	pageCount := 0

	for attempt := 0; attempt < 10; attempt++ {
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:               shared.ChatModel(r.effectiveModel(ctx)),
			Messages:            messages,
			Tools:               tools,
			MaxCompletionTokens: openai.Int(16384),
		})
		if err != nil {
			return "", err
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("chat completion: no choices returned")
		}

		choice := resp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			outputText := mapper.Restore(choice.Message.Content)
			if outputText != "" {
				return outputText, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}

		// Append assistant message (with tool_calls) to the conversation
		messages = append(messages, choice.Message.ToParam())

		// Execute each tool call and append tool result messages
		for _, tc := range choice.Message.ToolCalls {
			var outputMsg string

			if tc.Function.Name == "PAGE_IN" {
				var args struct {
					LocalRef int `json:"local_ref"`
				}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				if pageCount >= maxPages {
					outputMsg = "SYSTEM ERROR: MAX_PAGES_PER_CYCLE reached. Aborting fetch. Must explicitly emit outputs directly structurally."
				} else {
					pageCount++
					uuidStr, ok := localMap[args.LocalRef]
					if !ok {
						outputMsg = "ERROR: Invalid local_ref. Not found explicitly in target directory."
					} else {
						var docContent string
						var fetchErr error
						if fetcher != nil {
							docContent, fetchErr = fetcher(ctx, uuidStr)
						} else {
							fetchErr = fmt.Errorf("no document fetcher injected explicitly")
						}

						if fetchErr != nil {
							outputMsg = "ERROR: Failed resolving source explicitly intrinsically: " + fetchErr.Error()
						} else {
							outputMsg = mapper.Obfuscate(docContent)
						}
					}
				}
			} else {
				outputMsg = "ERROR: Unknown tool executed intrinsically."
			}

			messages = append(messages, openai.ToolMessage(outputMsg, tc.ID))
		}
	}

	return "", fmt.Errorf("exceeded max reasoning loops intrinsically mapped")
}

// ── Multi-tool calling (PAGE_IN + arbitrary tools) ──────────────────────

// ToolDef describes a function tool the LLM can call.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCallHandler executes a tool identified by name and returns its output.
type ToolCallHandler func(ctx context.Context, name string, args map[string]any) (string, error)

// ExecWithToolCalling runs the LLM with PAGE_IN plus extraTools.  The handler
// is invoked for any tool that is not PAGE_IN.  Document pages are fetched via
// the supplied fetcher exactly as in ExecWithPaging.
func (r *Runtime) ExecWithToolCalling(ctx context.Context, prompt string, systemPrompt string, pages []PageContext, fetcher DocumentFetcher, extraTools []ToolDef, toolHandler ToolCallHandler) (string, error) {
	if systemPrompt == "" {
		systemPrompt = r.Config.SystemPrompt
	}

	mapper := NewUUIDMapper()
	prompt = mapper.Obfuscate(prompt)
	systemPrompt = mapper.Obfuscate(systemPrompt)

	pc, client, err := r.resolveClient(ctx)
	if err != nil {
		return "", err
	}

	switch pc.Paradigm {
	case ParadigmResponses:
		return r.execWithToolsResponses(ctx, client, prompt, systemPrompt, pages, fetcher, extraTools, toolHandler, mapper)
	case ParadigmChat:
		return r.execWithToolsChat(ctx, client, prompt, systemPrompt, pages, fetcher, extraTools, toolHandler, mapper)
	default:
		return "", fmt.Errorf("paradigm %q not yet implemented", pc.Paradigm)
	}
}

func (r *Runtime) execWithToolsResponses(ctx context.Context, client openai.Client, prompt, systemPrompt string, pages []PageContext, fetcher DocumentFetcher, extraTools []ToolDef, toolHandler ToolCallHandler, mapper *UUIDMapper) (string, error) {
	localMap, pagesJSON := GenerateLocalContextMap(pages)

	sysPrompt := ""
	if systemPrompt != "" {
		sysPrompt = systemPrompt + "\n\n"
	}
	if len(pages) > 0 {
		sysPrompt += fmt.Sprintf("AVAILABLE PAGES DIRECTORY:\n%s\n\nUse the PAGE_IN tool with a local_ref to fetch uncompressed documents.\n", string(pagesJSON))
	}

	rtools := []responses.ToolUnionParam{
		responses.ToolParamOfFunction("PAGE_IN", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"local_ref": map[string]any{"type": "integer", "description": "The exact integer local_ref from the available_pages directory."},
			},
			"required": []string{"local_ref"},
		}, false),
	}
	for _, t := range extraTools {
		rtools = append(rtools, responses.ToolParamOfFunction(t.Name, t.Schema, false))
	}

	maxPages := 3
	pageCount := 0
	var lastResponseID string
	var toolOutputs responses.ResponseInputParam

	for attempt := 0; attempt < 10; attempt++ {
		params := responses.ResponseNewParams{
			Model:           shared.ChatModel(r.effectiveModel(ctx)),
			MaxOutputTokens: openai.Int(16384),
			Tools:           rtools,
		}
		if attempt == 0 {
			params.Input = responses.ResponseNewParamsInputUnion{OfString: openai.String(sysPrompt + "\n" + prompt)}
		} else {
			params.PreviousResponseID = openai.String(lastResponseID)
			params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: toolOutputs}
		}

		resp, err := client.Responses.New(ctx, params)
		if err != nil {
			return "", err
		}
		lastResponseID = resp.ID
		toolOutputs = nil
		hasToolCalls := false

		for _, item := range resp.Output {
			if item.Type == "function_call" {
				hasToolCalls = true
				call := item.AsFunctionCall()
				var args map[string]any
				_ = json.Unmarshal([]byte(call.Arguments), &args)

				var outputMsg string
				switch {
				case call.Name == "PAGE_IN":
					var pargs struct {
						LocalRef int `json:"local_ref"`
					}
					_ = json.Unmarshal([]byte(call.Arguments), &pargs)
					if pageCount >= maxPages {
						outputMsg = "ERROR: MAX_PAGES_PER_CYCLE reached."
					} else {
						pageCount++
						uuidStr, ok := localMap[pargs.LocalRef]
						if !ok {
							outputMsg = "ERROR: Invalid local_ref."
						} else if fetcher == nil {
							outputMsg = "ERROR: no document fetcher."
						} else {
							doc, ferr := fetcher(ctx, uuidStr)
							if ferr != nil {
								outputMsg = "ERROR: " + ferr.Error()
							} else {
								outputMsg = mapper.Obfuscate(doc)
							}
						}
					}
				case toolHandler != nil:
					out, herr := toolHandler(ctx, call.Name, args)
					if herr != nil {
						outputMsg = "ERROR: " + herr.Error()
					} else {
						outputMsg = mapper.Obfuscate(out)
					}
				default:
					outputMsg = "ERROR: Unknown tool '" + call.Name + "' — no handler registered."
				}
				toolOutputs = append(toolOutputs, responses.ResponseInputItemParamOfFunctionCallOutput(call.CallID, outputMsg))
			}
		}

		if !hasToolCalls {
			outputText := mapper.Restore(resp.OutputText())
			if outputText != "" {
				return outputText, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}
	}
	return "", fmt.Errorf("exceeded max reasoning loops")
}

func (r *Runtime) execWithToolsChat(ctx context.Context, client openai.Client, prompt, systemPrompt string, pages []PageContext, fetcher DocumentFetcher, extraTools []ToolDef, toolHandler ToolCallHandler, mapper *UUIDMapper) (string, error) {
	localMap, pagesJSON := GenerateLocalContextMap(pages)

	sysPrompt := systemPrompt
	if len(pages) > 0 {
		if sysPrompt != "" {
			sysPrompt += "\n\n"
		}
		sysPrompt += fmt.Sprintf("AVAILABLE PAGES DIRECTORY:\n%s\n\nUse PAGE_IN tool with a local_ref to fetch documents.", string(pagesJSON))
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(sysPrompt),
		openai.UserMessage(prompt),
	}

	chatTools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        "PAGE_IN",
			Description: openai.String("Fetch uncompressed document content by local_ref."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"local_ref": map[string]any{"type": "integer", "description": "The exact integer local_ref from the directory."},
				},
				"required": []string{"local_ref"},
			},
		}),
	}
	for _, t := range extraTools {
		chatTools = append(chatTools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Schema),
		}))
	}

	maxPages := 3
	pageCount := 0

	for attempt := 0; attempt < 10; attempt++ {
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:               shared.ChatModel(r.effectiveModel(ctx)),
			Messages:            messages,
			Tools:               chatTools,
			MaxCompletionTokens: openai.Int(16384),
		})
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("chat: no choices returned")
		}
		choice := resp.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			outputText := mapper.Restore(choice.Message.Content)
			if outputText != "" {
				return outputText, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}
		messages = append(messages, choice.Message.ToParam())
		for _, tc := range choice.Message.ToolCalls {
			var outputMsg string
			switch {
			case tc.Function.Name == "PAGE_IN":
				var pargs struct {
					LocalRef int `json:"local_ref"`
				}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &pargs)
				if pageCount >= maxPages {
					outputMsg = "ERROR: MAX_PAGES_PER_CYCLE reached."
				} else {
					pageCount++
					uuidStr, ok := localMap[pargs.LocalRef]
					if !ok {
						outputMsg = "ERROR: Invalid local_ref."
					} else if fetcher == nil {
						outputMsg = "ERROR: no document fetcher."
					} else {
						doc, ferr := fetcher(ctx, uuidStr)
						if ferr != nil {
							outputMsg = "ERROR: " + ferr.Error()
						} else {
							outputMsg = mapper.Obfuscate(doc)
						}
					}
				}
			case toolHandler != nil:
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				out, herr := toolHandler(ctx, tc.Function.Name, args)
				if herr != nil {
					outputMsg = "ERROR: " + herr.Error()
				} else {
					outputMsg = mapper.Obfuscate(out)
				}
			default:
				outputMsg = "ERROR: Unknown tool '" + tc.Function.Name + "'."
			}
			messages = append(messages, openai.ToolMessage(outputMsg, tc.ID))
		}
	}
	return "", fmt.Errorf("exceeded max reasoning loops")
}
