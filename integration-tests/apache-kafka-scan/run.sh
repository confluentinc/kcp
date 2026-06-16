#!/bin/bash
# Run kcp scan against all auth methods and metrics backends on the Apache Kafka scan broker.
# Assumes setup.sh has already been run.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$ROOT_DIR"

echo ""
echo "=========================================="
echo "  Apache Kafka Scan Tests (Apache Kafka scan broker)"
echo "=========================================="

# ── Kafka auth methods ─────────────────────────────────────────────────────────
for method in plaintext sasl sasl-sha512 sasl-sha512-only tls sasl-ssl sasl-plain; do
    echo ""
    echo "========================================"
    echo "  TEST: $method"
    echo "========================================"

    CREDS="integration-tests/apache-kafka-scan/credentials/kafka-${method}.yaml"
    STATE="test-state-apache-kafka-${method}.json"

    echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE"
    ./kcp scan clusters --source-type apache-kafka \
        --credentials-file "$CREDS" \
        --state-file "$STATE"

    echo "Results:"
    jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
    echo ""
done

# ── JMX / Jolokia metrics ──────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-noauth (Jolokia metrics)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/jmx-noauth.yaml"
STATE="test-state-apache-kafka-jmx-noauth.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus metrics ─────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-noauth (Prometheus metrics)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/prometheus-noauth.yaml"
STATE="test-state-apache-kafka-prometheus-noauth.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── JMX / Jolokia with auth ──────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-auth (Jolokia with auth)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/jmx-auth.yaml"
STATE="test-state-apache-kafka-jmx-auth.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── JMX / Jolokia with TLS ───────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: jmx-tls (Jolokia with TLS + auth)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/jmx-tls.yaml"
STATE="test-state-apache-kafka-jmx-tls.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics jolokia --metrics-duration 10s --metrics-interval 1s"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics jolokia \
    --metrics-duration 10s \
    --metrics-interval 1s

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus with auth ──────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-auth (Prometheus with auth)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/prometheus-auth.yaml"
STATE="test-state-apache-kafka-prometheus-auth.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

# ── Prometheus with TLS ───────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " TEST: prometheus-tls (Prometheus with TLS + auth)"
echo "========================================"

CREDS="integration-tests/apache-kafka-scan/credentials/prometheus-tls.yaml"
STATE="test-state-apache-kafka-prometheus-tls.json"

echo "Running: ./kcp scan clusters --source-type apache-kafka --credentials-file $CREDS --state-file $STATE --metrics prometheus --metrics-range 30d"
./kcp scan clusters --source-type apache-kafka \
    --credentials-file "$CREDS" \
    --state-file "$STATE" \
    --metrics prometheus \
    --metrics-range 30d

echo "Results:"
jq -r '.apache_kafka_sources.clusters[0] | "  Topics: \(.kafka_admin_client_information.topics.details | length), ACLs: \(.kafka_admin_client_information.acls | length), Metrics: \(if .metrics then (.metrics.results | length | tostring) + " data points (" + (.metrics.aggregates | keys | join(", ")) + ")" else "none" end)"' "$STATE"
echo ""

echo "All Apache Kafka scan tests passed!"
