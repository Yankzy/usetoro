-- +goose Up
-- +goose StatementBegin
-- Add knowledge system tables to CDC Logical Replication Publication
DO $$
BEGIN
    -- Check if table is already in publication to avoid errors, though ALTER PUBLICATION usually doesn't have an IF NOT EXISTS for tables directly without checking pg_publication_tables.
    -- We can safely just recreate the publication for the base tables or dynamically add them.
    
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'documents'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub ADD TABLE toro_core.documents;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'enterprise_facts'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub ADD TABLE toro_core.enterprise_facts;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'enterprise_relationships'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub ADD TABLE toro_core.enterprise_relationships;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'documents'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub DROP TABLE toro_core.documents;
    END IF;

    IF EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'enterprise_facts'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub DROP TABLE toro_core.enterprise_facts;
    END IF;

    IF EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'toro_ledger_pub' AND schemaname = 'toro_core' AND tablename = 'enterprise_relationships'
    ) THEN
        ALTER PUBLICATION toro_ledger_pub DROP TABLE toro_core.enterprise_relationships;
    END IF;
END;
$$;
-- +goose StatementEnd
