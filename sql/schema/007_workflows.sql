-- +goose NO TRANSACTION
-- +goose Up

-- =========================================================================
-- SCHEMA: toro_core.workflows
-- The deterministic Redux state matrix and LLM chat history.
-- =========================================================================

-- 1. Workflows (The Redux Engine Base State)
CREATE TABLE toro_core.workflows (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    state         JSONB NOT NULL DEFAULT '{}'::jsonb,
    sequence_id   BIGINT NOT NULL DEFAULT 0,    -- Tracks monotonic NATS idempotency
    status        TEXT NOT NULL DEFAULT 'open', -- 'open', 'processing', 'completed', 'failed'
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workflows_entity_id ON toro_core.workflows(entity_id);
CREATE INDEX idx_workflows_status ON toro_core.workflows(status);

-- Attach the standard toro_core updated_at trigger
CREATE TRIGGER update_workflows_updated_at
    BEFORE UPDATE ON toro_core.workflows
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 2. Workflow History (The LLM Chat State)
-- =========================================================================
CREATE TABLE toro_core.workflow_history (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workflow_id   UUID NOT NULL REFERENCES toro_core.workflows(id) ON DELETE CASCADE,
    role          TEXT NOT NULL,    -- e.g., 'user', 'assistant', 'system', 'tool'
    content       JSONB NOT NULL,   -- The explicit OpenAI content matrix / function calls 
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workflow_history_workflow_id ON toro_core.workflow_history(workflow_id);


-- +goose Down
DROP TABLE IF EXISTS toro_core.workflow_history;
DROP TRIGGER IF EXISTS update_workflows_updated_at ON toro_core.workflows;
DROP TABLE IF EXISTS toro_core.workflows;
