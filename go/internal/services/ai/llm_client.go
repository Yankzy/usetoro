package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// LLMClient provides a shared wrapper around openai-go/v3 for Responses
type LLMClient struct {
	client *openai.Client
	model  shared.ChatModel
}

// NewLLMClient creates a new LLM client. If model is empty, defaults to gpt-5.4
func NewLLMClient(apiKey, model string) (*LLMClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	if model == "" {
		model = string(shared.ChatModelGPT5_4)
	}

	client := openai.NewClient(option.WithAPIKey(apiKey))

	return &LLMClient{
		client: &client,
		model:  shared.ChatModel(model),
	}, nil
}

// GenerateJSON struct requires a system prompt, a user prompt, and an output pointer (struct).
// It forces the model to return valid JSON matching the shape of `output`.
func (c *LLMClient) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
	prompt := fmt.Sprintf("SYSTEM INSTRUCTIONS:\n%s\n\nUSER REQUEST:\n%s", systemPrompt, userPrompt)
	jsonFmt := shared.NewResponseFormatJSONObjectParam()

	req := responses.ResponseNewParams{
		Model:       shared.ResponsesModel(c.model),
		Input:       responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)},
		Temperature: openai.Float(0.1), // Low temp for deterministic mapping
		Reasoning: shared.ReasoningParam{
			Effort: shared.ReasoningEffortNone, // Required to use 'Temperature' with gpt-5.4
		},
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &jsonFmt,
			},
		},
	}

	resp, err := c.client.Responses.New(ctx, req)
	if err != nil {
		return fmt.Errorf("llm completion error: %w", err)
	}

	rawJSON := resp.OutputText()

	// Unmarshal directly into the provided struct pointer
	if err := json.Unmarshal([]byte(rawJSON), output); err != nil {
		return fmt.Errorf("failed to parse LLM JSON response: %w\nRaw: %s", err, rawJSON)
	}

	return nil
}

// GenerateText is a helper for standard, non-JSON completions
func (c *LLMClient) GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	prompt := fmt.Sprintf("SYSTEM INSTRUCTIONS:\n%s\n\nUSER REQUEST:\n%s", systemPrompt, userPrompt)

	req := responses.ResponseNewParams{
		Model:       shared.ResponsesModel(c.model),
		Input:       responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)},
		Temperature: openai.Float(0.7),
		Reasoning: shared.ReasoningParam{
			Effort: shared.ReasoningEffortNone, // Required to use 'Temperature' with gpt-5.4
		},
	}

	resp, err := c.client.Responses.New(ctx, req)
	if err != nil {
		return "", fmt.Errorf("llm completion error: %w", err)
	}

	return strings.TrimSpace(resp.OutputText()), nil
}
