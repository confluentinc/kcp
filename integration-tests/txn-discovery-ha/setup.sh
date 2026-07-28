#!/bin/bash
# Start the three-broker cluster for the txn-discovery leader-failover suite.
#
# Self-contained by design, like the single-node suite: the Go suite (build tag
# e2e_txndiscovery_ha) creates every topic and produces every transaction
# in-process with sarama, so there are no seed scripts here and nothing to keep
# in sync with the assertions.
#
# This script only waits for the three brokers to answer. Waiting for the
# internal topics to reach full ISR is the Go suite's job, because those topics
# do not exist until the first transaction is produced — the coordinators create
# them on first use, not at boot.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINERS=(
    kcp-test-txn-discovery-ha-kafka1
    kcp-test-txn-discovery-ha-kafka2
    kcp-test-txn-discovery-ha-kafka3
)

echo "Starting txn-discovery HA cluster (3 brokers)..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" up -d

echo "Waiting for all three brokers to be ready..."
MAX_WAIT=180
WAIT_TIME=0
while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    READY=0
    for c in "${CONTAINERS[@]}"; do
        if docker exec "$c" kafka-broker-api-versions --bootstrap-server localhost:9092 > /dev/null 2>&1; then
            READY=$((READY + 1))
        fi
    done
    if [ $READY -eq ${#CONTAINERS[@]} ]; then
        echo "All $READY brokers are ready!"
        break
    fi
    echo "$READY/${#CONTAINERS[@]} brokers ready, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 3
    WAIT_TIME=$((WAIT_TIME + 3))
done

if [ $WAIT_TIME -ge $MAX_WAIT ]; then
    echo "Timeout waiting for the cluster"
    docker compose -f "$SCRIPT_DIR/docker-compose.yml" logs --tail 100
    exit 1
fi

# A broker answering its own api-versions is not yet a formed cluster: the
# controller quorum has to have elected a leader before a replicated topic can be
# created at all. Asking one broker to describe the cluster is the cheapest proof
# that it can see its peers.
echo "Waiting for the cluster to report three brokers..."
QUORUM_WAIT=0
while [ $QUORUM_WAIT -lt 60 ]; do
    COUNT=$(docker exec "${CONTAINERS[0]}" kafka-broker-api-versions --bootstrap-server localhost:9092 2>/dev/null | grep -c "id: " || true)
    if [ "$COUNT" -ge 3 ]; then
        echo "Cluster reports $COUNT brokers."
        break
    fi
    echo "Cluster reports $COUNT/3 brokers, waiting... ($QUORUM_WAIT/60 seconds)"
    sleep 3
    QUORUM_WAIT=$((QUORUM_WAIT + 3))
done

echo ""
echo "Environment is ready."
echo "  Plaintext bootstrap: localhost:29192,localhost:29193,localhost:29194"
