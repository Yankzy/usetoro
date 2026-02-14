#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "🔨 Building store-webhook-secret for Linux..."
cd "$PROJECT_ROOT/go"
GOOS=linux GOARCH=amd64 go build -o ../bin/store-webhook-secret ./cmd/store-webhook-secret/main.go

echo "📦 Copying binary to gate container..."
docker cp "$PROJECT_ROOT/bin/store-webhook-secret" container-gate-1:/tmp/

echo "🚀 Executing inside gate container..."
echo ""
docker exec -it container-gate-1 /tmp/store-webhook-secret
