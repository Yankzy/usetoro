-- +goose Up
-- Add columns to track last successful webhook timestamp per entity type
-- This enables event-driven CDC that only fetches entities changed since last webhook

ALTER TABLE qbo_connections 
ADD COLUMN last_webhook_account TIMESTAMPTZ,
ADD COLUMN last_webhook_vendor TIMESTAMPTZ,
ADD COLUMN last_webhook_customer TIMESTAMPTZ,
ADD COLUMN last_webhook_invoice TIMESTAMPTZ,
ADD COLUMN last_webhook_bill TIMESTAMPTZ;

COMMENT ON COLUMN qbo_connections.last_webhook_account IS 
'Timestamp of last successful Account webhook. Used by CDC to fetch only missed events.';

COMMENT ON COLUMN qbo_connections.last_webhook_vendor IS 
'Timestamp of last successful Vendor webhook. Used by CDC to fetch only missed events.';

COMMENT ON COLUMN qbo_connections.last_webhook_customer IS 
'Timestamp of last successful Customer webhook. Used by CDC to fetch only missed events.';

COMMENT ON COLUMN qbo_connections.last_webhook_invoice IS 
'Timestamp of last successful Invoice webhook. Used by CDC to fetch only missed events.';

COMMENT ON COLUMN qbo_connections.last_webhook_bill IS 
'Timestamp of last successful Bill webhook. Used by CDC to fetch only missed events.';

-- +goose Down
ALTER TABLE qbo_connections 
DROP COLUMN last_webhook_account,
DROP COLUMN last_webhook_vendor,
DROP COLUMN last_webhook_customer,
DROP COLUMN last_webhook_invoice,
DROP COLUMN last_webhook_bill;
