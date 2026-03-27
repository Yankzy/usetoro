-- +goose Up
-- Stalled Messages (Paywall DLQ)
-- Stores NATS JetStream payloads that failed due to insufficient Micrions (402 Payment Required).
-- These are automatically replayed when the agent's wallet is topped up.
CREATE TABLE toro_core.stalled_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    agent_did VARCHAR(255) NOT NULL,
    original_subject VARCHAR(255) NOT NULL,
    payload BYTEA NOT NULL,
    error_reason TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_stalled_messages_agent ON toro_core.stalled_messages(agent_did);

-- +goose Down
DROP TABLE toro_core.stalled_messages;
