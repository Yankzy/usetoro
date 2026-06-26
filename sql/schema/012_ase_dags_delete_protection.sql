-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA toro_core;

CREATE OR REPLACE FUNCTION toro_core.protect_ase_dags_delete()
RETURNS TRIGGER AS $$
DECLARE
    expected_hash TEXT;
BEGIN
    expected_hash := current_setting('toro.expected_password_hash', true);
    
    IF expected_hash IS NULL OR expected_hash = '' THEN
        RAISE EXCEPTION 'toro.expected_password_hash is not configured in the database.';
    END IF;

    IF current_setting('toro.admin_password', true) IS NULL OR 
       toro_core.crypt(current_setting('toro.admin_password', true), expected_hash) IS DISTINCT FROM expected_hash THEN
        RAISE EXCEPTION 'Deletion of ASE DAGs requires an admin password. Use SET toro.admin_password = ''...'';';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

-- Helper function to easily set the admin password hash
-- Usage: SELECT toro_core.set_admin_password('your_desired_password_here');
CREATE OR REPLACE FUNCTION toro_core.set_admin_password(new_password TEXT)
RETURNS void AS $$
BEGIN
    EXECUTE format('ALTER DATABASE %I SET toro.expected_password_hash = %L', 
                   current_database(), 
                   toro_core.crypt(new_password, toro_core.gen_salt('bf'::text)));
    -- Apply to current session immediately as well
    PERFORM set_config('toro.expected_password_hash', toro_core.crypt(new_password, toro_core.gen_salt('bf'::text)), false);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_protect_ase_dags_delete
    BEFORE DELETE ON toro_core.ase_dags
    FOR EACH ROW
    EXECUTE FUNCTION toro_core.protect_ase_dags_delete();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_protect_ase_dags_delete ON toro_core.ase_dags;
DROP FUNCTION IF EXISTS toro_core.protect_ase_dags_delete();
DROP FUNCTION IF EXISTS toro_core.set_admin_password(TEXT);
-- +goose StatementEnd
