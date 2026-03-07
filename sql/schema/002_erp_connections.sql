-- +goose Up
-- =========================================================================
-- ERP Connections (in toro_core — ties ERP OAuth to the entity hierarchy)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.erp_connections (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    erp_system    TEXT NOT NULL, -- e.g. 'quickbooks_online', 'netsuite'
    realm_id      TEXT NOT NULL,
    access_token  TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,

    -- CDC Sync Tracking
    last_sync_timestamp      TIMESTAMPTZ,

    -- Webhook Event Timestamps (event-driven CDC)
    last_webhook_account     TIMESTAMPTZ,
    last_webhook_vendor      TIMESTAMPTZ,
    last_webhook_customer    TIMESTAMPTZ,
    last_webhook_invoice     TIMESTAMPTZ,
    last_webhook_bill        TIMESTAMPTZ,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(erp_system, realm_id),
    UNIQUE(entity_id)           -- One ERP connection per entity
);

CREATE INDEX IF NOT EXISTS idx_erp_realm_id   ON toro_core.erp_connections(realm_id);
CREATE INDEX IF NOT EXISTS idx_erp_entity_id  ON toro_core.erp_connections(entity_id);
CREATE INDEX IF NOT EXISTS idx_erp_system     ON toro_core.erp_connections(erp_system);

COMMENT ON COLUMN toro_core.erp_connections.last_sync_timestamp IS 'Timestamp of last successful CDC sync. Used as changedSince parameter for CDC API calls. Max lookback is 30 days per QBO limits.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_account  IS 'Timestamp of last successful Account webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_vendor   IS 'Timestamp of last successful Vendor webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_customer IS 'Timestamp of last successful Customer webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_invoice  IS 'Timestamp of last successful Invoice webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_bill     IS 'Timestamp of last successful Bill webhook. Used by CDC to fetch only missed events.';

-- +goose Down
DROP TABLE IF EXISTS toro_core.erp_connections;
