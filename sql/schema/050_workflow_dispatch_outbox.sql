-- +goose Up
-- Commands are written together with the workflow state transition, then
-- delivered asynchronously by the orchestrator. This prevents a worker from
-- completing a step before its activation is durable.
CREATE TABLE toro_core.workflow_dispatch_outbox (
    id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES toro_core.workflows(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    target_subject TEXT NOT NULL,
    envelope JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'LEASED', 'DELIVERED')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    leased_until TIMESTAMPTZ,
    last_error TEXT,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workflow_id, step_id, conversation_id)
);

CREATE INDEX idx_workflow_dispatch_outbox_ready
    ON toro_core.workflow_dispatch_outbox (status, next_attempt_at)
    WHERE status IN ('PENDING', 'LEASED');

-- +goose Down
DROP TABLE IF EXISTS toro_core.workflow_dispatch_outbox;
