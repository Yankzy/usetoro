package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/infra/vector"
)

// VectorSourceType identifies where a vector memory row was sourced from.
type VectorSourceType string

const (
	// VectorSourceMemoryRule is sourced from toro_core.agent_memory_rules.
	VectorSourceMemoryRule VectorSourceType = "memory_rule"
	// VectorSourceResolvedTx is sourced from a high-confidence fignode.staging_transaction.
	VectorSourceResolvedTx VectorSourceType = "resolved_tx"
)

// VectorMemoryRow is a retrieved result from the toro_core.ase_vector_memory table.
type VectorMemoryRow struct {
	Namespace  string          `json:"namespace"`
	RawText    string          `json:"raw_text"`
	Metadata   json.RawMessage `json:"metadata"`
	Similarity float64         `json:"similarity"`
}

// VectorStore handles semantic retrieval against the toro_core.ase_vector_memory table.
// It is safe for concurrent use.
type VectorStore struct {
	pool     *pgxpool.Pool
	embedder *vector.Embedder
	logger   *slog.Logger
}

// NewVectorStore creates a new VectorStore.
// embedder may be nil; in that case, embedding generation will return an error.
func NewVectorStore(pool *pgxpool.Pool, embedder *vector.Embedder, logger *slog.Logger) *VectorStore {
	return &VectorStore{
		pool:     pool,
		embedder: embedder,
		logger:   logger,
	}
}

// GenerateEmbedding calls the OpenAI Embeddings API using the model specified
// in the hyper_parameters.vector_memory configuration.
// The tenantID / realmID pair is used to look up the active config.
func (vs *VectorStore) GenerateEmbedding(ctx context.Context, tenantID, realmID, text string) ([]float32, error) {
	if vs.embedder == nil {
		return nil, fmt.Errorf("vector store: embedder not configured")
	}
	cfg := vs.VectorCfg()
	if cfg.EmbeddingProvider != "openai" {
		return nil, fmt.Errorf("vector store: unsupported embedding provider %q (only 'openai' is supported)", cfg.EmbeddingProvider)
	}
	return vs.embedder.Embed(ctx, text)
}

// Upsert inserts or updates a vector memory row.
// If namespace is empty, it defaults to "general".
// If embedding is nil the row is registered as pending hydration.
func (vs *VectorStore) Upsert(
	ctx context.Context,
	realmID string,
	namespace string,
	sourceType VectorSourceType,
	rawText string,
	sourceRowID uuid.UUID,
	embedding []float32,
	metadata map[string]any,
) error {
	if namespace == "" {
		namespace = "general"
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("vector store upsert: marshal metadata: %w", err)
	}

	if embedding != nil {
		// Full upsert with embedding.
		_, err = vs.pool.Exec(ctx, `
			INSERT INTO toro_core.ase_vector_memory
				(realm_id, namespace, source_type, raw_text, embedding, source_row_id, metadata, embedded_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (realm_id, namespace, source_type, source_row_id)
			DO UPDATE SET
				raw_text    = EXCLUDED.raw_text,
				embedding   = EXCLUDED.embedding,
				metadata    = EXCLUDED.metadata,
				embedded_at = NOW(),
				updated_at  = NOW()`,
			realmID, namespace, string(sourceType), rawText,
			floatsToVectorLiteral(embedding),
			sourceRowID, metaBytes,
		)
	} else {
		// Register as pending (hydrator will fill embedding later).
		_, err = vs.pool.Exec(ctx, `
			INSERT INTO toro_core.ase_vector_memory (realm_id, namespace, source_type, raw_text, source_row_id, metadata)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (realm_id, namespace, source_type, source_row_id) DO NOTHING`,
			realmID, namespace, string(sourceType), rawText, sourceRowID, metaBytes,
		)
	}
	if err != nil {
		return fmt.Errorf("vector store upsert: %w", err)
	}
	return nil
}

// UpdateEmbedding fills in the embedding for an already-registered row.
// Called by the VectorHydrator after generating the embedding.
func (vs *VectorStore) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	_, err := vs.pool.Exec(ctx, `
		UPDATE toro_core.ase_vector_memory
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
//  1. The planner uses the B-Tree index on (realm_id, namespace) to pre-filter rows,
//     building a tight in-memory bitmap for only that tenant/namespace vectors.
//  2. The ScaNN index then performs ANN cosine search over the bitmap.
//
// If namespace is empty or "*", it searches across all namespaces for the tenant.
// topK and the embedding model are read from the dynamically loaded config for the
// given tenant / realm pair, making retrieval fully hot-reloadable.
func (vs *VectorStore) Search(
	ctx context.Context,
	tenantID, realmID, namespace string,
	queryEmbedding []float32,
) ([]VectorMemoryRow, error) {
	cfg := vs.VectorCfg()
	topK := cfg.RetrievalTopK
	if topK <= 0 {
		topK = 5
	}

	var rowsExec string
	var args []any

	if namespace != "" && namespace != "*" {
		rowsExec = `
			SELECT namespace, raw_text, metadata, 1 - (embedding <=> $2::vector) AS similarity
			FROM toro_core.ase_vector_memory
			WHERE realm_id = $1
			  AND namespace = $4
			  AND embedding IS NOT NULL
			ORDER BY embedding <=> $2::vector
			LIMIT $3`
		args = []any{realmID, floatsToVectorLiteral(queryEmbedding), topK, namespace}
	} else {
		rowsExec = `
			SELECT namespace, raw_text, metadata, 1 - (embedding <=> $2::vector) AS similarity
			FROM toro_core.ase_vector_memory
			WHERE realm_id = $1
			  AND embedding IS NOT NULL
			ORDER BY embedding <=> $2::vector
			LIMIT $3`
		args = []any{realmID, floatsToVectorLiteral(queryEmbedding), topK}
	}

	rows, err := vs.pool.Query(ctx, rowsExec, args...)
	if err != nil {
		return nil, fmt.Errorf("vector store search: %w", err)
	}
	defer rows.Close()

	var results []VectorMemoryRow
	for rows.Next() {
		var r VectorMemoryRow
		if err := rows.Scan(&r.Namespace, &r.RawText, &r.Metadata, &r.Similarity); err != nil {
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
	Namespace   string
	SourceType  VectorSourceType
	RawText     string
	SourceRowID uuid.UUID
}

func (vs *VectorStore) PendingRows(ctx context.Context, limit int) ([]PendingVectorRow, error) {
	rows, err := vs.pool.Query(ctx, `
		SELECT id, realm_id, namespace, source_type, raw_text, source_row_id
		FROM toro_core.ase_vector_memory
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
		if err := rows.Scan(&r.ID, &r.RealmID, &r.Namespace, &sourceType, &r.RawText, &r.SourceRowID); err != nil {
			return nil, fmt.Errorf("vector store pending rows scan: %w", err)
		}
		r.SourceType = VectorSourceType(sourceType)
		results = append(results, r)
	}
	return results, rows.Err()
}

// vectorCfg returns the VectorMemoryConfig from the global system config.
func (vs *VectorStore) VectorCfg() VectorMemoryConfig {
	return GetSystemVectorConfig()
}

// EnsureScaNNIndex creates the ScaNN ANN index if:
//   - The table has more embedded rows than num_leaves (AlloyDB Omni requirement)
//   - The index does not already exist
//
// This is called by the VectorHydrator after each successful embed batch.
// num_leaves is read from VectorMemoryConfig at call time so tuning changes
// in the configuration take effect on the next index creation (requires DROP + recreate).
func (vs *VectorStore) EnsureScaNNIndex(ctx context.Context, tenantID, realmID string) error {
	// 1. Check whether the index already exists.
	var exists bool
	err := vs.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'toro_core'
			  AND tablename  = 'ase_vector_memory'
			  AND indexname  = 'idx_ase_vector_memory_scann'
		)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ensure scann index: check existence: %w", err)
	}
	if exists {
		return nil
	}

	// 2. Read num_leaves from the hot-reloadable config.
	cfg := vs.VectorCfg()
	numLeaves := cfg.ScaNNNumLeaves
	if numLeaves <= 0 {
		numLeaves = 10
	}

	// 3. Confirm table has > num_leaves embedded rows (AlloyDB Omni requirement).
	var count int64
	err = vs.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM toro_core.ase_vector_memory WHERE embedding IS NOT NULL
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("ensure scann index: count check: %w", err)
	}
	if count <= int64(numLeaves) {
		// Not enough rows to build ScaNN index yet — hydrator will retry when more rows arrive.
		return nil
	}

	// 4. Create the index. This is a DDL statement, so it cannot be run inside
	// a regular transaction block (hence pool.Exec is used directly).
	_, err = vs.pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_scann
		ON toro_core.ase_vector_memory USING scann (embedding cosine)
		WITH (num_leaves = %d)
		WHERE embedding IS NOT NULL`, numLeaves))
	if err != nil {
		return fmt.Errorf("ensure scann index: create: %w", err)
	}
	return nil
}

// floatsToVectorLiteral converts a []float64 into the pgvector wire format string
// that pgx can send as a text parameter to the vector column.
// Format: "[0.1,0.2,...,0.n]"
func floatsToVectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	b := make([]byte, 0, len(v)*10+2)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, fmt.Sprintf("%f", f)...)
	}
	b = append(b, ']')
	return string(b)
}
