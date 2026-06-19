-- name: UpsertASEConfig :one
INSERT INTO toro_core.ase_dags (
    tenant_id,
    realm_id,
    name,
    dag_config,
    hyper_parameters,
    prompts,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW()
)
ON CONFLICT (tenant_id, realm_id, name)
DO UPDATE SET
    dag_config = EXCLUDED.dag_config,
    hyper_parameters = EXCLUDED.hyper_parameters,
    prompts = EXCLUDED.prompts,
    updated_at = NOW()
RETURNING *;

-- name: GetASEConfigByTenant :one
SELECT * FROM toro_core.ase_dags
WHERE tenant_id = $1 AND name = $2
LIMIT 1;

-- name: GetASEConfigByRealm :one
SELECT * FROM toro_core.ase_dags
WHERE realm_id = $1 AND name = $2
LIMIT 1;

-- name: GetDefaultASEConfig :one
SELECT * FROM toro_core.ase_dags
WHERE tenant_id IS NULL AND realm_id IS NULL AND name = 'default'
LIMIT 1;

-- name: GetASEConfigGlobalByName :one
SELECT * FROM toro_core.ase_dags
WHERE tenant_id IS NULL AND realm_id IS NULL AND name = $1
LIMIT 1;

-- name: ListASEConfigsByTenant :many
SELECT * FROM toro_core.ase_dags
WHERE tenant_id = $1
ORDER BY updated_at DESC;

-- name: ListAllASEConfigs :many
SELECT * FROM toro_core.ase_dags
ORDER BY updated_at DESC;

-- name: ListASEDagVersions :many
SELECT id, dag_id, version_number, created_at
FROM toro_core.ase_dag_versions
WHERE dag_id = $1
ORDER BY version_number DESC;

-- name: GetASEDagVersion :one
SELECT id, dag_id, version_number, dag_config, hyper_parameters, prompts, created_at
FROM toro_core.ase_dag_versions
WHERE id = $1;

-- name: UpdateASEConfigByID :one
UPDATE toro_core.ase_dags
SET dag_config = $2,
    hyper_parameters = $3,
    prompts = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

