-- name: GetEntityDescendants :many
WITH RECURSIVE entity_tree AS (
    -- Base case: the current entity
    SELECT base.id
    FROM toro_core.entities base
    WHERE base.id = $1

    UNION ALL

    -- Recursive step: all children of the currently found entities
    SELECT child.id
    FROM toro_core.entities child
    INNER JOIN entity_tree et ON child.parent_id = et.id
)
SELECT id FROM entity_tree;

-- name: GetRealmIDByEntityID :one
SELECT realm_id
FROM toro_core.erp_connections
WHERE entity_id = $1
LIMIT 1;
