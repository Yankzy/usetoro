#!/bin/bash
set -e

echo "Starting AlloyDB Omni Engine..."
# Start the AlloyDB Postgres process in the background using the standard entrypoint
docker-entrypoint.sh postgres \
    -c wal_level=logical \
    -c max_replication_slots=5 \
    -c max_wal_senders=5 \
    -c max_connections=200 &

# Wait for Postgres to be available before relying on it in Go (handled by Go app, but good practice)
# We just launch the Go app directly since it has connection retries.

echo "Starting ToroDB Stream Engine..."
exec /usr/local/bin/torodb
