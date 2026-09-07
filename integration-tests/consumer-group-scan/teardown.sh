#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
echo "Tearing down consumer-group-scan..."

# Stop the Streams app (streams-type cell).
if [ -f "$SCRIPT_DIR/streams-app.pid" ]; then
  kill "$(cat "$SCRIPT_DIR/streams-app.pid")" 2>/dev/null || true
  rm -f "$SCRIPT_DIR/streams-app.pid"
fi
pkill -f "StreamsDemo" 2>/dev/null || true

docker compose -f "$SCRIPT_DIR/docker-compose.yml" down -v 2>/dev/null || true

# Remove generated build artifacts.
rm -rf "$SCRIPT_DIR/klibs" "$SCRIPT_DIR/classes"
rm -f "$SCRIPT_DIR/streams-app.log"
echo "Teardown complete."
