-- +goose Up
-- +goose StatementBegin

-- =========================================================================
-- Expand source_type CHECK constraint on toro_core.ase_vector_memory to
-- include 'document' alongside 'memory_rule' and 'resolved_tx'.
-- =========================================================================

DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'toro_core.ase_vector_memory'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%source_type%'
    LOOP
        EXECUTE format('ALTER TABLE toro_core.ase_vector_memory DROP CONSTRAINT %I', r.conname);
    END LOOP;
END $$;

ALTER TABLE toro_core.ase_vector_memory
    ADD CONSTRAINT ase_vector_memory_source_type_check
    CHECK (source_type IN ('memory_rule', 'resolved_tx', 'document'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE toro_core.ase_vector_memory
    DROP CONSTRAINT IF EXISTS ase_vector_memory_source_type_check;

ALTER TABLE toro_core.ase_vector_memory
    ADD CONSTRAINT ase_vector_memory_source_type_check
    CHECK (source_type IN ('memory_rule', 'resolved_tx'));
-- +goose StatementEnd
