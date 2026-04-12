package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"strings"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/nats-io/nats.go"
)

const (
	vectorBatchSize = 100 // OpenAI recommended batch size
	vectorTimeout   = 2 * time.Second
)

type vectorBatchItem struct {
	realmID  string
	id       string
	text     string
	metadata map[string]interface{}
	msg      *nats.Msg
}

// VectorSyncWorker synchronizes database entities to Pinecone via CDC events
type VectorSyncWorker struct {
	logger       *slog.Logger
	store        *store.Store
	vectorClient *vector.PineconeClient
	embedder     *vector.Embedder
	nc           *nats.Conn
	js           nats.JetStreamContext

	itemChan chan vectorBatchItem
	cfg      *config.Config
}

// NewVectorSyncWorker creates a new vector sync worker
func NewVectorSyncWorker(logger *slog.Logger, s *store.Store, vc *vector.PineconeClient, e *vector.Embedder, nc *nats.Conn, cfg *config.Config) (*VectorSyncWorker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &VectorSyncWorker{
		logger:       logger,
		store:        s,
		vectorClient: vc,
		embedder:     e,
		nc:           nc,
		js:           js,
		itemChan:     make(chan vectorBatchItem, 1000),
		cfg:          cfg,
	}, nil
}

func (w *VectorSyncWorker) Init(ctx context.Context) error {
	w.logger.Info("🚀 VectorSyncWorker batch processor started")
	// Start the batch processor. Note: We don't wait for WaitGroup in StartAll,
	// but processBatches handles ctx.Done() and will flush then return.
	var wg sync.WaitGroup
	wg.Add(1)
	go w.processBatches(ctx, &wg)
	return nil
}

func (w *VectorSyncWorker) Subscriptions() []SubscriptionConfig {
	if w.cfg == nil {
		w.logger.Error("vector worker: missing config")
		return nil
	}
	subjects := w.cfg.Workers.Vector
	if len(subjects) == 0 {
		w.logger.Error("vector worker: subjects not configured")
		return nil
	}

	var configs []SubscriptionConfig
	for _, subject := range subjects {
		queueGroup := groupFromSubject(subject)
		if w.cfg.Workers.VectorGroupPrefix != "" {
			queueGroup = w.cfg.Workers.VectorGroupPrefix + "-" + strings.TrimSuffix(groupFromSubject(subject), "-group")
		}
		configs = append(configs, SubscriptionConfig{
			Subject: subject,
			Group:   queueGroup,
			Options: []nats.SubOpt{nats.Durable(durableFromSubject(subject)), nats.ManualAck(), nats.AckWait(5 * time.Minute), nats.MaxDeliver(5), nats.BindStream("LEDGER"), nats.DeliverNew()},
		})
	}
	return configs
}

func (w *VectorSyncWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	w.handleEvent(ctx, msg)
	return nil
}

func (w *VectorSyncWorker) handleEvent(ctx context.Context, msg *nats.Msg) {
	var event cdc.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("Failed to parse CDC Event JSON", "error", err)
		msg.Term() // Unrecoverable
		return
	}

	if event.Action == "DELETE" {
		// We could potentially delete from Pinecone, but pinecone upsert logic generally ignores them for now.
		// For now we just ack and skip. Can be added later.
		msg.Ack()
		return
	}

	realmID := ""
	if r, ok := event.Data["realm_id"].(string); ok {
		realmID = r
	}
	erpID := ""
	if e, ok := event.Data["erp_id"].(string); ok {
		erpID = e
	}

	if realmID == "" || erpID == "" {
		// Cannot sync without realm or id
		msg.Ack()
		return
	}

	var text string
	var metadata map[string]interface{}

	switch event.Table {
	case "accounts":
		name, _ := event.Data["name"].(string)
		fqn, ok := event.Data["fully_qualified_name"].(string)
		if ok && fqn != "" {
			text = fqn
		} else {
			text = name
		}
		metadata = map[string]interface{}{
			"name":        name,
			"entity_type": "account",
			"type":        event.Data["account_type"],
		}
	case "vendors":
		text, _ = event.Data["display_name"].(string)
		metadata = map[string]interface{}{
			"name":        text,
			"entity_type": "vendor",
		}
	case "customers":
		text, _ = event.Data["display_name"].(string)
		metadata = map[string]interface{}{
			"name":        text,
			"entity_type": "customer",
		}
	default:
		msg.Ack()
		return
	}

	if text == "" {
		// Nothing to embed
		msg.Ack()
		return
	}

	item := vectorBatchItem{
		realmID:  realmID,
		id:       erpID,
		text:     text,
		metadata: metadata,
		msg:      msg,
	}

	w.logger.Info("VectorWorker queued item for batch", "table", event.Table, "id", erpID)

	select {
	case w.itemChan <- item:
		// buffered for batch
	case <-ctx.Done():
		msg.Nak()
	}
}

func (w *VectorSyncWorker) processBatches(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(vectorTimeout)
	defer ticker.Stop()

	var batch []vectorBatchItem

	flush := func() {
		if len(batch) > 0 {
			w.flushBatch(ctx, batch)
			batch = make([]vectorBatchItem, 0, vectorBatchSize)
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush() // Flush any remaining on shutdown
			return
		case item := <-w.itemChan:
			batch = append(batch, item)
			if len(batch) >= vectorBatchSize {
				flush()
				ticker.Reset(vectorTimeout)
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *VectorSyncWorker) flushBatch(ctx context.Context, batch []vectorBatchItem) {
	if len(batch) == 0 {
		return
	}
	w.logger.Info("VectorWorker flushing batch", "size", len(batch))

	// 1. Embed all texts at once
	texts := make([]string, len(batch))
	for i, item := range batch {
		texts[i] = item.text
	}

	w.logger.Info("VectorWorker embedding batch...")
	embeddings, err := w.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		w.logger.Error("Batch embedding failed", "error", err)
		for _, item := range batch {
			item.msg.Nak()
		}
		return
	}
	w.logger.Info("VectorWorker batch embedded successfully")

	// 2. Group by realmID
	type realmBatch struct {
		vectors []vector.Vector
		msgs    []*nats.Msg
	}
	byRealm := make(map[string]*realmBatch)

	for i, item := range batch {
		if byRealm[item.realmID] == nil {
			byRealm[item.realmID] = &realmBatch{}
		}
		byRealm[item.realmID].vectors = append(byRealm[item.realmID].vectors, vector.Vector{
			ID:       item.id,
			Values:   embeddings[i],
			Metadata: item.metadata,
		})
		byRealm[item.realmID].msgs = append(byRealm[item.realmID].msgs, item.msg)
	}

	// 3. Upsert per realm
	for realmID, rb := range byRealm {
		if err := w.vectorClient.UpsertVectors(ctx, realmID, rb.vectors); err != nil {
			w.logger.Error("Batch upsert failed", "realm_id", realmID, "error", err)
			for _, msg := range rb.msgs {
				msg.Nak()
			}
		} else {
			for _, msg := range rb.msgs {
				msg.Ack()
			}
		}
	}
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.Pinecone == nil || deps.Embedder == nil {
			return nil, nil
		}
		return NewVectorSyncWorker(deps.Logger, deps.Store, deps.Pinecone, deps.Embedder, deps.Queue, deps.Config)
	})
}
