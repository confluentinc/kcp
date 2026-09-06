#!/bin/bash
# Brings up the five-broker consumer-group discovery matrix and prepares the
# streams-type cell (enable the streams.version feature on 4.2, then compile and
# launch a Streams app — there is no console tool for streams groups).
set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE="$SCRIPT_DIR/docker-compose.yml"

echo "Starting consumer-group-scan brokers (AK 3.7/3.8/4.0/4.1/4.2)..."
docker compose -f "$COMPOSE" up -d

wait_broker() {
  local cn="$1" host="$2" max=150 w=0
  echo "Waiting for $cn..."
  until docker exec "$cn" /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server "$host:29092" >/dev/null 2>&1; do
    [ $w -ge $max ] && { echo "Timeout waiting for $cn"; docker logs --tail 80 "$cn" || true; exit 1; }
    sleep 3; w=$((w + 3))
  done
  echo "$cn ready."
}
wait_broker kcp-cg-kafka-37 kafka-37
wait_broker kcp-cg-kafka-38 kafka-38
wait_broker kcp-cg-kafka-40 kafka-40
wait_broker kcp-cg-kafka-41 kafka-41
wait_broker kcp-cg-kafka-42 kafka-42

# --- streams (KIP-1071) prep on the 4.2 broker ---
# Streams groups are production-ready in AK 4.2, but the streams.version feature
# must be finalized before the broker serves STREAMS_GROUP_HEARTBEAT.
echo "Enabling streams.version feature on kafka-42..."
docker exec kcp-cg-kafka-42 /opt/kafka/bin/kafka-features.sh \
  --bootstrap-server kafka-42:29092 upgrade --feature streams.version=1

# The Streams app needs its source topic to exist before it starts.
docker exec kcp-cg-kafka-42 /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka-42:29092 --create --if-not-exists \
  --topic orders --partitions 3 --replication-factor 1 >/dev/null 2>&1 || true

# Compile the Streams app against the broker's own client libraries, then launch
# it against the 4.2 host listener so it forms a real streams-type group.
echo "Compiling + launching the Streams app for the streams-type cell..."
rm -rf "$SCRIPT_DIR/klibs" "$SCRIPT_DIR/classes"
mkdir -p "$SCRIPT_DIR/klibs" "$SCRIPT_DIR/classes"
docker cp kcp-cg-kafka-42:/opt/kafka/libs/. "$SCRIPT_DIR/klibs/" >/dev/null
javac -cp "$SCRIPT_DIR/klibs/*" -d "$SCRIPT_DIR/classes" "$SCRIPT_DIR/StreamsDemo.java"
nohup java -cp "$SCRIPT_DIR/classes:$SCRIPT_DIR/klibs/*" StreamsDemo localhost:39096 streams-grp \
  >"$SCRIPT_DIR/streams-app.log" 2>&1 &
echo $! >"$SCRIPT_DIR/streams-app.pid"
echo "Streams app launched (pid $(cat "$SCRIPT_DIR/streams-app.pid"))."

echo "Environment ready: 3.7=:39092 3.8=:39093 4.0=:39094 4.1=:39095 4.2=:39096"
