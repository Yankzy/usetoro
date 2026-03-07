package ai

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infrastructure/vector"
	"github.com/Yankzy/usetoro/internal/store"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	batchSize   = 100 // OpenAI recommended batch size
	maxParallel = 5   // Concurrent embedding requests
)

// VectorSyncWorker synchronizes database entities to Pinecone
type VectorSyncWorker struct {
	logger       *slog.Logger
	store        *store.Store
	vectorClient *vector.PineconeClient
	embedder     *vector.Embedder
	interval     time.Duration
}

// NewVectorSyncWorker creates a new vector sync worker
func NewVectorSyncWorker(logger *slog.Logger, s *store.Store, vc *vector.PineconeClient, e *vector.Embedder, interval time.Duration) *VectorSyncWorker {
	return &VectorSyncWorker{
		logger:       logger,
		store:        s,
		vectorClient: vc,
		embedder:     e,
		interval:     interval,
	}
}

// Start runs the background worker
func (w *VectorSyncWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("🚀 VectorSyncWorker started", "interval", w.interval)

	// Initial sync on startup
	if err := w.Sync(ctx); err != nil {
		w.logger.Error("Initial vector sync failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Stopping VectorSyncWorker")
			return
		case <-ticker.C:
			if err := w.Sync(ctx); err != nil {
				w.logger.Error("Vector sync failed", "error", err)
			}
		}
	}
}

// Sync performs a single synchronization cycle for all realms
func (w *VectorSyncWorker) Sync(ctx context.Context) error {
	// 1. Get all active connections to sync
	conns, err := w.store.Queries.GetAllActiveConnections(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch active connections: %w", err)
	}

	for _, conn := range conns {
		if err := w.SyncRealm(ctx, conn.RealmID); err != nil {
			w.logger.Error("Failed to sync realm", "realm_id", conn.RealmID, "error", err)
		}
	}

	return nil
}

// SyncRealm synchronizes vectors for a specific realm using delta sync
func (w *VectorSyncWorker) SyncRealm(ctx context.Context, realmID string) error {
	w.logger.Debug("Syncing vectors for realm", "realm_id", realmID)

	state, err := w.store.Queries.GetVectorSyncState(ctx, realmID)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("failed to fetch sync state: %w", err)
	}

	// 1. Sync Accounts
	if err := w.syncAccounts(ctx, realmID, state.LastCoaSync); err != nil {
		return fmt.Errorf("account sync failed: %w", err)
	}

	// 2. Sync Vendors
	if err := w.syncVendors(ctx, realmID, state.LastVendorSync); err != nil {
		return fmt.Errorf("vendor sync failed: %w", err)
	}

	// 3. Sync Customers
	if err := w.syncCustomers(ctx, realmID, state.LastCustomerSync); err != nil {
		return fmt.Errorf("customer sync failed: %w", err)
	}

	return nil
}

func (w *VectorSyncWorker) syncAccounts(ctx context.Context, realmID string, since pgtype.Timestamptz) error {
	w.logger.Debug("Syncing modified accounts", "realm_id", realmID, "since", since.Time)
	accounts, err := w.store.Queries.GetAccountsUpdatedSince(ctx, database.GetAccountsUpdatedSinceParams{
		RealmID:   realmID,
		UpdatedAt: since,
	})
	if err != nil {
		return err
	}

	if len(accounts) == 0 {
		return nil
	}

	items := make([]batchItem, len(accounts))
	var latestUpdate time.Time
	for i, acc := range accounts {
		text := acc.Name
		if acc.FullyQualifiedName.Valid {
			text = acc.FullyQualifiedName.String
		}
		items[i] = batchItem{
			id:   acc.ErpID,
			text: text,
			metadata: map[string]interface{}{
				"name":        acc.Name,
				"entity_type": "account",
				"type":        acc.AccountType,
			},
		}
		if acc.UpdatedAt.Valid && acc.UpdatedAt.Time.After(latestUpdate) {
			latestUpdate = acc.UpdatedAt.Time
		}
	}

	if err := w.processBatches(ctx, realmID, items); err != nil {
		return err
	}

	// Update sync state
	return w.store.Queries.UpsertVectorSyncState(ctx, database.UpsertVectorSyncStateParams{
		RealmID:        realmID,
		LastCoaSync:    pgtype.Timestamptz{Time: latestUpdate, Valid: true},
		CoaVectorCount: pgtype.Int4{Int32: int32(len(items)), Valid: true},
	})
}

func (w *VectorSyncWorker) syncVendors(ctx context.Context, realmID string, since pgtype.Timestamptz) error {
	w.logger.Debug("Syncing modified vendors", "realm_id", realmID, "since", since.Time)
	vendors, err := w.store.Queries.GetVendorsUpdatedSince(ctx, database.GetVendorsUpdatedSinceParams{
		RealmID:   realmID,
		UpdatedAt: since,
	})
	if err != nil {
		return err
	}

	if len(vendors) == 0 {
		return nil
	}

	items := make([]batchItem, len(vendors))
	var latestUpdate time.Time
	for i, v := range vendors {
		items[i] = batchItem{
			id:   v.ErpID,
			text: v.DisplayName,
			metadata: map[string]interface{}{
				"name":        v.DisplayName,
				"entity_type": "vendor",
			},
		}
		if v.UpdatedAt.Valid && v.UpdatedAt.Time.After(latestUpdate) {
			latestUpdate = v.UpdatedAt.Time
		}
	}

	if err := w.processBatches(ctx, realmID, items); err != nil {
		return err
	}

	return w.store.Queries.UpdateVendorVectorSync(ctx, database.UpdateVendorVectorSyncParams{
		RealmID:           realmID,
		LastVendorSync:    pgtype.Timestamptz{Time: latestUpdate, Valid: true},
		VendorVectorCount: pgtype.Int4{Int32: int32(len(items)), Valid: true},
	})
}

func (w *VectorSyncWorker) syncCustomers(ctx context.Context, realmID string, since pgtype.Timestamptz) error {
	w.logger.Debug("Syncing modified customers", "realm_id", realmID, "since", since.Time)
	customers, err := w.store.Queries.GetCustomersUpdatedSince(ctx, database.GetCustomersUpdatedSinceParams{
		RealmID:   realmID,
		UpdatedAt: since,
	})
	if err != nil {
		return err
	}

	if len(customers) == 0 {
		return nil
	}

	items := make([]batchItem, len(customers))
	var latestUpdate time.Time
	for i, c := range customers {
		items[i] = batchItem{
			id:   c.ErpID,
			text: c.DisplayName,
			metadata: map[string]interface{}{
				"name":        c.DisplayName,
				"entity_type": "customer",
			},
		}
		if c.UpdatedAt.Valid && c.UpdatedAt.Time.After(latestUpdate) {
			latestUpdate = c.UpdatedAt.Time
		}
	}

	if err := w.processBatches(ctx, realmID, items); err != nil {
		return err
	}

	return w.store.Queries.UpdateCustomerVectorSync(ctx, database.UpdateCustomerVectorSyncParams{
		RealmID:             realmID,
		LastCustomerSync:    pgtype.Timestamptz{Time: latestUpdate, Valid: true},
		CustomerVectorCount: pgtype.Int4{Int32: int32(len(items)), Valid: true},
	})
}

type batchItem struct {
	id       string
	text     string
	metadata map[string]interface{}
}

func (w *VectorSyncWorker) processBatches(ctx context.Context, realmID string, items []batchItem) error {
	if len(items) == 0 {
		return nil
	}

	// 1. Split into chunks
	var chunks [][]batchItem
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}

	// 2. Process chunks concurrently
	results := make(chan error, len(chunks))
	semaphore := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for _, chunk := range chunks {
		wg.Add(1)
		go func(items []batchItem) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Extract texts for bulk embedding
			texts := make([]string, len(items))
			for i, item := range items {
				texts[i] = item.text
			}

			// Bulk embed
			embeddings, err := w.embedder.EmbedBatch(ctx, texts)
			if err != nil {
				results <- fmt.Errorf("batch embedding failed: %w", err)
				return
			}

			// Map back to vectors
			vectors := make([]vector.Vector, len(items))
			for i, emb := range embeddings {
				vectors[i] = vector.Vector{
					ID:       items[i].id,
					Values:   emb,
					Metadata: items[i].metadata,
				}
			}

			// Bulk upsert
			if err := w.vectorClient.UpsertVectors(ctx, realmID, vectors); err != nil {
				results <- fmt.Errorf("batch upsert failed: %w", err)
				return
			}

			results <- nil
		}(chunk)
	}

	// 3. Wait and check results
	go func() {
		wg.Wait()
		close(results)
	}()

	for err := range results {
		if err != nil {
			return err // Stop on first error for now
		}
	}

	return nil
}
