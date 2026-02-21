package llm

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

type AnthropicAdapter struct {
	apiKey string
}

func NewAnthropicAdapter(apiKey string) (*AnthropicAdapter, error) {
	return &AnthropicAdapter{apiKey: apiKey}, nil
}

func (a *AnthropicAdapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	// TODO: Implement actual call to Anthropic API
	// Extract messages, tools, map to Claude format, call API, map back.
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Anthropic Adapter not yet implemented",
				},
			},
		},
	}, fmt.Errorf("anthropic provider not fully implemented")
}
