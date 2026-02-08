-- +goose NO TRANSACTION
-- +goose Up
-- Enable Timescale
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- 1. The Event Log (Hypertable)
CREATE TABLE telemetry_events (
    time TIMESTAMPTZ NOT NULL,
    trace_id UUID NOT NULL,
    tenant_id UUID,
    service TEXT NOT NULL,
    event_type TEXT NOT NULL,
    duration_ms INT,
    meta JSONB
);

-- Convert to Hypertable (Partition by time)
SELECT create_hypertable('telemetry_events', 'time');

-- 2. Agent Performance (Aggregated View)
CREATE MATERIALIZED VIEW agent_performance_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    meta->>'agent_id' AS agent_id,
    COUNT(*) AS total_tasks,
    AVG((meta->>'confidence')::float) AS avg_confidence,
    SUM((meta->>'cost_usd')::float) AS total_cost
FROM telemetry_events
WHERE event_type = 'agent_decision'
GROUP BY bucket, agent_id;

-- 3. Refresh Policy (Keep the view updated every 30 mins)
SELECT add_continuous_aggregate_policy('agent_performance_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes');

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS agent_performance_hourly;
-- Note: We don't drop the timescaledb extension as other migrations might use it
DROP TABLE IF EXISTS telemetry_events;