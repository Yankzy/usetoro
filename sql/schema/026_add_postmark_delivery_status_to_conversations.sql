-- +goose Up
ALTER TABLE toro_core.conversations
    ADD COLUMN delivered JSONB,
    ADD COLUMN bounced JSONB,
    ADD COLUMN opened JSONB,
    ADD COLUMN clicked JSONB,
    ADD COLUMN complained JSONB;

-- +goose Down
ALTER TABLE toro_core.conversations
    DROP COLUMN IF EXISTS delivered,
    DROP COLUMN IF EXISTS bounced,
    DROP COLUMN IF EXISTS opened,
    DROP COLUMN IF EXISTS clicked,
    DROP COLUMN IF EXISTS complained;
