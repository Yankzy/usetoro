-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS toro_core.ase_dags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID REFERENCES toro_core.users(id) ON DELETE CASCADE,
    realm_id VARCHAR(255),
    name VARCHAR(255) NOT NULL DEFAULT 'default',
    dag_config JSONB NOT NULL,
    hyper_parameters JSONB NOT NULL,
    prompts JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE NULLS NOT DISTINCT (tenant_id, realm_id, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS toro_core.ase_dags;
-- +goose StatementEnd
