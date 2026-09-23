-- +goose NO TRANSACTION
-- +goose Up
-- =========================================================================
-- Unify Django EntityModel and Go toro_core.entities
-- Add accounting and treebeard fields to toro_core.entities
-- =========================================================================

-- 1. Add new columns
ALTER TABLE toro_core.entities 
    ADD COLUMN IF NOT EXISTS slug VARCHAR(50) UNIQUE,
    ADD COLUMN IF NOT EXISTS currency VARCHAR(3) DEFAULT 'USD',
    ADD COLUMN IF NOT EXISTS accrual_method BOOLEAN DEFAULT false,
    ADD COLUMN IF NOT EXISTS fy_start_month INTEGER DEFAULT 1,
    ADD COLUMN IF NOT EXISTS last_closing_date DATE,
    ADD COLUMN IF NOT EXISTS picture VARCHAR(100),
    ADD COLUMN IF NOT EXISTS meta JSONB DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS hidden BOOLEAN DEFAULT false,
    ADD COLUMN IF NOT EXISTS is_ephemeral BOOLEAN DEFAULT false,
    ADD COLUMN IF NOT EXISTS default_coa_id UUID,
    ADD COLUMN IF NOT EXISTS admin_id UUID REFERENCES toro_core.users(id),
    
    -- Contact fields
    ADD COLUMN IF NOT EXISTS address_1 VARCHAR(70),
    ADD COLUMN IF NOT EXISTS address_2 VARCHAR(70),
    ADD COLUMN IF NOT EXISTS city VARCHAR(70),
    ADD COLUMN IF NOT EXISTS state VARCHAR(70),
    ADD COLUMN IF NOT EXISTS country VARCHAR(70),
    ADD COLUMN IF NOT EXISTS zip_code VARCHAR(20),
    ADD COLUMN IF NOT EXISTS email VARCHAR(254),
    ADD COLUMN IF NOT EXISTS website VARCHAR(200),
    ADD COLUMN IF NOT EXISTS phone VARCHAR(30),
    
    -- Treebeard fields
    ADD COLUMN IF NOT EXISTS path VARCHAR(255) UNIQUE,
    ADD COLUMN IF NOT EXISTS depth INTEGER,
    ADD COLUMN IF NOT EXISTS numchild INTEGER DEFAULT 0;

-- 2. Clean up any partially migrated state from previous failed runs
-- Move any existing paths in toro_core.entities out of the way to avoid unique constraint collisions
UPDATE toro_core.entities SET path = concat('TEMP-', substr(id::text, 1, 8)) WHERE path IS NOT NULL;

-- 3. Migrate data from public.ledger_entitymodel to toro_core.entities (if table exists)
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'ledger_entitymodel') THEN
        
        -- Update existing matched UUIDs
        UPDATE toro_core.entities c
        SET 
            slug = p.slug,
            accrual_method = p.accrual_method,
            fy_start_month = p.fy_start_month,
            last_closing_date = p.last_closing_date,
            picture = p.picture,
            meta = p.meta,
            hidden = p.hidden,
            is_ephemeral = p.is_ephemeral,
            default_coa_id = p.default_coa_id,
            admin_id = p.admin_id,
            address_1 = p.address_1,
            address_2 = p.address_2,
            city = p.city,
            state = p.state,
            country = p.country,
            zip_code = p.zip_code,
            email = p.email,
            website = p.website,
            phone = p.phone,
            path = p.path,
            depth = p.depth,
            numchild = p.numchild,
            created_at = p.created,
            updated_at = p.updated
        FROM public.ledger_entitymodel p
        WHERE c.id = p.uuid;

        -- Insert stray entities
        INSERT INTO toro_core.entities (
            id, name, entity_type, slug, accrual_method, fy_start_month, 
            last_closing_date, picture, meta, hidden, is_ephemeral, default_coa_id, admin_id,
            address_1, address_2, city, state, country, zip_code, email, website, phone,
            path, depth, numchild, created_at, updated_at
        )
        SELECT 
            uuid, name, 'client', slug, accrual_method, fy_start_month, 
            last_closing_date, picture, meta, hidden, is_ephemeral, default_coa_id, admin_id,
            address_1, address_2, city, state, country, zip_code, email, website, phone,
            path, depth, numchild, created, updated
        FROM public.ledger_entitymodel
        WHERE uuid NOT IN (SELECT id FROM toro_core.entities);
        
    END IF;
END $$;
-- +goose StatementEnd

-- 4. Backfill remaining toro_core.entities rows that still don't have treebeard paths
-- Assign root path '1001', '1002' etc. (offset by 1000 to avoid colliding with early Django entities)
-- and a unique slug based on ID.
WITH numbered_entities AS (
    SELECT id, row_number() OVER (ORDER BY created_at ASC) as rn
    FROM toro_core.entities
    WHERE path IS NULL OR path LIKE 'TEMP-%'
)
UPDATE toro_core.entities c
SET 
    path = lpad((n.rn + 1000)::text, 4, '0'),
    depth = 1,
    numchild = 0,
    slug = concat('entity-', substr(c.id::text, 1, 8)),
    currency = 'USD'
FROM numbered_entities n
WHERE c.id = n.id AND (c.path IS NULL OR c.path LIKE 'TEMP-%');

-- Make Treebeard fields NOT NULL after backfilling
ALTER TABLE toro_core.entities ALTER COLUMN path SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN depth SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN numchild SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN slug SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN currency SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN accrual_method SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN fy_start_month SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN hidden SET NOT NULL;
ALTER TABLE toro_core.entities ALTER COLUMN is_ephemeral SET NOT NULL;

-- +goose Down
ALTER TABLE toro_core.entities 
    DROP COLUMN IF EXISTS slug,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS accrual_method,
    DROP COLUMN IF EXISTS fy_start_month,
    DROP COLUMN IF EXISTS last_closing_date,
    DROP COLUMN IF EXISTS picture,
    DROP COLUMN IF EXISTS meta,
    DROP COLUMN IF EXISTS hidden,
    DROP COLUMN IF EXISTS is_ephemeral,
    DROP COLUMN IF EXISTS default_coa_id,
    DROP COLUMN IF EXISTS admin_id,
    DROP COLUMN IF EXISTS address_1,
    DROP COLUMN IF EXISTS address_2,
    DROP COLUMN IF EXISTS city,
    DROP COLUMN IF EXISTS state,
    DROP COLUMN IF EXISTS country,
    DROP COLUMN IF EXISTS zip_code,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS website,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS path,
    DROP COLUMN IF EXISTS depth,
    DROP COLUMN IF EXISTS numchild;
