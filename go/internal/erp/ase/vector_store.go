package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/services/ai"
)

// VectorSourceType identifies where a vector memory row was sourced from.
type VectorSourceType string

const (
	// VectorSourceMemoryRule is sourced from toro_core.agent_memory_rules.
	VectorSourceMemoryRule VectorSourceType = "memory_rule"
	// VectorSourceResolvedTx is sourced from a high-confidence fignode.staging_transaction.
	VectorSourceResolvedTx VectorSourceType = "resolved_tx"
)

// VectorMemoryRow is a retrieved result from the ase.vector_memory table.
type VectorMemoryRow struct {
	RawText    string          `json:"raw_text"`
	Metadata   json.RawMessage `json:"metadata"`
	Similarity float64         `json:"similarity"`
}

// VectorStore handles semantic retrieval against the ase.vector_memory table.
// It is safe for concurrent use.
type VectorStore struct {
	pool      *pgxpool.Pool
	llmClient *ai.LLMClient
	logger    *slog.Logger
}

// NewVectorStore creates a new VectorStore.
// llmClient may be nil; in that case, embedding generation will return an error.
func NewVectorStore(pool *pgxpool.Pool, llmClient *ai.LLMClient, logger *slog.Logger) *VectorStore {
	return &VectorStore{
		pool:      pool,
		llmClient: llmClient,
		logger:    logger,
	}
}

// GenerateEmbedding calls the OpenAI Embeddings API using the model specified
// in the ase.yml hyper_parameters.vector_memory block.
// The tenantID / realmID pair is used to look up the active config.
func (vs *VectorStore) GenerateEmbedding(ctx context.Context, tenantID, realmID, text string) ([]float64, error) {
	if vs.llmClient == nil {
		return nil, fmt.Errorf("vector store: llmClient not configured")
	}
	cfg := vs.vectorCfg(tenantID, realmID)
	if cfg.EmbeddingProvider != "openai" {
		return nil, fmt.Errorf("vector store: unsupported embedding provider %q (only 'openai' is supported)", cfg.EmbeddingProvider)
	}
	model := cfg.OpenAIEmbeddingModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	return vs.llmClient.GenerateEmbedding(ctx, model, text)
}

// Upsert inserts or updates a vector memory row.
// If embedding is nil the row is registered as pending hydration.
func (vs *VectorStore) Upsert(
	ctx context.Context,
	realmID string,
	sourceType VectorSourceType,
	rawText string,
	sourceRowID uuid.UUID,
	embedding []float64,
	metadata map[string]any,
) error {
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("vector store upsert: marshal metadata: %w", err)
	}

	if embedding != nil {
		// Full upsert with embedding.
		_, err = vs.pool.Exec(ctx, `
			INSERT INTO ase.vector_memory
				(realm_id, source_type, raw_text, embedding, source_row_id, metadata, embedded_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (realm_id, source_type, source_row_id)
			DO UPDATE SET
				raw_text    = EXCLUDED.raw_text,
				embedding   = EXCLUDED.embedding,
				metadata    = EXCLUDED.metadata,
				embedded_at = NOW(),
				updated_at  = NOW()`,
			realmID, string(sourceType), rawText,
			floatsToVectorLiteral(embedding),
			sourceRowID, metaBytes,
		)
	} else {
		// Register as pending (hydrator will fill embedding later).
		_, err = vs.pool.Exec(ctx, `
			INSERT INTO ase.vector_memory (realm_id, source_type, raw_text, source_row_id, metadata)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (realm_id, source_type, source_row_id) DO NOTHING`,
			realmID, string(sourceType), rawText, sourceRowID, metaBytes,
		)
	}
	if err != nil {
		return fmt.Errorf("vector store upsert: %w", err)
	}
	return nil
}

// UpdateEmbedding fills in the embedding for an already-registered row.
// Called by the VectorHydrator after generating the embedding.
func (vs *VectorStore) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float64) error {
	_, err := vs.pool.Exec(ctx, `
		UPDATE ase.vector_memory
		SET embedding = $2, embedded_at = NOW(), updated_at = NOW()
		WHERE id = $1`,
		id, floatsToVectorLiteral(embedding),
	)
	if err != nil {
		return fmt.Errorf("vector store update embedding: %w", err)
	}
	return nil
}

// Search executes a Bitmap-Assisted ScaNN ANN query:
//  1. The planner uses the B-Tree index on (realm_id) to pre-filter rows,
//     building a tight in-memory bitmap for only that tenant's vectors.
//  2. The ScaNN index then performs ANN cosine search over the bitmap.
//
// topK and the embedding model are read from the ase.yml config for the
// given tenant / realm pair, making retrieval fully hot-reloadable.
func (vs *VectorStore) Search(
	ctx context.Context,
	tenantID, realmID string,
	queryEmbedding []float64,
) ([]VectorMemoryRow, error) {
	cfg := vs.vectorCfg(tenantID, realmID)
	topK := cfg.RetrievalTopK
	if topK <= 0 {
		topK = 5
	}

	rows, err := vs.pool.Query(ctx, `
		SELECT raw_text, metadata, 1 - (embedding <=> $2) AS similarity
		FROM ase.vector_memory
		WHERE realm_id = $1
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $2
		LIMIT $3`,
		realmID, floatsToVectorLiteral(queryEmbedding), topK,
	)
	if err != nil {
		return nil, fmt.Errorf("vector store search: %w", err)
	}
	defer rows.Close()

	var results []VectorMemoryRow
	for rows.Next() {
		var r VectorMemoryRow
		if err := rows.Scan(&r.RawText, &r.Metadata, &r.Similarity); err != nil {
			return nil, fmt.Errorf("vector store search scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// PendingRows returns rows that have no embedding yet, up to limit rows.
// Used by the VectorHydrator each tick.
type PendingVectorRow struct {
	ID          uuid.UUID
	RealmID     string
	SourceType  VectorSourceType
	RawText     string
	SourceRowID uuid.UUID
}

func (vs *VectorStore) PendingRows(ctx context.Context, limit int) ([]PendingVectorRow, error) {
	rows, err := vs.pool.Query(ctx, `
		SELECT id, realm_id, source_type, raw_text, source_row_id
		FROM ase.vector_memory
		WHERE embedding IS NULL
		ORDER BY created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("vector store pending rows: %w", err)
	}
	defer rows.Close()

	var results []PendingVectorRow
	for rows.Next() {
		var r PendingVectorRow
		var sourceType string
		if err := rows.Scan(&r.ID, &r.RealmID, &sourceType, &r.RawText, &r.SourceRowID); err != nil {
			return nil, fmt.Errorf("vector store pending rows scan: %w", err)
		}
		r.SourceType = VectorSourceType(sourceType)
		results = append(results, r)
	}
	return results, rows.Err()
}

// vectorCfg returns the VectorMemoryConfig for the given tenant/realm,
// with a safe zero-value fallback so callers never need to nil-check.
func (vs *VectorStore) vectorCfg(tenantID, realmID string) VectorMemoryConfig {
	cfg := GetConfig(tenantID, realmID)
	if cfg == nil {
		return VectorMemoryConfig{
			OpenAIEmbeddingModel: "text-embedding-3-small",
			RetrievalTopK:        5,
		}
	}
	return cfg.HyperParameters.VectorMemory
}

// EnsureScaNNIndex creates the ScaNN ANN index if:
//   - The table has at least one embedded row (AlloyDB Omni requirement)
//   - The index does not already exist
//
// This is called by the VectorHydrator after each successful embed batch.
// num_leaves is read from VectorMemoryConfig at call time so tuning changes
// in ase.yml take effect on the next index creation (requires DROP + recreate).
func (vs *VectorStore) EnsureScaNNIndex(ctx context.Context, tenantID, realmID string) error {
	// 1. Check whether the index already exists.
	var exists bool
	err := vs.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'ase'
			  AND tablename  = 'vector_memory'
			  AND indexname  = 'idx_ase_vector_memory_scann'
		)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ensure scann index: check existence: %w", err)
	}
	if exists {
		return nil
	}

	// 2. Confirm the table is non-empty (AlloyDB Omni requirement).
	var count int64
	err = vs.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM ase.vector_memory WHERE embedding IS NOT NULL
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("ensure scann index: count check: %w", err)
	}
	if count == 0 {
		// Nothing to index yet — the hydrator will retry next tick.
		return nil
	}

	// 3. Read num_leaves from the hot-reloadable config.
	cfg := vs.vectorCfg(tenantID, realmID)
	numLeaves := cfg.ScaNNNumLeaves
	if numLeaves <= 0 {
		numLeaves = 10
	}

	// 4. Create the index. This is a DDL statement, so it cannot be run inside
	// a regular transaction block (hence pool.Exec is used directly).
	vs.logger.Info("ase vector store: creating ScaNN index",
		"num_leaves", numLeaves,
		"embedded_rows", count,
	)
	_, err = vs.pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_scann
		ON ase.vector_memory USING scann (embedding cosine)
		WITH (num_leaves = %d)
		WHERE embedding IS NOT NULL`, numLeaves))
	if err != nil {
		return fmt.Errorf("ensure scann index: create: %w", err)
	}
	vs.logger.Info("ase vector store: ScaNN index created successfully", "num_leaves", numLeaves)
	return nil
}

// floatsToVectorLiteral converts a []float64 into the pgvector wire format string

// that pgx can send as a text parameter to the vector column.
// Format: "[0.1,0.2,...,0.n]"
func floatsToVectorLiteral(v []float64) string {
	if len(v) == 0 {
		return "[]"
	}
	b := make([]byte, 0, len(v)*10+2)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = fmt.Appendf(b, "%g", f)
	}
	b = append(b, ']')
	return string(b)
}
