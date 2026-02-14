-- +goose Up
ALTER TABLE qbo_connections 
ADD COLUMN last_sync_timestamp TIMESTAMPTZ DEFAULT NOW() - INTERVAL '30 days';

COMMENT ON COLUMN qbo_connections.last_sync_timestamp IS 
'Timestamp of last successful CDC sync. Used as changedSince parameter for CDC API calls. Max lookback is 30 days per QBO limits.';

-- +goose Down
ALTER TABLE qbo_connections 
DROP COLUMN last_sync_timestamp;
