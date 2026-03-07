-- +goose Up
-- +goose StatementBegin
CREATE TABLE toro_core.team_invites (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token VARCHAR(8) UNIQUE NOT NULL,
    email VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL,
    firm_name VARCHAR(255) NOT NULL,
    is_used BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE toro_core.team_invites;
-- +goose StatementEnd
