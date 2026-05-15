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

	maxPages := 3
	pageCount := 0

	var lastResponseID string
	var toolOutputs responses.ResponseInputParam

	for attempt := 0; attempt < 10; attempt++ {
		params := responses.ResponseNewParams{
			Model: shared.ChatModel(r.Config.Model),
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
					false, // strict
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
		r.Logger.Info("🧠 [DEBUG] LLM RESPONSE API", "response_id", resp.ID, "status", resp.Status)
		// respJSON, _ := json.Marshal(resp)
		// r.Logger.Debug("🧠 [DEBUG] FULL LLM RESPONSE", "response", string(respJSON))

		// Reset toolOutputs for this iteration
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
								outputMsg = docContent
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
			outputText := resp.OutputText()
			if outputText != "" {
				return outputText, nil
			}
			return "", fmt.Errorf("empty response generated without tools")
		}
	}

	return "", fmt.Errorf("exceeded max reasoning loops intrinsically mapped")
}

// {
// 	output:[{
// 		id:msg_0d89a4fdb4ab095c006a0293c69c50819e9dddf31bc1afe61f,
// 		content:[{text:The total amount of transactions for Q1 is $150., type:output_text}],
// 		role:assistant,
// 		status:completed,
// 	}],
// 	tools:[{
// 			name:PAGE_IN,
// 			parameters:{
// 				properties:{
// 					local_ref:{description:The exact integer local_ref from the available_pages directory.,type:integer}
// 			},
// 			required:[local_ref],
// 			type:object},
// 			strict:false,
// 			type:function,
// 			defer_loading:false,
// 			description:,
// 			vector_store_ids:null
// 		}],
//  id:"resp_0d89a4fdb4ab095c006a0293c5e7bc819ea0f7e1eee6e8f8e5",
// 	model:gpt-4o-2024-08-06,
// 	object:response,
// 	top_p:1,
// 	background:false,
// 	completed_at:1778553798,
// 	conversation:{id:},
// 	max_output_tokens:0,
// 	max_tool_calls:0,
// 	previous_response_id:resp_0d89a4fdb4ab095c006a0293c407cc819eae2edd7b7c7fe157,
// 	status:completed,
// 	usage:{
// 		input_tokens:203,
// 		input_tokens_details:{cached_tokens:0},
// 		output_tokens:14,
// 		output_tokens_details:{reasoning_tokens:0},
// 		total_tokens:217
// 	}
// }
