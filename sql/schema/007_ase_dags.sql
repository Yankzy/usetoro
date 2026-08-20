-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS toro_core.ase_dags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES toro_core.users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL DEFAULT 'default',
    dag_config JSONB NOT NULL,
    hyper_parameters JSONB NOT NULL,
    prompts JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_ase_dags_user_name UNIQUE NULLS NOT DISTINCT (user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_ase_dags_user_name ON toro_core.ase_dags(user_id, name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS toro_core.ase_dags;
-- +goose StatementEnd
