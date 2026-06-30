-- +goose Up
CREATE TABLE fignode.ase_session_email_holds (
    session_id UUID NOT NULL,
    transaction_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, transaction_id)
);

-- +goose Down
DROP TABLE IF EXISTS fignode.ase_session_email_holds;
