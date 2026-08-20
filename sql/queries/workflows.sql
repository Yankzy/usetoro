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

-- name: GetActiveWorkflowsByEntityID :many
SELECT * FROM toro_core.workflows 
WHERE entity_id = $1 AND status IN ('open', 'processing')
ORDER BY updated_at DESC;


-- name: LogWorkflowHistory :one
INSERT INTO toro_core.workflow_history (workflow_id, role, content)
VALUES (@workflow_id, @role, @content)
RETURNING *;

-- =========================================================================
-- Workflow Blueprints (declarative definitions)
-- =========================================================================

-- name: UpsertWorkflowBlueprint :one
INSERT INTO toro_core.workflow_blueprints (name, user_id, trigger_topic, definition)
VALUES (@name, @user_id, @trigger_topic, @definition)
ON CONFLICT (name) DO UPDATE
SET trigger_topic = EXCLUDED.trigger_topic,
    definition    = EXCLUDED.definition,
    user_id       = COALESCE(EXCLUDED.user_id, toro_core.workflow_blueprints.user_id),
    updated_at    = NOW()
RETURNING *;

-- name: GetWorkflowBlueprints :many
SELECT *
FROM toro_core.workflow_blueprints
ORDER BY name;

-- name: GetWorkflowBlueprintsByUser :many
SELECT *
FROM toro_core.workflow_blueprints
WHERE user_id = $1 OR user_id IS NULL
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
