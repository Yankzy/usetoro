-- +goose Up
-- +goose StatementBegin

-- Stripe Checkout Sessions: tracks hosted payment page state
CREATE TABLE toro_core.stripe_checkout_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id        UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    stripe_session_id TEXT NOT NULL UNIQUE,
    amount_cents     BIGINT NOT NULL,
    product_name     TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'PENDING',
    metadata         JSONB DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stripe_checkout_entity ON toro_core.stripe_checkout_sessions(entity_id);
CREATE INDEX idx_stripe_checkout_status ON toro_core.stripe_checkout_sessions(status);

-- Financial Connection Attempts: logs each bank link initiation
CREATE TABLE toro_core.financial_connection_attempts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id          UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    fc_session_id      TEXT NOT NULL,
    stripe_customer_id TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'INITIATED',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fc_attempts_entity ON toro_core.financial_connection_attempts(entity_id);
CREATE INDEX idx_fc_attempts_session ON toro_core.financial_connection_attempts(fc_session_id);

-- Linked Bank Accounts: stores accounts connected via Financial Connections
CREATE TABLE toro_core.linked_bank_accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id          UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    fc_session_id      TEXT NOT NULL,
    stripe_account_id  TEXT NOT NULL UNIQUE,
    institution_name   TEXT NOT NULL,
    last4              TEXT,
    subcategory        TEXT,
    status             TEXT NOT NULL,
    metadata           JSONB DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_linked_accounts_entity ON toro_core.linked_bank_accounts(entity_id);
CREATE INDEX idx_linked_accounts_session ON toro_core.linked_bank_accounts(fc_session_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS toro_core.linked_bank_accounts;
DROP TABLE IF EXISTS toro_core.financial_connection_attempts;
DROP TABLE IF EXISTS toro_core.stripe_checkout_sessions;
-- +goose StatementEnd
