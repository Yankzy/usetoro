package llm

import (
	"context"

	"github.com/sashabaranov/go-openai"
)

// Client abstracts the interaction with Large Language Models.
type Client interface {
	CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}
