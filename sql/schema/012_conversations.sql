-- +goose Up
CREATE TABLE toro_core.conversations (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,           -- 'email', 'whatsapp', 'sms', 'slack', 'discord'
    external_id   TEXT UNIQUE NOT NULL,    -- Postmark MessageID, etc.
    from_handle   TEXT NOT NULL,           -- email address, phone number, etc.
    to_handle     TEXT NOT NULL,           -- recipient email, etc.
    reply_to      TEXT,                    -- Reply-To header
    in_reply_to   TEXT,                    -- In-Reply-To header (for threading)
    subject       TEXT,
    body_text     TEXT,
    body_html     TEXT,
    stripped_text TEXT,                    -- Useful for AI chat (removes email signatures/quotes)
    metadata      JSONB DEFAULT '{}',      -- attachments, raw headers, etc.
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_conversations_external_id ON toro_core.conversations(external_id);
CREATE INDEX idx_conversations_entity_id ON toro_core.conversations(entity_id);
CREATE INDEX idx_conversations_source ON toro_core.conversations(source);
CREATE INDEX idx_conversations_in_reply_to ON toro_core.conversations(in_reply_to);

-- +goose Down
DROP TABLE IF EXISTS toro_core.conversations;
