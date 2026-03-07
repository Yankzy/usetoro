-- +goose Up
CREATE TABLE IF NOT EXISTS shadow_erp.attachables (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id VARCHAR(255) NOT NULL,
    erp_id VARCHAR(255) NOT NULL,
    file_name VARCHAR(255),
    content_type VARCHAR(255),
    size NUMERIC,
    note TEXT,
    attachable_refs JSONB,
    sync_token VARCHAR(255),
    erp_created_time TIMESTAMPTZ,
    erp_updated_time TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (realm_id, erp_id)
);

CREATE INDEX idx_attachables_realm_id ON shadow_erp.attachables(realm_id);

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.attachables;
