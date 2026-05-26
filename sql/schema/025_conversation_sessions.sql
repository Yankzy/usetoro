-- +goose Up

-- Conversation Sessions group messages into durable threads that survive
-- across long pauses (hours to months). The general agent uses these to
-- load full context before each response.
CREATE TABLE toro_core.conversation_sessions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id           UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    external_id         TEXT,              -- external reference (e.g. email thread ID, Twilio conversation SID)
    source              TEXT NOT NULL,     -- 'email', 'sms', 'whatsapp', 'telegram', 'slack', 'api'
    participant_handle  TEXT NOT NULL,     -- the external person's handle (email, phone, @username)
    toro_handle         TEXT NOT NULL,     -- our handle on this channel (toro email, toro phone, etc.)
    subject             TEXT,              -- conversation topic
    status              TEXT NOT NULL DEFAULT 'active',  -- active, awaiting_reply, resolved, escalated
    system_prompt       TEXT,              -- per-session override of the general agent's system prompt
    context_json        JSONB DEFAULT '{}', -- arbitrary context (chase intent, doc list, deadlines, etc.)
    last_activity_at    TIMESTAMPTZ DEFAULT NOW(),
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- Add session_id to existing conversations table
ALTER TABLE toro_core.conversations
    ADD COLUMN session_id UUID REFERENCES toro_core.conversation_sessions(id) ON DELETE SET NULL;

CREATE INDEX idx_conv_sessions_entity        ON toro_core.conversation_sessions(entity_id);
CREATE INDEX idx_conv_sessions_participant   ON toro_core.conversation_sessions(entity_id, participant_handle);
CREATE INDEX idx_conv_sessions_status        ON toro_core.conversation_sessions(status);
CREATE INDEX idx_conv_sessions_last_activity ON toro_core.conversation_sessions(last_activity_at);
CREATE INDEX idx_conversations_session       ON toro_core.conversations(session_id);

-- Attach updated_at trigger
CREATE TRIGGER update_conv_sessions_updated_at
    BEFORE UPDATE ON toro_core.conversation_sessions
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_conv_sessions_updated_at ON toro_core.conversation_sessions;
DROP INDEX IF EXISTS idx_conversations_session;
DROP INDEX IF EXISTS idx_conv_sessions_last_activity;
DROP INDEX IF EXISTS idx_conv_sessions_status;
DROP INDEX IF EXISTS idx_conv_sessions_participant;
DROP INDEX IF EXISTS idx_conv_sessions_entity;
ALTER TABLE toro_core.conversations DROP COLUMN IF EXISTS session_id;
DROP TABLE IF EXISTS toro_core.conversation_sessions;
