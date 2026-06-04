package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/services/ai"
)

// VectorHydrator is a background worker that:
//  1. Reads un-embedded rows from ase.vector_memory (pending hydration).
//  2. Scans toro_core.agent_memory_rules for new rules not yet registered.
//  3. Scans fignode.staging_transactions for high-confidence resolved rows.
//  4. Generates embeddings via OpenAI and upserts them back into the store.
//
// All tuning parameters (interval, batch size, min confidence, model) are
// read from VectorMemoryConfig at each tick, making them hot-reloadable.
type VectorHydrator struct {
	store     *VectorStore
	pool      *pgxpool.Pool
	llmClient *ai.LLMClient
	logger    *slog.Logger
}

// NewVectorHydrator creates a new VectorHydrator.
func NewVectorHydrator(
	store *VectorStore,
	pool *pgxpool.Pool,
	llmClient *ai.LLMClient,
	logger *slog.Logger,
) *VectorHydrator {
	return &VectorHydrator{
		store:     store,
		pool:      pool,
		llmClient: llmClient,
		logger:    logger,
	}
}

// Run starts the hydrator loop. It blocks until ctx is cancelled.
// Designed to be called as a goroutine:
//
//	go hydrator.Run(ctx)
func (h *VectorHydrator) Run(ctx context.Context) {
	h.logger.Info("ase vector hydrator started")
	for {
		cfg := h.activeCfg()
		if !cfg.Enabled {
			// Vector memory is disabled — sleep and check again.
			select {
			case <-ctx.Done():
				h.logger.Info("ase vector hydrator stopped")
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}

		interval := time.Duration(cfg.HydratorIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}

		if err := h.tick(ctx, cfg); err != nil {
			h.logger.Error("ase vector hydrator tick error", "error", err)
		}

		select {
		case <-ctx.Done():
			h.logger.Info("ase vector hydrator stopped")
			return
		case <-time.After(interval):
		}
	}
}

// tick performs one full hydration pass:
//  1. Registers new source rows (memory rules + resolved transactions).
//  2. Embeds pending rows.
//  3. Creates the ScaNN index lazily (once data exists).
func (h *VectorHydrator) tick(ctx context.Context, cfg VectorMemoryConfig) error {
	// Phase A: register new memory rules that aren't in ase.vector_memory yet.
	if err := h.registerMemoryRules(ctx); err != nil {
		h.logger.Warn("hydrator: failed to register memory rules", "error", err)
	}

	// Phase B: register new high-confidence resolved transactions.
	if err := h.registerResolvedTx(ctx, cfg.HydratorMinConfidence); err != nil {
		h.logger.Warn("hydrator: failed to register resolved transactions", "error", err)
	}

	// Phase C: embed pending rows.
	batchSize := cfg.HydratorBatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	if err := h.embedPending(ctx, cfg, batchSize); err != nil {
		return err
	}

	// Phase D: lazily create the ScaNN index once the table is non-empty.
	// EnsureScaNNIndex is a no-op if the index already exists or the table
	// is still empty (AlloyDB Omni requires at least one row).
	if err := h.store.EnsureScaNNIndex(ctx, "", ""); err != nil {
		h.logger.Warn("hydrator: EnsureScaNNIndex failed", "error", err)
	}

	return nil
}


// registerMemoryRules inserts a pending row into ase.vector_memory for each
// toro_core.agent_memory_rules row that isn't already registered.
func (h *VectorHydrator) registerMemoryRules(ctx context.Context) error {
	rows, err := h.pool.Query(ctx, `
		SELECT amr.id, amr.realm_id, amr.instruction
		FROM toro_core.agent_memory_rules amr
		WHERE NOT EXISTS (
			SELECT 1 FROM ase.vector_memory vm
			WHERE vm.realm_id     = amr.realm_id
			  AND vm.source_type  = 'memory_rule'
			  AND vm.source_row_id = amr.id
		)
		LIMIT 200`)
	if err != nil {
		return fmt.Errorf("register memory rules query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rowID uuid.UUID
		var realmID, instruction string
		if err := rows.Scan(&rowID, &realmID, &instruction); err != nil {
			return fmt.Errorf("register memory rules scan: %w", err)
		}
		meta := map[string]any{"source": "agent_memory_rules"}
		if err := h.store.Upsert(ctx, realmID, VectorSourceMemoryRule, instruction, rowID, nil, meta); err != nil {
			h.logger.Warn("hydrator: failed to register memory rule", "row_id", rowID, "error", err)
		}
	}
	return rows.Err()
}

// registerResolvedTx inserts pending rows for high-confidence staging_transactions
// that have been resolved (READY_FOR_SYNC or COLLAPSED) but not yet embedded.
func (h *VectorHydrator) registerResolvedTx(ctx context.Context, minConfidence float64) error {
	rows, err := h.pool.Query(ctx, `
		SELECT
			st.id,
			ss.realm_id,
			st.raw_description,
			st.confidence_score,
			st.macro_class,
			st.account_type
		FROM fignode.staging_transactions st
		JOIN fignode.staging_sessions ss ON ss.id = st.session_id
		WHERE st.status IN ('READY_FOR_SYNC', 'COLLAPSED')
		  AND st.confidence_score >= $1
		  AND st.raw_description IS NOT NULL
		  AND ss.realm_id IS NOT NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM ase.vector_memory vm
			  WHERE vm.realm_id      = ss.realm_id
			    AND vm.source_type   = 'resolved_tx'
			    AND vm.source_row_id = st.id
		  )
		LIMIT 200`, minConfidence)
	if err != nil {
		return fmt.Errorf("register resolved tx query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rowID uuid.UUID
		var realmID, rawDesc string
		var confidence float64
		var macroClass, accountType *string
		if err := rows.Scan(&rowID, &realmID, &rawDesc, &confidence, &macroClass, &accountType); err != nil {
			return fmt.Errorf("register resolved tx scan: %w", err)
		}
		meta := map[string]any{
			"confidence":   confidence,
			"macro_class":  derefStr(macroClass),
			"account_type": derefStr(accountType),
		}
		if err := h.store.Upsert(ctx, realmID, VectorSourceResolvedTx, rawDesc, rowID, nil, meta); err != nil {
			h.logger.Warn("hydrator: failed to register resolved tx", "row_id", rowID, "error", err)
		}
	}
	return rows.Err()
}

// embedPending fetches pending rows and generates embeddings for each.
func (h *VectorHydrator) embedPending(ctx context.Context, cfg VectorMemoryConfig, batchSize int) error {
	pending, err := h.store.PendingRows(ctx, batchSize)
	if err != nil {
		return fmt.Errorf("embed pending: fetch: %w", err)
	}

	model := cfg.OpenAIEmbeddingModel
	if model == "" {
		model = "text-embedding-3-small"
	}

	embedded, skipped := 0, 0
	for _, row := range pending {
		if row.RawText == "" {
			skipped++
			continue
		}
		vec, err := h.llmClient.GenerateEmbedding(ctx, model, row.RawText)
		if err != nil {
			h.logger.Warn("hydrator: embedding generation failed",
				"id", row.ID,
				"source_type", row.SourceType,
				"error", err,
			)
			skipped++
			continue
		}
		if err := h.store.UpdateEmbedding(ctx, row.ID, vec); err != nil {
			h.logger.Warn("hydrator: update embedding failed", "id", row.ID, "error", err)
			skipped++
			continue
		}
		embedded++
	}

	if embedded > 0 || skipped > 0 {
		h.logger.Info("ase vector hydrator tick complete",
			"embedded", embedded,
			"skipped", skipped,
		)
	}
	return nil
}

// activeCfg returns the VectorMemoryConfig from the default ASE config.
func (h *VectorHydrator) activeCfg() VectorMemoryConfig {
	cfg := GetConfig("", "")
	if cfg == nil {
		return VectorMemoryConfig{
			HydratorIntervalSeconds: 30,
			HydratorBatchSize:       50,
			OpenAIEmbeddingModel:    "text-embedding-3-small",
		}
	}
	return cfg.HyperParameters.VectorMemory
}

// metaToJSON is a helper for converting metadata maps to JSON bytes.
// Used internally; exported for testing.
func metaToJSON(m map[string]any) ([]byte, error) {
	return json.Marshal(m)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
