#!/bin/bash
# Waits for Kafka to be ready (runs commands inside the container)

CONTAINER_NAME=$1
INTERNAL_BOOTSTRAP="${2:-localhost:29092}"
MAX_WAIT=60
WAIT_TIME=0

echo "Waiting for Kafka in container $CONTAINER_NAME at $INTERNAL_BOOTSTRAP..."

while [ $WAIT_TIME -lt $MAX_WAIT ]; do
    if docker exec $CONTAINER_NAME kafka-broker-api-versions --bootstrap-server $INTERNAL_BOOTSTRAP > /dev/null 2>&1; then
        echo "Kafka is ready!"
        exit 0
    fi

    echo "Kafka not ready yet, waiting... ($WAIT_TIME/$MAX_WAIT seconds)"
    sleep 2
    WAIT_TIME=$((WAIT_TIME + 2))
done

echo "Timeout waiting for Kafka"
exit 1
