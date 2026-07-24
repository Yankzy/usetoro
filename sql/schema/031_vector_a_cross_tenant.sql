-- +goose Up

-- 1. Add metadata to wallet_transactions for cross-tenant burn auditability.
ALTER TABLE toro_core.wallet_transactions ADD COLUMN metadata JSONB DEFAULT '{}'::jsonb;

-- 2. Add payer_tenant_id to llm_turn_metrics to track who pays for cross-tenant inference.
-- Note: To avoid double counting, standard usage metrics should query by tenant_id (the executing agent),
-- while billing reports should query by payer_tenant_id (or tenant_id if payer_tenant_id is null).
ALTER TABLE toro_core.llm_turn_metrics ADD COLUMN payer_tenant_id UUID REFERENCES toro_core.entities(id) ON DELETE SET NULL;
CREATE INDEX idx_llm_metrics_payer ON toro_core.llm_turn_metrics(payer_tenant_id);

-- +goose Down
DROP INDEX IF EXISTS toro_core.idx_llm_metrics_payer;
ALTER TABLE toro_core.llm_turn_metrics DROP COLUMN payer_tenant_id;
ALTER TABLE toro_core.wallet_transactions DROP COLUMN metadata;
