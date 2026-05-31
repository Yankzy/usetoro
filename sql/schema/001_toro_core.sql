-- +goose NO TRANSACTION
-- +goose Up
-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- =========================================================================
-- SCHEMA: toro_core
-- The Nervous System: Hierarchy, Users, Memory, and Internal Ledger
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS toro_core;

-- =========================================================================
-- 1. Entities (The Hierarchy: Apex CPA -> Sub CPA -> Client)
-- =========================================================================
CREATE TABLE toro_core.entities (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    parent_id     UUID REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    name          VARCHAR(255) NOT NULL,
    entity_type   VARCHAR(50) NOT NULL,    -- 'apex_cpa', 'sub_cpa', 'client'
    erp_provider  VARCHAR(50),             -- 'qbo', 'netsuite', 'sage', NULL
    erp_tenant_id VARCHAR(255),            -- The ID of this entity inside the ERP
    plan_tier     TEXT DEFAULT 'basic',    -- 'basic', 'pro', 'enterprise'
    status        TEXT DEFAULT 'active',   -- 'active', 'suspended', 'archived'
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_entities_parent ON toro_core.entities(parent_id);
CREATE INDEX idx_entities_status ON toro_core.entities(status);
CREATE INDEX idx_entities_type   ON toro_core.entities(entity_type);

-- =========================================================================
-- 2. Updated_at Trigger Function (shared across toro_core tables)
-- =========================================================================
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION toro_core.update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE 'plpgsql';
-- +goose StatementEnd

CREATE TRIGGER update_entities_updated_at
    BEFORE UPDATE ON toro_core.entities
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 3. Users (The Humans, scoped to an Entity)
-- =========================================================================
CREATE TABLE toro_core.users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,           -- Argon2id hash
    full_name     TEXT,
    role          TEXT DEFAULT 'member',   -- 'owner', 'admin', 'member'
    user_type     TEXT NOT NULL DEFAULT 'standard', -- Extended from 012
    is_active     BOOLEAN DEFAULT TRUE,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_users_email     ON toro_core.users(email);
CREATE INDEX idx_users_entity_id ON toro_core.users(entity_id);

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON toro_core.users
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 4. Refresh Tokens (Long-lived sessions)
-- =========================================================================
CREATE TABLE toro_core.refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES toro_core.users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    ip_address TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- =========================================================================
-- 5. Webhook Provider Connections
-- =========================================================================
CREATE TABLE toro_core.webhooks_providerconnection (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    connection_id  TEXT NOT NULL,
    webhook_secret TEXT NOT NULL,
    is_active      BOOLEAN DEFAULT true,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    updated_at     TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(connection_id)
);

CREATE INDEX idx_provider_connection_id ON toro_core.webhooks_providerconnection(connection_id);

-- =========================================================================
-- 6. Agent Memory Rules (Scoped to QBO realm)
-- =========================================================================
CREATE TABLE toro_core.agent_memory_rules (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id     TEXT NOT NULL,
    entity_type  TEXT NOT NULL,   -- e.g., "vendor", "description"
    entity_value TEXT NOT NULL,   -- e.g., "Home Depot", "Uber"
    instruction  TEXT NOT NULL,   -- e.g., "Always categorize as 'Repairs'"
    source       TEXT DEFAULT 'user_correction', -- 'user_correction', 'admin_override'
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(realm_id, entity_type, entity_value)
);

CREATE INDEX idx_memory_realm_lookup ON toro_core.agent_memory_rules(realm_id, entity_value);

-- =========================================================================
-- 7. Internal Toro Transactions (NOT ERP data)
-- =========================================================================
CREATE TABLE toro_core.transactions (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    external_id   TEXT NOT NULL,
    amount_micros BIGINT NOT NULL,
    description   TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(entity_id, external_id)
);

ALTER TABLE toro_core.transactions ENABLE ROW LEVEL SECURITY;

CREATE POLICY entity_isolation_policy ON toro_core.transactions
    FOR ALL
    USING (entity_id = current_setting('app.current_entity')::UUID)
    WITH CHECK (entity_id = current_setting('app.current_entity')::UUID);

-- =========================================================================
-- 8. ERP Connections (from 002)
-- =========================================================================
CREATE TABLE IF NOT EXISTS toro_core.erp_connections (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    erp_system    TEXT NOT NULL, -- e.g. 'quickbooks_online', 'netsuite'
    realm_id      TEXT NOT NULL,
    access_token  TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,

    -- CDC Sync Tracking
    last_sync_timestamp      TIMESTAMPTZ,

    -- Webhook Event Timestamps (event-driven CDC)
    last_webhook_account     TIMESTAMPTZ,
    last_webhook_vendor      TIMESTAMPTZ,
    last_webhook_customer    TIMESTAMPTZ,
    last_webhook_invoice     TIMESTAMPTZ,
    last_webhook_bill        TIMESTAMPTZ,
    last_webhook_transaction TIMESTAMPTZ,
    last_webhook_deposit     TIMESTAMPTZ,          -- (from 002 shadow_erp)
    last_webhook_payment     TIMESTAMPTZ,          -- (from 023)
    last_webhook_sales_receipt TIMESTAMPTZ,        -- (from 023)

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(erp_system, realm_id),
    UNIQUE(entity_id)
);

CREATE INDEX IF NOT EXISTS idx_erp_realm_id   ON toro_core.erp_connections(realm_id);
CREATE INDEX IF NOT EXISTS idx_erp_entity_id  ON toro_core.erp_connections(entity_id);
CREATE INDEX IF NOT EXISTS idx_erp_system     ON toro_core.erp_connections(erp_system);

COMMENT ON COLUMN toro_core.erp_connections.last_sync_timestamp IS 'Timestamp of last successful CDC sync. Used as changedSince parameter for CDC API calls. Max lookback is 30 days per QBO limits.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_account  IS 'Timestamp of last successful Account webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_vendor   IS 'Timestamp of last successful Vendor webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_customer IS 'Timestamp of last successful Customer webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_invoice  IS 'Timestamp of last successful Invoice webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_bill     IS 'Timestamp of last successful Bill webhook. Used by CDC to fetch only missed events.';
COMMENT ON COLUMN toro_core.erp_connections.last_webhook_transaction IS 'Timestamp of last successful Purchase/Transaction webhook. Used by CDC to fetch only missed events.';

-- =========================================================================
-- 9. Team Invites (from 014)
-- =========================================================================
CREATE TABLE toro_core.team_invites (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token VARCHAR(8) UNIQUE NOT NULL,
    email VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL,
    firm_name VARCHAR(255) NOT NULL,
    is_used BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);

-- =========================================================================
-- 10. Analytics & Telemetry (from 004)
-- =========================================================================
CREATE TABLE toro_core.telemetry_events (
    time        TIMESTAMPTZ NOT NULL,
    trace_id    UUID NOT NULL,
    entity_id   UUID,               -- The toro_core.entities node this event belongs to
    service     TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    duration_ms INT,
    meta        JSONB
);

-- Convert to Hypertable (Partition by time)
SELECT create_hypertable('toro_core.telemetry_events', 'time');

-- Agent Performance (Aggregated View)
CREATE MATERIALIZED VIEW toro_core.agent_performance_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    meta->>'agent_id' AS agent_id,
    COUNT(*) AS total_tasks,
    AVG((meta->>'confidence')::float) AS avg_confidence,
    SUM((meta->>'cost_usd')::float) AS total_cost
FROM toro_core.telemetry_events
WHERE event_type = 'agent_decision'
GROUP BY bucket, agent_id;

-- Refresh Policy (Keep the view updated every 30 mins)
SELECT add_continuous_aggregate_policy('toro_core.agent_performance_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes');

-- =========================================================================
-- 11. Wallet & Micrion Ledger (from 005)
-- =========================================================================

-- The Wallet Master Record
CREATE TABLE toro_core.wallet (
    id                       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id                UUID NOT NULL UNIQUE REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    total_purchased_micrions BIGINT NOT NULL DEFAULT 0,
    total_burned_micrions    BIGINT NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ DEFAULT NOW(),
    updated_at               TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_wallet_updated_at
    BEFORE UPDATE ON toro_core.wallet
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- The Transactions Ledger (Immutable Audit Log)
-- `stripe_session_id` tracks Fiat On-Ramps.
-- `nats_revision` tracks high-speed burn rollups (The Two Generals idempontency lock).
CREATE TABLE toro_core.wallet_transactions (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    wallet_id         UUID NOT NULL REFERENCES toro_core.wallet(id) ON DELETE CASCADE,
    transaction_type  VARCHAR(50) NOT NULL, -- 'purchase' or 'burn'
    micrion_amount    BIGINT NOT NULL,      -- Positive for purchase, positive for burn (contextualized by type)
    usd_amount        BIGINT,               -- Only populated on 'purchase' (in cents)
    stripe_session_id TEXT,                 -- Only populated on 'purchase'
    nats_revision     BIGINT,               -- Only populated on 'burn' (The CAS idempotency lock)
    created_at        TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for fast querying
CREATE INDEX idx_wallet_transactions_wallet ON toro_core.wallet_transactions(wallet_id);

-- Idempotency Constraints!
-- Ensure we never double-charge a Stripe Session
CREATE UNIQUE INDEX idx_wallet_txn_stripe_unique 
    ON toro_core.wallet_transactions(stripe_session_id) 
    WHERE stripe_session_id IS NOT NULL;

-- Ensure we never double-charge a NATS execution rollup! (The cross-database atomic safety lock)
CREATE UNIQUE INDEX idx_wallet_txn_nats_unique 
    ON toro_core.wallet_transactions(wallet_id, nats_revision) 
    WHERE nats_revision IS NOT NULL;

-- =========================================================================
-- 12. Stalled Messages (Paywall DLQ, from 006)
-- =========================================================================
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

-- =========================================================================
-- 13. Workflows & History (from 007)
-- =========================================================================

-- Workflows (The Redux Engine Base State)
CREATE TABLE toro_core.workflows (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID REFERENCES toro_core.entities(id) ON DELETE CASCADE,
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

-- Workflow History (The LLM Chat State)
CREATE TABLE toro_core.workflow_history (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workflow_id   UUID NOT NULL REFERENCES toro_core.workflows(id) ON DELETE CASCADE,
    role          TEXT NOT NULL,    -- e.g., 'user', 'assistant', 'system', 'tool'
    content       JSONB NOT NULL,   -- The explicit OpenAI content matrix / function calls 
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workflow_history_workflow_id ON toro_core.workflow_history(workflow_id);

-- =========================================================================
-- 14. Workflow Blueprints (from 010)
-- =========================================================================

CREATE TABLE IF NOT EXISTS toro_core.workflow_blueprints (
    name          TEXT PRIMARY KEY,
    trigger_topic TEXT NOT NULL,
    definition    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workflow_blueprints_trigger_topic
    ON toro_core.workflow_blueprints(trigger_topic);

-- Attach the standard toro_core updated_at trigger
DROP TRIGGER IF EXISTS update_workflow_blueprints_updated_at ON toro_core.workflow_blueprints;
CREATE TRIGGER update_workflow_blueprints_updated_at
    BEFORE UPDATE ON toro_core.workflow_blueprints
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 15. Conversations (from 012 + 026 postmark cols + 027 role col)
-- =========================================================================
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
    role          TEXT NOT NULL DEFAULT 'user',  -- (from 027)
    -- Postmark delivery status tracking (from 026)
    delivered     JSONB,
    bounced       JSONB,
    opened        JSONB,
    clicked       JSONB,
    complained    JSONB,
    session_id    UUID,                    -- FK added after conversation_sessions is created below
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_conversations_external_id ON toro_core.conversations(external_id);
CREATE INDEX idx_conversations_entity_id ON toro_core.conversations(entity_id);
CREATE INDEX idx_conversations_source ON toro_core.conversations(source);
CREATE INDEX idx_conversations_in_reply_to ON toro_core.conversations(in_reply_to);

-- =========================================================================
-- 16. Conversation Sessions (from 025)
-- =========================================================================
-- Groups messages into durable threads that survive across long pauses
-- (hours to months). The general agent uses these to load full context
-- before each response.
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

-- Now add the FK from conversations to conversation_sessions
ALTER TABLE toro_core.conversations
    ADD CONSTRAINT fk_conversations_session
    FOREIGN KEY (session_id) REFERENCES toro_core.conversation_sessions(id) ON DELETE SET NULL;

CREATE INDEX idx_conv_sessions_entity        ON toro_core.conversation_sessions(entity_id);
CREATE INDEX idx_conv_sessions_participant   ON toro_core.conversation_sessions(entity_id, participant_handle);
CREATE INDEX idx_conv_sessions_status        ON toro_core.conversation_sessions(status);
CREATE INDEX idx_conv_sessions_last_activity ON toro_core.conversation_sessions(last_activity_at);
CREATE INDEX idx_conversations_session       ON toro_core.conversations(session_id);

-- Attach updated_at trigger
CREATE TRIGGER update_conv_sessions_updated_at
    BEFORE UPDATE ON toro_core.conversation_sessions
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 17. Thread Mappings (from 028)
-- =========================================================================
-- Bridges email and Slack threads bidirectionally.
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

-- =========================================================================
-- 18. Slack Tenant Mappings (from 029)
-- =========================================================================
CREATE TABLE toro_core.slack_tenant_mappings (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id           UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    slack_team_id       TEXT NOT NULL UNIQUE,
    slack_access_token  TEXT NOT NULL,
    slack_bot_user_id   TEXT NOT NULL,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_slack_mapping_tenant ON toro_core.slack_tenant_mappings(tenant_id);

CREATE TRIGGER update_slack_mapping_updated_at
    BEFORE UPDATE ON toro_core.slack_tenant_mappings
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 19. Scheduled Jobs (from 030)
-- =========================================================================
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
DROP TRIGGER IF EXISTS update_slack_mapping_updated_at ON toro_core.slack_tenant_mappings;
DROP TABLE IF EXISTS toro_core.slack_tenant_mappings;
DROP TABLE IF EXISTS toro_core.toro_threads_mappings;
DROP TRIGGER IF EXISTS update_conv_sessions_updated_at ON toro_core.conversation_sessions;
DROP INDEX IF EXISTS idx_conversations_session;
DROP INDEX IF EXISTS idx_conv_sessions_last_activity;
DROP INDEX IF EXISTS idx_conv_sessions_status;
DROP INDEX IF EXISTS idx_conv_sessions_participant;
DROP INDEX IF EXISTS idx_conv_sessions_entity;
ALTER TABLE toro_core.conversations DROP CONSTRAINT IF EXISTS fk_conversations_session;
DROP TABLE IF EXISTS toro_core.conversation_sessions;
DROP TABLE IF EXISTS toro_core.conversations;
DROP TRIGGER IF EXISTS update_workflow_blueprints_updated_at ON toro_core.workflow_blueprints;
DROP TABLE IF EXISTS toro_core.workflow_blueprints;
DROP TABLE IF EXISTS toro_core.workflow_history;
DROP TRIGGER IF EXISTS update_workflows_updated_at ON toro_core.workflows;
DROP TABLE IF EXISTS toro_core.workflows;
DROP TABLE IF EXISTS toro_core.stalled_messages;
DROP TABLE IF EXISTS toro_core.wallet_transactions;
DROP TABLE IF EXISTS toro_core.wallet;
DROP MATERIALIZED VIEW IF EXISTS toro_core.agent_performance_hourly;
DROP TABLE IF EXISTS toro_core.telemetry_events;
DROP TABLE IF EXISTS toro_core.team_invites;
DROP TABLE IF EXISTS toro_core.erp_connections;
DROP TABLE IF EXISTS toro_core.transactions;
DROP TABLE IF EXISTS toro_core.agent_memory_rules;
DROP TABLE IF EXISTS toro_core.webhooks_providerconnection;
DROP TABLE IF EXISTS toro_core.refresh_tokens;
DROP TABLE IF EXISTS toro_core.users;
DROP TABLE IF EXISTS toro_core.entities;
DROP FUNCTION IF EXISTS toro_core.update_updated_at_column();
DROP SCHEMA IF EXISTS toro_core CASCADE;
