-- +goose Up
-- +goose StatementBegin
ALTER TABLE toro_core.conversations ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE toro_core.conversations DROP COLUMN IF EXISTS role;
-- +goose StatementEnd
