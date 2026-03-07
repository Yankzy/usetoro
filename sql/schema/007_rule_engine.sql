-- +goose Up
-- =========================================================================
-- SCHEMA: shadow_erp
-- Table: rule_groups & rule_conditions
-- Purpose: Deterministic rule engine for evaluating transactions against 
--          user-defined conditions (e.g., string matching, regex, amount).
-- =========================================================================

-- Rule Groups hold the hierarchical logic and metadata
CREATE TABLE IF NOT EXISTS shadow_erp.rule_groups (
    id SERIAL PRIMARY KEY,
    realm_id TEXT NOT NULL, -- To isolate rules per tenant/connection
    name VARCHAR(255) NOT NULL,
    logic VARCHAR(10) NOT NULL DEFAULT 'AND', -- 'AND' or 'OR'
    priority INT NOT NULL DEFAULT 0,
    keywords TEXT, -- Auto-generated for optimization
    active BOOLEAN NOT NULL DEFAULT true,
    
    -- Resolution Targets
    target_account_id UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,
    target_vendor_id UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,

    parent_id INT REFERENCES shadow_erp.rule_groups(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

-- Indexes for fast filtering
CREATE INDEX IF NOT EXISTS idx_rule_groups_realm ON shadow_erp.rule_groups(realm_id);
CREATE INDEX IF NOT EXISTS idx_rule_groups_parent ON shadow_erp.rule_groups(parent_id);
CREATE INDEX IF NOT EXISTS idx_rule_groups_active ON shadow_erp.rule_groups(active) WHERE active = true;

-- Rule Conditions define the specific matching criteria for a group
CREATE TABLE IF NOT EXISTS shadow_erp.rule_conditions (
    id SERIAL PRIMARY KEY,
    rule_group_id INT NOT NULL REFERENCES shadow_erp.rule_groups(id) ON DELETE CASCADE,
    field VARCHAR(50) NOT NULL,    -- 'description', 'vendor', 'customer', 'category', 'amount', 'date', 'time', 'memo', 'role', 'uuid', 'mcc', 'invoice_text'
    operator VARCHAR(50) NOT NULL, -- 'equals', 'equals_cs', 'contains', 'not_contains', 'contains_cs', 'startswith', 'endswith', 'in', 'not_in', 'regex', 'is_null', 'is_not_null', 'gt', 'gte', 'lt', 'lte'
    value TEXT NOT NULL,           -- The target value to match against
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rule_conditions_group ON shadow_erp.rule_conditions(rule_group_id);

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.rule_conditions;
DROP TABLE IF EXISTS shadow_erp.rule_groups;
