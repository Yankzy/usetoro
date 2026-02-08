-- +goose Up
-- TRUNCATE is used here to avoid issues with adding a NOT NULL column to a populated table
-- In a production environment with critical data, we would backfill this instead.
TRUNCATE TABLE qbo_connections;

ALTER TABLE qbo_connections 
ADD COLUMN tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE;

-- Create index for faster lookups by tenant
CREATE INDEX idx_qbo_tenant_id ON qbo_connections(tenant_id);

-- Add unique constraint so one tenant can only have one QBO connection (optional but likely desired)
ALTER TABLE qbo_connections ADD CONSTRAINT unique_tenant_qbo UNIQUE (tenant_id);
