-- name: UpsertAttachable :exec
INSERT INTO shadow_erp.attachables (
    realm_id,
    erp_id,
    file_name,
    content_type,
    size,
    note,
    attachable_refs,
    sync_token,
    erp_created_time,
    erp_updated_time
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    file_name = EXCLUDED.file_name,
    content_type = EXCLUDED.content_type,
    size = EXCLUDED.size,
    note = EXCLUDED.note,
    attachable_refs = EXCLUDED.attachable_refs,
    sync_token = EXCLUDED.sync_token,
    erp_created_time = EXCLUDED.erp_created_time,
    erp_updated_time = EXCLUDED.erp_updated_time,
    updated_at = NOW(),
    deleted_at = NULL; -- restore if previously soft-deleted

-- name: SoftDeleteAttachable :exec
UPDATE shadow_erp.attachables
SET deleted_at = $1, updated_at = NOW()
WHERE realm_id = $2 AND erp_id = $3;

-- name: GetAttachableByERPID :one
SELECT * FROM shadow_erp.attachables
WHERE realm_id = $1 AND erp_id = $2 AND deleted_at IS NULL;
