-- name: CreateAgentConfiguration :one
INSERT INTO toro_core.agent_configurations (
    name,
    description,
    system_prompt,
    sdk_client,
    metadata
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: UpsertAgentConfiguration :one
INSERT INTO toro_core.agent_configurations (
    name,
    description,
    system_prompt,
    sdk_client,
    metadata
) VALUES (
    $1, $2, $3, $4, $5
) ON CONFLICT (name) DO UPDATE SET
    description = EXCLUDED.description,
    system_prompt = EXCLUDED.system_prompt,
    sdk_client = EXCLUDED.sdk_client,
    metadata = EXCLUDED.metadata,
    updated_at = NOW()
RETURNING *;

-- name: GetAgentConfigurationByName :one
SELECT * FROM toro_core.agent_configurations
WHERE name = $1 LIMIT 1;

-- name: ListAgentConfigurations :many
SELECT * FROM toro_core.agent_configurations
ORDER BY name ASC;
