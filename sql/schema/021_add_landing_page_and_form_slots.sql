-- +goose Up
ALTER TABLE marketing.campaign_steps ADD COLUMN landing_page_id UUID;
ALTER TABLE marketing.campaign_steps ADD COLUMN email_form_id UUID;

ALTER TABLE marketing.email_logs ADD COLUMN landing_page_id UUID;
ALTER TABLE marketing.email_logs ADD COLUMN email_form_id UUID;

-- +goose Down
ALTER TABLE marketing.campaign_steps DROP COLUMN landing_page_id;
ALTER TABLE marketing.campaign_steps DROP COLUMN email_form_id;

ALTER TABLE marketing.email_logs DROP COLUMN landing_page_id;
ALTER TABLE marketing.email_logs DROP COLUMN email_form_id;
