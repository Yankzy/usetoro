package llm

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

type GoogleAdapter struct {
	apiKey string
}

func NewGoogleAdapter(apiKey string) (*GoogleAdapter, error) {
	return &GoogleAdapter{apiKey: apiKey}, nil
}

func (g *GoogleAdapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	// TODO: Implement actual call to Google Gemini API
	// Extract messages, tools, map to Google format, call API, map back.
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "Google Gemini Adapter not yet implemented",
				},
			},
		},
	}, fmt.Errorf("google provider not fully implemented")
}
