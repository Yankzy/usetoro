#!/usr/bin/env bash
# purge-nats-tasks.sh
#
# Clears all stalled messages and orphaned durable consumers from the TASKS
# JetStream stream. Run this when agents are stuck in a DeliverAll replay loop
# at boot due to previously nacked/unacked messages from dead DID sessions.
#
# Usage: ./container/scripts/purge-nats-tasks.sh
# Requires: Docker running, usetoro stack up (nats-1 must be healthy)
#
# Safe to run with the stack up — the TASKS stream will be recreated on next
# boot by tap/pkg/transport/stream.go (EnsureStream call).

set -euo pipefail

NATS_URL="nats://nats-1:4222"
NATS_BOX="natsio/nats-box:latest"

# Auto-detect the Docker network that nats-1 is attached to
NETWORK=$(docker inspect --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}' \
  "$(docker ps --filter "name=nats-1" --format "{{.ID}}" | head -1)" 2>/dev/null | head -1)

if [[ -z "$NETWORK" ]]; then
  echo "❌ Could not detect Docker network. Is the stack running?"
  exit 1
fi
echo "📡 Using network: $NETWORK"

echo "🔍 Checking stalled consumer state..."
docker run --rm --network "$NETWORK" "$NATS_BOX" \
  nats stream info TASKS -s "$NATS_URL" 2>/dev/null || {
    echo "❌ Cannot reach TASKS stream. Is the stack running?"
    exit 1
  }

echo ""
echo "⚠️  This will DELETE the TASKS stream (messages + all consumers)."
echo "    The stream will be recreated automatically on next boot."
echo "    Active in-flight tasks WILL be lost."
read -rp "Continue? [y/N] " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

echo ""
echo "🗑️  Deleting TASKS stream..."
docker run --rm --network "$NETWORK" "$NATS_BOX" \
  nats stream rm TASKS --force -s "$NATS_URL"

echo "✅ TASKS stream deleted. It will be recreated on next protocol boot."
echo ""
echo "💡 Also cleaning up WORKFLOWS stream consumer orphans..."
# List and delete all dead DID durable consumers from WORKFLOWS stream
CONSUMER_LIST=$(docker run --rm --network "$NETWORK" "$NATS_BOX" \
  nats consumer ls WORKFLOWS -s "$NATS_URL" 2>/dev/null | grep "did-toro-" || true)

if [[ -z "$CONSUMER_LIST" ]]; then
  echo "   No orphaned WORKFLOWS consumers found."
else
  echo "$CONSUMER_LIST" | while read -r consumer; do
    echo "   Deleting consumer: $consumer"
    docker run --rm --network "$NETWORK" "$NATS_BOX" \
      nats consumer rm WORKFLOWS "$consumer" --force -s "$NATS_URL" 2>/dev/null || true
  done
  echo "   ✅ Done."
fi

echo ""
echo "🚀 Restart the stack to resume with a clean state:"
echo "   docker compose -f container/docker-compose.yml restart protocol"
