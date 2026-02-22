-- +goose Up
-- =========================================================================
-- SCHEMA: shadow_erp
-- Table: rule_audit_logs

CREATE TABLE IF NOT EXISTS shadow_erp.rule_audit_logs (
    id SERIAL PRIMARY KEY,
    realm_id TEXT NOT NULL,
    transaction_id UUID NOT NULL REFERENCES shadow_erp.proposed_transactions(id) ON DELETE CASCADE,
    rule_group_id INT REFERENCES shadow_erp.rule_groups(id) ON DELETE SET NULL,
    matched BOOLEAN NOT NULL,
    match_info JSONB DEFAULT '{}'::jsonb NOT NULL,
    human_readable_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_realm ON shadow_erp.rule_audit_logs(realm_id);
CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_transaction ON shadow_erp.rule_audit_logs(transaction_id);
CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_group ON shadow_erp.rule_audit_logs(rule_group_id);

COMMENT ON COLUMN shadow_erp.rule_audit_logs.match_info IS 'Verbose match explanation: JSON containing condition-level results (maps to MatchExplanation struct)';

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.rule_audit_logs;
