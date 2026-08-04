-- +goose Up
CREATE TABLE fignode.client_request_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL,
    session_id UUID NOT NULL,
    client_id UUID NOT NULL,
    request_type TEXT NOT NULL,
    context TEXT,
    dag_node_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_client_request_outbox_session_id ON fignode.client_request_outbox(session_id);

-- +goose Down
DROP TABLE IF EXISTS fignode.client_request_outbox;
