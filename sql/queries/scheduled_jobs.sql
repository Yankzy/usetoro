-- name: InsertScheduledJob :one
INSERT INTO toro_core.scheduled_jobs (
    queue_subject,
    payload_json,
    fire_at
) VALUES (
    $1, $2, $3
) RETURNING id;

-- name: GetPendingJobsWindow :many
SELECT * FROM toro_core.scheduled_jobs
WHERE status = 'pending'
  AND fire_at >= $1
  AND fire_at <= $2
ORDER BY fire_at ASC;

-- name: MarkJobFired :exec
UPDATE toro_core.scheduled_jobs
SET status = 'fired', fired_at = NOW()
WHERE id = $1;
