-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS toro_core.ase_dag_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dag_id UUID NOT NULL REFERENCES toro_core.ase_dags(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    dag_config JSONB NOT NULL,
    hyper_parameters JSONB NOT NULL,
    prompts JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ase_dag_versions_dag_version ON toro_core.ase_dag_versions(dag_id, version_number);

CREATE OR REPLACE FUNCTION toro_core.trigger_save_ase_dag_version()
RETURNS TRIGGER AS $$
DECLARE
    next_version INT;
BEGIN
    SELECT COALESCE(MAX(version_number), 0) + 1 INTO next_version
    FROM toro_core.ase_dag_versions
    WHERE dag_id = NEW.id;

    INSERT INTO toro_core.ase_dag_versions (dag_id, version_number, dag_config, hyper_parameters, prompts)
    VALUES (NEW.id, next_version, NEW.dag_config, NEW.hyper_parameters, NEW.prompts);

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER save_ase_dag_version_trigger
AFTER INSERT OR UPDATE ON toro_core.ase_dags
FOR EACH ROW
EXECUTE FUNCTION toro_core.trigger_save_ase_dag_version();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS save_ase_dag_version_trigger ON toro_core.ase_dags;
DROP FUNCTION IF EXISTS toro_core.trigger_save_ase_dag_version();
DROP TABLE IF EXISTS toro_core.ase_dag_versions;
-- +goose StatementEnd
