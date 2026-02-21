-- +goose NO TRANSACTION
-- +goose Up
-- Enable TimescaleDB
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- =========================================================================
-- Telemetry Events (Hypertable, partitioned by time)
-- entity_id replaces the old tenant_id
-- =========================================================================
CREATE TABLE telemetry_events (
    time        TIMESTAMPTZ NOT NULL,
    trace_id    UUID NOT NULL,
    entity_id   UUID,               -- The toro_core.entities node this event belongs to
    service     TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    duration_ms INT,
    meta        JSONB
);

-- Convert to Hypertable (Partition by time)
SELECT create_hypertable('telemetry_events', 'time');

-- =========================================================================
-- Agent Performance (Aggregated View)
-- =========================================================================
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

-- Refresh Policy (Keep the view updated every 30 mins)
SELECT add_continuous_aggregate_policy('agent_performance_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '30 minutes');

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS agent_performance_hourly;
DROP TABLE IF EXISTS telemetry_events;
