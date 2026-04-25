-- name: CreateOrGetWorkflow :one
INSERT INTO toro_core.workflows (id, entity_id, state)
VALUES (@id, @entity_id, '{}'::jsonb)
ON CONFLICT (id) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: GetWorkflow :one
SELECT * FROM toro_core.workflows WHERE id = $1 LIMIT 1;

-- name: UpdateWorkflowState :one
UPDATE toro_core.workflows
SET state = @state, sequence_id = @sequence_id, updated_at = NOW()
WHERE id = @id
RETURNING *;

-- name: GetWorkflowsByEntityID :many
SELECT * FROM toro_core.workflows 
WHERE entity_id = $1
ORDER BY updated_at DESC;

-- name: LogWorkflowHistory :one
INSERT INTO toro_core.workflow_history (workflow_id, role, content)
VALUES (@workflow_id, @role, @content)
RETURNING *;

-- =========================================================================
-- Workflow Blueprints (declarative definitions)
-- =========================================================================

-- name: UpsertWorkflowBlueprint :one
INSERT INTO toro_core.workflow_blueprints (name, trigger_topic, definition)
VALUES (@name, @trigger_topic, @definition)
ON CONFLICT (name) DO UPDATE
SET trigger_topic = EXCLUDED.trigger_topic,
    definition    = EXCLUDED.definition,
    updated_at    = NOW()
RETURNING *;

-- name: GetWorkflowBlueprints :many
SELECT *
FROM toro_core.workflow_blueprints
ORDER BY name;

-- name: GetBlueprintByName :one
SELECT *
FROM toro_core.workflow_blueprints
WHERE name = $1
LIMIT 1;

-- name: GetBlueprintByNameOrTriggerTopic :one
SELECT *
FROM toro_core.workflow_blueprints
WHERE name = $1 OR trigger_topic = $1
LIMIT 1;

-- name: DeleteWorkflowBlueprint :exec
DELETE FROM toro_core.workflow_blueprints
WHERE name = $1;
