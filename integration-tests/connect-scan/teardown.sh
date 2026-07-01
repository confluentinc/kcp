#!/usr/bin/env bash
# Tear down the Connect scan env.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Stopping Connect env..."
docker compose down -v
echo "Connect env stopped."
