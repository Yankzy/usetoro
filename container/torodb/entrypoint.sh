#!/bin/bash
set -e

echo "Starting AlloyDB Omni Engine..."
docker-entrypoint.sh postgres \
    -c wal_level=logical \
    -c max_replication_slots=5 \
    -c max_wal_senders=5 \
    -c max_connections=200 &

PG_PID=$!

echo "Waiting for AlloyDB Omni to accept connections..."
until pg_isready -h localhost -U "${POSTGRES_USER:-toro}" > /dev/null 2>&1; do
    if ! kill -0 $PG_PID 2>/dev/null; then
        echo "ERROR: AlloyDB Omni process died on startup!"
        wait $PG_PID
        exit 1
    fi
    sleep 1
done

echo "AlloyDB Omni is ready. Starting ToroDB Stream Engine..."
exec /usr/local/bin/torodb
