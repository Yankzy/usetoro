-- +goose NO TRANSACTION
-- +goose Up

-- =========================================================================
-- Phase 1: Activate vector extensions
-- =========================================================================
-- pgvector: provides the vector column type and <=> cosine distance operator.
CREATE EXTENSION IF NOT EXISTS vector;

-- alloydb_scann: Google's ScaNN-based approximate nearest-neighbour index.
-- Ships bundled with AlloyDB Omni 17.7+ (already loaded in shared_preload_libraries).
CREATE EXTENSION IF NOT EXISTS alloydb_scann;

-- =========================================================================
-- Phase 2: Removed (toro_core schema already exists)
-- =========================================================================

-- =========================================================================
-- Phase 3: Vector memory table
--
-- Design notes:
--   • realm_id      — matches toro_core.erp_connections.realm_id exactly.
--                     This is the multi-tenant isolation key.
--   • source_type   — 'memory_rule'  (from toro_core.agent_memory_rules)
--                     'resolved_tx'  (from fignode.staging_transactions with
--                                     confidence >= hydrator_min_confidence)
--   • raw_text      — the string that was embedded (description or instruction).
--   • embedding     — vector(1536) matches text-embedding-3-small dimensionality.
--                     WARNING: changing dimensions requires DROP + recreate of
--                     this column AND the ScaNN index. Update ase.yml accordingly.
--   • source_row_id — UUID of the originating row for auditability.
--   • metadata      — JSONB bag for macro_class, account_type, confidence, etc.
--                     Avoids JOIN overhead at retrieval time.
--   • embedded_at   — timestamp of last successful embedding generation.
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.ase_vector_memory (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id      TEXT        NOT NULL,
    namespace     TEXT        NOT NULL DEFAULT 'general',
    source_type   TEXT        NOT NULL CHECK (source_type IN ('memory_rule', 'resolved_tx', 'document', 'fact')),
    raw_text      TEXT        NOT NULL,
    embedding     vector(1536),              -- NULL until hydrated by VectorHydrator
    source_row_id UUID        NOT NULL,      -- PK of the originating row
    metadata      JSONB       NOT NULL DEFAULT '{}',
    embedded_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (realm_id, namespace, source_type, source_row_id)
);

-- =========================================================================
-- Phase 4: Indexes
-- =========================================================================

-- B-Tree index: enables the planner to pre-filter by realm_id and namespace
-- before the ScaNN scan, achieving Bitmap-Assisted Inline Filtering.
-- Also used by the hydrator to find un-embedded rows efficiently.
CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_realm_ns
    ON toro_core.ase_vector_memory (realm_id, namespace, source_type);

-- Partial index: helps the hydrator quickly find rows pending embedding.
CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_pending
    ON toro_core.ase_vector_memory (created_at)
    WHERE embedding IS NULL;

-- NOTE: The ScaNN ANN index (idx_ase_vector_memory_scann) is NOT created here.
-- AlloyDB Omni forbids building a ScaNN index on an empty table.
-- It is created lazily by VectorStore.EnsureScaNNIndex the first time the
-- VectorHydrator successfully embeds rows into the table.
-- See: vector_store.go → EnsureScaNNIndex()


CREATE TRIGGER update_ase_vector_memory_updated_at
    BEFORE UPDATE ON toro_core.ase_vector_memory
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();


-- +goose Down
DROP TRIGGER IF EXISTS update_ase_vector_memory_updated_at ON toro_core.ase_vector_memory;
-- ScaNN index is created lazily at runtime; drop it if it exists.
DROP INDEX IF EXISTS idx_ase_vector_memory_scann;
DROP INDEX IF EXISTS idx_ase_vector_memory_pending;
DROP INDEX IF EXISTS idx_ase_vector_memory_realm;
DROP TABLE IF EXISTS toro_core.ase_vector_memory;
DROP EXTENSION IF EXISTS alloydb_scann;
DROP EXTENSION IF EXISTS vector;

