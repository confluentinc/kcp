#!/bin/bash
# Run kcp scan self-managed-connectors against Kafka Connect.
# Assumes setup.sh has already been run (base Kafka cluster).

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$ROOT_DIR"

echo ""
echo "=========================================="
echo "  Kafka Connect Scan Test"
echo "=========================================="

# Start Kafka Connect with the profile
echo "Starting Kafka Connect..."
cd "$SCRIPT_DIR"
docker compose --profile connect up -d
cd "$ROOT_DIR"

# Wait for Connect to be ready
echo "Waiting for Kafka Connect to be ready..."
MAX_WAIT=60
WAIT_TIME=0

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if curl -s http://localhost:8083/ > /dev/null 2>&1; then
        echo "Kafka Connect is ready!"
        break
    fi
    echo "Connect not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

if [ $WAIT_TIME -ge $MAX_WAIT ]; then
    echo "ERROR: Kafka Connect failed to start within $MAX_WAIT seconds"
    exit 1
fi

# Create test connector with retry (Connect may need time after REST is up
# to finish its internal group rebalance before accepting connectors)
echo "Creating test connector..."
CONN_WAIT=0
CONN_MAX=60
CONNECTOR_CREATED=false
while [ $CONN_WAIT -lt $CONN_MAX ]; do
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8083/connectors \
        -H "Content-Type: application/json" \
        -d '{
          "name": "test-heartbeat",
          "config": {
            "connector.class": "org.apache.kafka.connect.mirror.MirrorHeartbeatConnector",
            "tasks.max": "1",
            "source.cluster.alias": "source",
            "target.cluster.alias": "target",
            "source.cluster.bootstrap.servers": "apache-kafka:29092",
            "target.cluster.bootstrap.servers": "apache-kafka:29092"
          }
        }' 2>/dev/null)
    if [ "$HTTP_CODE" = "201" ] || [ "$HTTP_CODE" = "200" ]; then
        echo "Connector created! (HTTP $HTTP_CODE)"
        CONNECTOR_CREATED=true
        break
    fi
    echo "Connector creation returned HTTP $HTTP_CODE, retrying... ($CONN_WAIT/$CONN_MAX seconds)"
    sleep 2
    CONN_WAIT=$((CONN_WAIT + 2))
done

if [ "$CONNECTOR_CREATED" != "true" ]; then
    echo "ERROR: Failed to create connector within ${CONN_MAX}s"
    echo "Connect REST response: $(curl -s http://localhost:8083/connectors 2>/dev/null)"
    exit 1
fi

# Create a state file for the test
STATE="test-state-kafka-connect.json"

# First, run a basic Apache Kafka scan to populate the state file with the cluster
echo "Populating state file with Apache Kafka cluster..."
./kcp scan clusters --source-type apache-kafka \
    --credentials-file integration-tests/apache-kafka-scan/credentials/kafka-plaintext.yaml \
    --state-file "$STATE"

# Now scan for self-managed connectors
echo ""
echo "Running: ./kcp scan self-managed-connectors --state-file $STATE --connect-rest-url http://localhost:8083 --cluster-id apache-kafka --use-unauthenticated"
./kcp scan self-managed-connectors \
    --state-file "$STATE" \
    --connect-rest-url http://localhost:8083 \
    --cluster-id apache-kafka \
    --use-unauthenticated

echo ""
echo "Results:"
TOPIC_COUNT=$(jq '.apache_kafka_sources.clusters[0].kafka_admin_client_information.topics.details | length' "$STATE")
ACL_COUNT=$(jq '.apache_kafka_sources.clusters[0].kafka_admin_client_information.acls | length' "$STATE")
CONNECTOR_COUNT=$(jq '.apache_kafka_sources.clusters[0].kafka_admin_client_information.self_managed_connectors.connectors | length // 0' "$STATE")
CONNECTOR_HOST_COUNT=$(jq '[.apache_kafka_sources.clusters[0].kafka_admin_client_information.self_managed_connectors.connectors[]? | select(.connect_host != null and .connect_host != "")] | length' "$STATE")
echo "  Topics: $TOPIC_COUNT, ACLs: $ACL_COUNT, Connectors: $CONNECTOR_COUNT (with connect_host populated: $CONNECTOR_HOST_COUNT)"

# Real assertions — fail loudly if the REST scanner regresses or stops
# populating ConnectHost (which the UI's per-host grouping depends on).
if [ "$CONNECTOR_COUNT" -le 0 ]; then
    echo "ERROR: expected at least one self-managed connector in state, found $CONNECTOR_COUNT"
    exit 1
fi
if [ "$CONNECTOR_HOST_COUNT" -le 0 ]; then
    echo "ERROR: connectors are present but none have connect_host populated; the UI's per-Connect-host grouping will break"
    exit 1
fi
echo ""

# ---- Connect metrics collection via Jolokia ----
echo ""
echo "Testing Connect metrics collection via Jolokia..."

# Wait for Jolokia to be ready on the Connect worker
echo "Waiting for Jolokia on Connect worker (port 8781)..."
JOLOKIA_WAIT=0
JOLOKIA_MAX=30
while [ $JOLOKIA_WAIT -lt $JOLOKIA_MAX ]; do
    if curl -s http://localhost:8781/jolokia/version > /dev/null 2>&1; then
        echo "Jolokia is ready on Connect worker!"
        break
    fi
    sleep 2
    JOLOKIA_WAIT=$((JOLOKIA_WAIT + 2))
done

if [ $JOLOKIA_WAIT -ge $JOLOKIA_MAX ]; then
    echo "WARNING: Jolokia not ready on Connect worker within ${JOLOKIA_MAX}s, skipping metrics test"
else
    echo "Running: ./kcp scan self-managed-connectors with --metrics jolokia"
    ./kcp scan self-managed-connectors \
        --state-file "$STATE" \
        --connect-rest-url http://localhost:8083 \
        --cluster-id apache-kafka \
        --use-unauthenticated \
        --metrics jolokia \
        --metrics-duration 30s \
        --metrics-interval 10s \
        --credentials-file integration-tests/apache-kafka-scan/credentials/connect-jolokia.yaml

    METRICS_COUNT=$(jq '.apache_kafka_sources.clusters[0].kafka_admin_client_information.self_managed_connectors.metrics.results | length // 0' "$STATE")
    echo "  Connect metrics data points: $METRICS_COUNT"
    if [ "$METRICS_COUNT" -le 0 ]; then
        echo "ERROR: expected at least one Connect metrics data point, found $METRICS_COUNT"
        exit 1
    fi
    echo "  Connect metrics collection test passed!"
fi

echo ""

# Clean up Connect (leave base Kafka running for potential other tests)
echo "Stopping Kafka Connect..."
cd "$SCRIPT_DIR"
docker compose --profile connect down
cd "$ROOT_DIR"

echo "Kafka Connect scan test passed!"
