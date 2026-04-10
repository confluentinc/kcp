#!/bin/bash
# Run kcp scan against all auth methods and metrics backends on the OSK scan broker.
# Assumes setup.sh has already been run.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$ROOT_DIR"

echo ""
echo "=========================================="
echo "  OSK Scan Tests (OSK scan broker)"
echo "=========================================="

# ── Kafka auth methods ─────────────────────────────────────────────────────────
for method in plaintext sasl tls sasl-ssl; do
    echo ""
    echo "========================================"
    echo "  TEST: $method"
    echo "========================================"

    CREDS="integration-tests/osk-scan/credentials/kafka-${method}.yaml"
    STATE="test-state-osk-${method}.json"

    echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE"
    ./kcp scan clusters --source-type osk \
        --credentials-file "$CREDS" \
        --state-file "$STATE"

    echo "Results:"
    jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
    echo ""
done

# ── JMX / Jolokia metrics ──────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-noauth (Jolokia metrics)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/jmx-noauth.yaml"
STATE="test-state-osk-jmx-noauth.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus metrics ─────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-noauth (Prometheus metrics)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/prometheus-noauth.yaml"
STATE="test-state-osk-prometheus-noauth.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── JMX / Jolokia with auth ──────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-auth (Jolokia with auth)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/jmx-auth.yaml"
STATE="test-state-osk-jmx-auth.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── JMX / Jolokia with TLS ───────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-tls (Jolokia with TLS + auth)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/jmx-tls.yaml"
STATE="test-state-osk-jmx-tls.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus with auth ──────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-auth (Prometheus with auth)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/prometheus-auth.yaml"
STATE="test-state-osk-prometheus-auth.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus with TLS ───────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-tls (Prometheus with TLS + auth)"
echo "========================================"

CREDS="integration-tests/osk-scan/credentials/prometheus-tls.yaml"
STATE="test-state-osk-prometheus-tls.json"

echo "Running: ./kcp scan clusters --source-type osk --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type osk \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.osk_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

echo "All OSK scan tests passed!"
