-- +goose Up
-- +goose StatementBegin
-- 1. Drop the default value for the name column
ALTER TABLE toro_core.ase_dags ALTER COLUMN name DROP DEFAULT;

-- 2. Create the system vector config table
CREATE TABLE IF NOT EXISTS toro_core.system_vector_config (
    id INT PRIMARY KEY DEFAULT 1,
    embedding_provider VARCHAR(50) NOT NULL DEFAULT 'openai',
    embedding_model VARCHAR(100) NOT NULL DEFAULT 'text-embedding-3-small',
    retrieval_top_k INT NOT NULL DEFAULT 5,
    hydrator_interval_seconds INT NOT NULL DEFAULT 30,
    hydrator_batch_size INT NOT NULL DEFAULT 50,
    hydrator_min_confidence FLOAT NOT NULL DEFAULT 0.98,
    scann_num_leaves INT NOT NULL DEFAULT 10,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT system_vector_config_single_row CHECK (id = 1)
);

-- Insert the default configuration
INSERT INTO toro_core.system_vector_config (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS toro_core.system_vector_config;

ALTER TABLE toro_core.ase_dags ALTER COLUMN name SET DEFAULT 'default';
-- +goose StatementEnd
