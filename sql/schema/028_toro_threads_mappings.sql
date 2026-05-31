-- +goose Up
-- toro_threads_mappings bridges email and Slack threads bidirectionally.
-- When an email initiates a Slack thread (Flow 1), a row is inserted with
-- the Slack parent_ts and email Message-ID. Subsequent replies on either
-- channel update email_latest_message_id to keep the pointer chain current.
CREATE TABLE toro_core.toro_threads_mappings (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id     UUID NOT NULL,
    tenant_id           UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    slack_channel_id    TEXT NOT NULL,
    slack_parent_ts     TEXT NOT NULL,
    email_latest_message_id TEXT NOT NULL,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_thread_map_slack ON toro_core.toro_threads_mappings(slack_channel_id, slack_parent_ts);
CREATE INDEX idx_thread_map_email ON toro_core.toro_threads_mappings(email_latest_message_id);
CREATE INDEX idx_thread_map_tenant ON toro_core.toro_threads_mappings(tenant_id);

-- +goose Down
DROP TABLE IF EXISTS toro_core.toro_threads_mappings;
