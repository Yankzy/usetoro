-- name: GetPendingHydrationRows :many
-- Returns rows that have no embedding yet, ordered oldest-first.
-- Used by the VectorHydrator to find work each tick.
SELECT id, realm_id, source_type, raw_text, source_row_id
FROM toro_core.ase_vector_memory
WHERE embedding IS NULL
ORDER BY created_at ASC
LIMIT $1;

-- name: InsertPendingVectorRow :exec
-- Registers a new source row for future hydration (embedding = NULL).
-- The VectorHydrator will pick this up on its next tick.
INSERT INTO toro_core.ase_vector_memory (realm_id, source_type, raw_text, source_row_id, metadata)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (realm_id, source_type, source_row_id) DO NOTHING;

-- NOTE: SearchVectorMemory is intentionally omitted here.
-- Those queries involve the `vector` column type, which requires the pgvector-go
-- library that is not yet vendored. The VectorStore in vector_store.go handles
-- those operations directly via raw pgx queries using the pgvector wire format string.
