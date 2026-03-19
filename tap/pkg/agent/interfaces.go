package agent

import (
	"context"

	"github.com/Yankzy/usetoro/tap/internal/memory"
	"github.com/nats-io/nats.go"
)

// EventBus abstracts the underlying messaging system (e.g., NATS).
type EventBus interface {
	Publish(subject string, data []byte) error
	RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error)
	QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// MemoryStore abstracts the long-term memory/RAG storage.
type MemoryStore interface {
	Recall(ctx context.Context, realmID, query string) (string, error)
	Learn(ctx context.Context, realmID, trigger, instruction string) error
}

var _ MemoryStore = (*memory.Manager)(nil)
