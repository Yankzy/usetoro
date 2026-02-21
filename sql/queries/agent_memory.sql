-- name: CreateMemoryRule :exec
INSERT INTO toro_core.agent_memory_rules (realm_id, entity_type, entity_value, instruction)
VALUES ($1, 'keyword', $2, $3)
ON CONFLICT (realm_id, entity_type, entity_value)
DO UPDATE SET instruction = EXCLUDED.instruction, created_at = NOW();

-- name: GetMemoryRules :many
SELECT entity_value, instruction
FROM toro_core.agent_memory_rules
WHERE realm_id = $1;
