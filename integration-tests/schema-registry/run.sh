#!/bin/bash
# Run kcp scan schema-registry against both unauthenticated and basic auth instances.
# Assumes setup.sh has already been run.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$ROOT_DIR"

echo ""
echo "=========================================="
echo "  Schema Registry Scan Tests"
echo "=========================================="

# ── Unauthenticated ──────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo "  TEST: unauthenticated"
echo "========================================"

STATE="test-state-sr-unauth.json"
echo '{}' > "$STATE"

echo "Running: ./kcp scan schema-registry --sr-type confluent --url http://localhost:8081 --use-unauthenticated --state-file $STATE"
./kcp scan schema-registry \
    --sr-type confluent \
    --url http://localhost:8081 \
    --use-unauthenticated \
    --state-file "$STATE"

echo "Results:"
jq -r '.schema_registries.confluent_schema_registry[] | "  \(.url): \(.subjects | length) subjects"' "$STATE"
echo ""

# ── Basic Auth ────────────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo "  TEST: basic-auth"
echo "========================================"

STATE="test-state-sr-basic-auth.json"
echo '{}' > "$STATE"

echo "Running: ./kcp scan schema-registry --sr-type confluent --url http://localhost:8082 --use-basic-auth --username schemauser --password schemapass --state-file $STATE"
./kcp scan schema-registry \
    --sr-type confluent \
    --url http://localhost:8082 \
    --use-basic-auth \
    --username schemauser \
    --password schemapass \
    --state-file "$STATE"

echo "Results:"
jq -r '.schema_registries.confluent_schema_registry[] | "  \(.url): \(.subjects | length) subjects"' "$STATE"
echo ""

# ── Cleanup state files ──────────────────────────────────────────────────────
rm -f test-state-sr-unauth.json test-state-sr-basic-auth.json

echo "All Schema Registry scan tests passed!"
