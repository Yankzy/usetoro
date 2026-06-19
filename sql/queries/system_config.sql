-- name: GetSystemVectorConfig :one
SELECT * FROM toro_core.system_vector_config
WHERE id = 1
LIMIT 1;

-- name: UpdateSystemVectorConfig :one
UPDATE toro_core.system_vector_config
SET embedding_provider = $2,
    embedding_model = $3,
    retrieval_top_k = $4,
    hydrator_interval_seconds = $5,
    hydrator_batch_size = $6,
    hydrator_min_confidence = $7,
    scann_num_leaves = $8,
    updated_at = NOW()
WHERE id = $1
RETURNING *;
