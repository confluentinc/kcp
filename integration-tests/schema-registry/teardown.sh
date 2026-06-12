#!/bin/bash
# Tear down the Schema Registry integration test environment.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "$SCRIPT_DIR"

echo ""
echo "Tearing down Schema Registry environment..."
docker compose -p kcp-test-sr down -v
echo "Schema Registry environment stopped."
