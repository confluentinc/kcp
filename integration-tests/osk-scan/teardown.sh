#!/bin/bash
# Tear down the OSK scan broker and clean up artifacts.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "Tearing down OSK scan broker..."
# --profile connect ensures profile-gated services (kafka-connect) are torn
# down too. Without it, `docker compose down` only stops services with no
# profile assigned, leaving the Connect container behind when run-connect.sh
# fails partway through.
docker compose -f "$SCRIPT_DIR/docker-compose.yml" --profile connect down -v 2>/dev/null || true

# Clean up artifacts
rm -rf "$SCRIPT_DIR/certs"
rm -f "$ROOT_DIR"/test-state-osk-*.json
rm -f "$ROOT_DIR/kcp.log"

echo "Teardown complete."
