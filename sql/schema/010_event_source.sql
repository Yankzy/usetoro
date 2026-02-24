-- +goose Up
-- =========================================================================
-- CDC Event Source Guard
-- Adds event_source to every shadow_erp table so the CDC decoder can stamp
-- each NATS event with its origin. Consumers use this to break write-back loops.
--
-- Values:
--   'toro_internal' (DEFAULT) — written by Toro's own logic
--   'qbo_sync'                — written by QBOConnector (webhook, CDC, full sync)
-- =========================================================================
ALTER TABLE shadow_erp.accounts
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.vendors
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.customers
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.invoices
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.bills
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.proposed_transactions
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.ai_corrections
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.vector_sync_state
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

ALTER TABLE shadow_erp.company_info
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'toro_internal';

-- +goose Down
ALTER TABLE shadow_erp.accounts           DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.vendors            DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.customers          DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.invoices           DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.bills              DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.proposed_transactions DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.ai_corrections     DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.vector_sync_state  DROP COLUMN IF EXISTS event_source;
ALTER TABLE shadow_erp.company_info       DROP COLUMN IF EXISTS event_source;
