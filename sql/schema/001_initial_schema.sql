-- +goose Up
-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 1. Tenants Table
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    status TEXT DEFAULT 'active', -- 'active', 'suspended', 'archived'
    plan_tier TEXT DEFAULT 'basic', -- 'basic', 'pro', 'enterprise'
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Index for authentication lookups
CREATE INDEX idx_tenants_status ON tenants(status);

-- 2. Transactions Table (RLS Enabled)
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    
    external_id TEXT NOT NULL,
    amount_micros BIGINT NOT NULL,
    description TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    -- Performance: Index tenant_id for RLS lookups
    UNIQUE(tenant_id, external_id)
);

-- CRITICAL: Enable RLS on the table
ALTER TABLE transactions ENABLE ROW LEVEL SECURITY;

-- 3. RLS Policies
-- Policy for Transactions: specific tenant isolation
CREATE POLICY tenant_isolation_policy ON transactions
    FOR ALL -- Applies to SELECT, INSERT, UPDATE, DELETE
    USING (
        -- The row's tenant_id must match the current session variable
        tenant_id = current_setting('app.current_tenant')::UUID
    )
    WITH CHECK (
        -- Ensures new rows are also inserted with the correct tenant_id
        tenant_id = current_setting('app.current_tenant')::UUID
    );
