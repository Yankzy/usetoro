package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/infrastructure/vector"
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
}

// NewVectorSyncWorker creates a new vector sync worker
func NewVectorSyncWorker(logger *slog.Logger, s *store.Store, vc *vector.PineconeClient, e *vector.Embedder, nc *nats.Conn) (*VectorSyncWorker, error) {
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
	}, nil
}

// Start runs the background worker
func (w *VectorSyncWorker) Start(ctx context.Context) error {
	w.logger.Info("🚀 VectorSyncWorker CDC event consumer started")

	// Start the batch processor
	var wg sync.WaitGroup
	wg.Add(1)
	go w.processBatches(ctx, &wg)

	subjects := []string{
		"ledger.shadow_erp_accounts.*",
		"ledger.shadow_erp_vendors.*",
		"ledger.shadow_erp_customers.*",
	}

	var subs []*nats.Subscription
	for _, subject := range subjects {
		sub, err := w.js.QueueSubscribe(subject, "toro-vector-sync-workers", func(msg *nats.Msg) {
			w.handleEvent(ctx, msg)
		}, nats.ManualAck())

		if err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
		}
		w.logger.Info("🎧 VectorSyncWorker subscribed", "subject", subject)
		subs = append(subs, sub)
	}

	<-ctx.Done()
	w.logger.Info("🛑 VectorSyncWorker shutting down")

	for _, sub := range subs {
		sub.Unsubscribe()
	}

	wg.Wait()
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
	case "shadow_erp_accounts":
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
	case "shadow_erp_vendors":
		text, _ = event.Data["display_name"].(string)
		metadata = map[string]interface{}{
			"name":        text,
			"entity_type": "vendor",
		}
	case "shadow_erp_customers":
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
	// 1. Embed all texts at once
	texts := make([]string, len(batch))
	for i, item := range batch {
		texts[i] = item.text
	}

	embeddings, err := w.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		w.logger.Error("Batch embedding failed", "error", err)
		for _, item := range batch {
			item.msg.Nak()
		}
		return
	}

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
