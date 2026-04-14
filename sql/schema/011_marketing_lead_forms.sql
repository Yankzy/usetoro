-- +goose Up
CREATE SCHEMA IF NOT EXISTS marketing;

CREATE TABLE marketing.lead_forms (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    website     TEXT NOT NULL,
    form_data   JSONB NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Index for faster lookups by website
CREATE INDEX idx_lead_forms_website ON marketing.lead_forms(website);

-- Update trigger for updated_at
CREATE TRIGGER update_lead_forms_updated_at
    BEFORE UPDATE ON marketing.lead_forms
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TABLE IF EXISTS marketing.lead_forms;
-- We don't drop the schema as it might contain other tables in the future
