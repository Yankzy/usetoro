package agent

import (
	"context"

	"github.com/Yankzy/usetoro/tap/internal/memory"
	"github.com/nats-io/nats.go"
	"github.com/sashabaranov/go-openai"
)

// LLMClient abstracts the interaction with Large Language Models.
type LLMClient interface {
	CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// EventBus abstracts the underlying messaging system (e.g., NATS).
type EventBus interface {
	// Publish sends a message to a subject.
	Publish(subject string, data []byte) error
	// Request sends a request and waits for a reply.
	RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error)
	// Subscribe to a subject (this might need to return a subscription wrapper in the future,
	// but for now we stick close to NATS to avoid over-abstracting prematurely).
	QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// MemoryStore abstracts the long-term memory/RAG storage.
type MemoryStore interface {
	Recall(ctx context.Context, realmID, query string) (string, error)
	Learn(ctx context.Context, realmID, trigger, instruction string) error
}

// Ensure concrete types implement the interfaces (compile-time check)
var _ MemoryStore = (*memory.Manager)(nil)
