package connectors

import (
	"context"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/store"
)

// CDCWorker performs periodic Change Data Capture syncs for all QBO connections.
type CDCWorker struct {
	logger    *slog.Logger
	connector *QBOConnector
	store     *store.Store
	interval  time.Duration
}

// NewCDCWorker creates a new CDC worker with the specified polling interval.
func NewCDCWorker(logger *slog.Logger, connector *QBOConnector, store *store.Store, interval time.Duration) *CDCWorker {
	return &CDCWorker{
		logger:    logger,
		connector: connector,
		store:     store,
		interval:  interval,
	}
}

// Start begins the CDC polling loop. It runs until the context is cancelled.
func (w *CDCWorker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("🔄 CDC Worker started", "interval", w.interval)

	// Run initial sync immediately
	if err := w.runSyncCycle(ctx); err != nil {
		w.logger.Error("Initial CDC sync failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("🛑 CDC Worker shutting down")
			return nil
		case <-ticker.C:
			if err := w.runSyncCycle(ctx); err != nil {
				w.logger.Error("CDC sync cycle failed", "error", err)
			}
		}
	}
}

// runSyncCycle fetches all active QBO connections and runs CDC for each.
func (w *CDCWorker) runSyncCycle(ctx context.Context) error {
	w.logger.Info("🔄 Starting CDC sync cycle")
	start := time.Now()

	// Get all active QBO connections
	connections, err := w.store.Queries.GetAllActiveConnections(ctx)
	if err != nil {
		return err
	}

	if len(connections) == 0 {
		w.logger.Info("⏭️  No QBO connections to sync")
		return nil
	}

	var successCount, errorCount int

	// Sync each connection
	for _, conn := range connections {
		realmID := conn.RealmID
		lastSync := conn.LastSyncTimestamp.Time

		// Ensure we don't go beyond QBO's 30-day limit
		maxLookback := time.Now().Add(-30 * 24 * time.Hour)
		if lastSync.Before(maxLookback) {
			w.logger.Warn("Last sync exceeds 30-day lookback limit, using max lookback",
				"realm_id", realmID,
				"last_sync", lastSync,
				"max_lookback", maxLookback,
			)
			lastSync = maxLookback
		}

		if err := w.connector.SyncCDC(ctx, realmID, lastSync); err != nil {
			w.logger.Error("CDC sync failed for connection",
				"realm_id", realmID,
				"error", err,
			)
			errorCount++
		} else {
			successCount++
		}
	}

	duration := time.Since(start)
	w.logger.Info("✅ CDC sync cycle completed",
		"total_connections", len(connections),
		"success", successCount,
		"errors", errorCount,
		"duration", duration,
	)

	return nil
}
