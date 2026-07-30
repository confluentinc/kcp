#!/bin/bash
# Create the SCRAM users the Phase-2 link-auth tests authenticate as.
# Run after the source broker is up (the cluster-link metadata it needs is
# created lazily; SCRAM creds just need the broker accepting admin requests).
set -e

echo "Waiting for source broker (non-empty cluster id)..."
for i in $(seq 1 40); do
  CID=$(curl -s http://localhost:18090/kafka/v3/clusters 2>/dev/null \
    | python3 -c 'import sys,json;d=json.load(sys.stdin)["data"];print(d[0]["cluster_id"] if d else "")' 2>/dev/null || true)
  [ -n "$CID" ] && break
  sleep 3
done
[ -n "$CID" ] || { echo "source broker not ready"; exit 1; }

echo "Creating SCRAM users (kcp)..."
docker exec kcp-mcl-source kafka-configs --bootstrap-server localhost:29092 \
  --alter --add-config 'SCRAM-SHA-256=[password=kcp-secret]' --entity-type users --entity-name kcp
docker exec kcp-mcl-source kafka-configs --bootstrap-server localhost:29092 \
  --alter --add-config 'SCRAM-SHA-512=[password=kcp-secret]' --entity-type users --entity-name kcp
echo "SCRAM users ready."
