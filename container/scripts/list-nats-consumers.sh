#!/usr/bin/env bash
# list-nats-consumers.sh
#
# Lists all JetStream durable consumers across every stream, showing pending
# and ack-pending counts so you can spot stalled state at a glance.
#
# Usage:
#   ./container/scripts/list-nats-consumers.sh          # all streams
#   ./container/scripts/list-nats-consumers.sh TASKS    # specific stream
#
# Requires: stack running with nats-1 port 8222 exposed on localhost.

set -euo pipefail

FILTER="${1:-}"  # optional stream name filter (e.g. TASKS, WORKFLOWS)

curl -sf "http://localhost:8222/jsz?consumers=true" | python3 - "$FILTER" <<'EOF'
import json, sys

data = json.load(sys.stdin)
filter_stream = sys.argv[1] if len(sys.argv) > 1 else ""

total_consumers = 0
total_stalled   = 0

for acc in data.get("account_details", []):
    for stream in acc.get("stream_detail", []):
        name = stream.get("name", "?")
        if filter_stream and name != filter_stream:
            continue

        consumers = stream.get("consumer_detail", [])
        msgs      = stream.get("state", {}).get("messages", 0)

        print(f"\n┌─ stream: {name}  ({msgs} msgs, {len(consumers)} consumers)")

        if not consumers:
            print("│  (no consumers)")
            continue

        for c in sorted(consumers, key=lambda x: x["name"]):
            pending     = c.get("num_pending", 0)
            ack_pending = c.get("num_ack_pending", 0)
            waiting     = c.get("num_waiting", 0)
            delivered   = c.get("delivered", {}).get("consumer_seq", 0)
            durable     = c.get("config", {}).get("durable_name", "")
            flag        = " ⚠️  STALLED" if (pending > 0 or ack_pending > 0) else ""
            total_consumers += 1
            if flag:
                total_stalled += 1
            print(
                f"│  {c['name']}"
                f"\n│    durable={durable or '(none)'}  delivered={delivered}"
                f"  pending={pending}  ack_pending={ack_pending}  waiting={waiting}{flag}"
            )

print(f"\n── total: {total_consumers} consumers, {total_stalled} stalled ──\n")
EOF
