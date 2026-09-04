#!/usr/bin/env bash
# Tear the migplan integration environment down, removing volumes so the next
# setup.sh starts from a clean state.
set -euo pipefail
cd "$(dirname "$0")"
echo "==> docker compose down -v"
docker compose down -v
