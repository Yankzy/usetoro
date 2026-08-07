-- +goose Up

-- 1. Add namespace column to toro_core.ase_vector_memory
ALTER TABLE toro_core.ase_vector_memory
ADD COLUMN IF NOT EXISTS namespace TEXT NOT NULL DEFAULT 'general';

-- 2. Update unique constraint on ase_vector_memory
ALTER TABLE toro_core.ase_vector_memory
DROP CONSTRAINT IF EXISTS ase_vector_memory_realm_id_source_type_source_row_id_key;

ALTER TABLE toro_core.ase_vector_memory
ADD CONSTRAINT ase_vector_memory_realm_ns_type_row_key
UNIQUE (realm_id, namespace, source_type, source_row_id);

-- 3. Update B-Tree index for bitmap pre-filtering
DROP INDEX IF EXISTS toro_core.idx_ase_vector_memory_realm;

CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_realm_ns
ON toro_core.ase_vector_memory (realm_id, namespace, source_type);

-- +goose Down
DROP INDEX IF EXISTS toro_core.idx_ase_vector_memory_realm_ns;

CREATE INDEX IF NOT EXISTS idx_ase_vector_memory_realm
ON toro_core.ase_vector_memory (realm_id, source_type);

ALTER TABLE toro_core.ase_vector_memory
DROP CONSTRAINT IF EXISTS ase_vector_memory_realm_ns_type_row_key;

ALTER TABLE toro_core.ase_vector_memory
ADD CONSTRAINT ase_vector_memory_realm_id_source_type_source_row_id_key
UNIQUE (realm_id, source_type, source_row_id);

ALTER TABLE toro_core.ase_vector_memory
DROP COLUMN IF EXISTS namespace;
