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

-- name: LogWorkflowHistory :one
INSERT INTO toro_core.workflow_history (workflow_id, role, content)
VALUES (@workflow_id, @role, @content)
RETURNING *;
