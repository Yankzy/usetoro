-- +goose Up
-- +goose StatementBegin
-- =========================================================================
-- CDC Logical Replication Publication
-- Watches: toro_core.users, toro_core.qbo_connections, and ALL shadow_erp tables
-- =========================================================================

DROP PUBLICATION IF EXISTS toro_ledger_pub;

-- Base publication: core tables that need CDC
CREATE PUBLICATION toro_ledger_pub FOR TABLE
    toro_core.users,
    toro_core.qbo_connections;

-- Dynamically add all tables in the shadow_erp schema
DO $$
DECLARE
    tbl record;
BEGIN
    FOR tbl IN
        SELECT table_name
        FROM information_schema.tables
        WHERE table_schema = 'shadow_erp' AND table_type = 'BASE TABLE'
    LOOP
        EXECUTE format('ALTER PUBLICATION toro_ledger_pub ADD TABLE shadow_erp.%I', tbl.table_name);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP PUBLICATION IF EXISTS toro_ledger_pub;
-- +goose StatementEnd
