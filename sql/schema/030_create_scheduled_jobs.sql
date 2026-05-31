-- +goose Up
CREATE TABLE toro_core.scheduled_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    queue_subject TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    fire_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fired_at TIMESTAMPTZ
);
CREATE INDEX idx_scheduled_jobs_fire_at ON toro_core.scheduled_jobs(fire_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS toro_core.scheduled_jobs;
