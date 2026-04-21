-- +goose Up
-- Migration to update shadow_erp.rule_groups for split transactions (allocations) and unify target entities.

-- 1. Add new columns
ALTER TABLE shadow_erp.rule_groups ADD COLUMN IF NOT EXISTS requires_review BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE shadow_erp.rule_groups ADD COLUMN IF NOT EXISTS allocations JSONB DEFAULT '[]'::jsonb NOT NULL;

-- 2. Migrate existing target_account_id data into allocations JSONB
-- Note: We use a simple UPDATE before dropping the column.
UPDATE shadow_erp.rule_groups 
SET allocations = jsonb_build_array(
    jsonb_build_object(
        'AccountID', target_account_id,
        'Percentage', 100.0
    )
)
WHERE target_account_id IS NOT NULL;

-- 3. Rename and Drop
-- RENAMING target_vendor_id to target_entity_id
-- We use direct ALTER TABLE as sqlc handles this better than DO blocks.
ALTER TABLE shadow_erp.rule_groups RENAME COLUMN target_vendor_id TO target_entity_id;
ALTER TABLE shadow_erp.rule_groups DROP COLUMN target_account_id;

-- +goose Down
-- Reverting rule_groups schema changes

-- 1. Restore target_account_id
ALTER TABLE shadow_erp.rule_groups ADD COLUMN target_account_id UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL;

-- 2. Rename target_entity_id back to target_vendor_id
ALTER TABLE shadow_erp.rule_groups RENAME COLUMN target_entity_id TO target_vendor_id;

-- 3. Remove new columns
ALTER TABLE shadow_erp.rule_groups DROP COLUMN IF EXISTS requires_review;
ALTER TABLE shadow_erp.rule_groups DROP COLUMN IF EXISTS allocations;
