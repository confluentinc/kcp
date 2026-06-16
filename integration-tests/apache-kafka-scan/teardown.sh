#!/bin/bash
# Tear down the Apache Kafka scan broker and clean up artifacts.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "Tearing down Apache Kafka scan broker..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" down -v 2>/dev/null || true

# Clean up artifacts
rm -rf "$SCRIPT_DIR/certs"
rm -f "$ROOT_DIR"/test-state-apache-kafka-*.json
rm -f "$ROOT_DIR/kcp.log"

echo "Teardown complete."
