#!/usr/bin/env bash
# Bring up the reversibleStaticMigration integration environment: two
# cp-server clusters (plaintext) joined by one bidirectional cluster link,
# rev-link, created on both clusters with consumer offset sync on and the
# single * LOCAL_MIRROR group filter; rev.a and rev.b mirrored source -> dest
# and ACTIVE; group g-rev committed part way on rev.a on the source; and a
# second link, rev-half, created on dest only.
#
# Idempotent / safe to re-run: creation tolerates "already exists", the link
# configs are re-applied, and readiness is polled (no fixed sleeps).
#
# Usage: ./setup.sh   then   go test -tags e2e ./integration-tests/migplan-reversible/ -v
set -euo pipefail
cd "$(dirname "$0")"

SRC_CID='6ub6fPVJRzKjE4i-REkq-A'
DST_CID='LKsbYRvfTM-TVXKjdjgdxA'
SRC_REST='http://localhost:18190'
DST_REST='http://localhost:28190'
SRC_CONTAINER=kcp-migplan-rev-source
LINK=rev-link
HALF_LINK=rev-half
TOPICS=(rev.a rev.b)
SYNC_CONFIGS='{"data":[{"name":"consumer.offset.sync.enable","value":"true"},{"name":"consumer.offset.sync.ms","value":"5000"},{"name":"consumer.offset.group.filters","value":"{\"groupFilters\":[{\"name\":\"*\",\"patternType\":\"LITERAL\",\"filterType\":\"INCLUDE\",\"topicTypes\":[\"LOCAL_MIRROR\"]}]}"}]}'

echo "==> docker compose up"
docker compose up -d

wait_rest() {
  local name="$1" cid="$2" rest="$3"
  echo "==> waiting for $name REST v3 ($rest)"
  curl -sf --retry 60 --retry-delay 2 --retry-all-errors \
    "$rest/kafka/v3/clusters/$cid" >/dev/null
}
wait_rest source "$SRC_CID" "$SRC_REST"
wait_rest dest   "$DST_CID" "$DST_REST"

echo "==> creating source topics"
for t in "${TOPICS[@]}"; do
  docker exec "$SRC_CONTAINER" kafka-topics --bootstrap-server localhost:29092 \
    --create --if-not-exists --topic "$t" --partitions 3 --replication-factor 1
done

# create_link <rest> <cluster id> <link> <remote cluster id> <remote bootstrap>
# cp-server answers an existing link with 400 "already exists" (not 409);
# tolerate exactly that.
create_link() {
  local rest="$1" cid="$2" link="$3" remote="$4" bootstrap="$5" resp code body
  echo "==> creating bidirectional link $link on $cid (remote $remote at $bootstrap)"
  resp=$(curl -s -w $'\n%{http_code}' -X POST -H 'Content-Type: application/json' \
    "$rest/kafka/v3/clusters/$cid/links?link_name=$link" \
    -d "{\"remote_cluster_id\":\"$remote\",\"configs\":[{\"name\":\"link.mode\",\"value\":\"BIDIRECTIONAL\"},{\"name\":\"bootstrap.servers\",\"value\":\"$bootstrap\"}]}")
  code=$(tail -n1 <<<"$resp"); body=$(sed '$d' <<<"$resp")
  if [[ "$code" != "200" && "$code" != "201" && "$code" != "409" ]] \
     && ! { [[ "$code" == "400" ]] && grep -qi 'already exists' <<<"$body"; }; then
    echo "unexpected status $code creating link $link on $cid: $body" >&2; exit 1
  fi
}

# Link creation is asynchronous: wait until the link answers on that cluster.
wait_link() {
  local rest="$1" cid="$2" link="$3"
  echo "==> waiting for $link to be queryable on $cid"
  curl -sf --retry 60 --retry-delay 2 --retry-all-errors \
    "$rest/kafka/v3/clusters/$cid/links/$link" >/dev/null
}

set_sync() {
  local rest="$1" cid="$2" link="$3" code
  code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' \
    "$rest/kafka/v3/clusters/$cid/links/$link/configs:alter" -d "$SYNC_CONFIGS")
  [[ "$code" == "200" || "$code" == "204" ]] || { echo "unexpected status $code altering $link configs on $cid" >&2; exit 1; }
}

create_link "$DST_REST" "$DST_CID" "$LINK" "$SRC_CID" source:29092
create_link "$SRC_REST" "$SRC_CID" "$LINK" "$DST_CID" dest:39092
wait_link "$DST_REST" "$DST_CID" "$LINK"
wait_link "$SRC_REST" "$SRC_CID" "$LINK"
echo "==> offset sync on, * LOCAL_MIRROR filter, on both copies"
set_sync "$DST_REST" "$DST_CID" "$LINK"
set_sync "$SRC_REST" "$SRC_CID" "$LINK"

echo "==> creating mirror topics on dest"
for t in "${TOPICS[@]}"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
    -d "{\"source_topic_name\":\"$t\"}")
  if [[ "$code" != "200" && "$code" != "201" && "$code" != "400" && "$code" != "409" ]]; then
    echo "unexpected status $code creating mirror $t" >&2; exit 1
  fi
done

echo "==> polling mirrors until all ACTIVE on dest"
ready=false
for _ in $(seq 1 60); do
  active=$(curl -s "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
    | grep -o '"mirror_status":"ACTIVE"' | wc -l | tr -d ' ')
  echo "    ACTIVE mirrors: $active/${#TOPICS[@]}"
  [[ "$active" == "${#TOPICS[@]}" ]] && { ready=true; break; }
  sleep 2
done
[[ "$ready" == true ]] || { echo "mirrors did not all reach ACTIVE" >&2; exit 1; }

echo "==> producing 100 records to rev.a and committing g-rev part way"
seq 1 100 | docker exec -i "$SRC_CONTAINER" kafka-console-producer --bootstrap-server localhost:29092 --topic rev.a
docker exec "$SRC_CONTAINER" kafka-console-consumer --bootstrap-server localhost:29092 \
  --topic rev.a --group g-rev --max-messages 50 --from-beginning >/dev/null

# rev-half exists on dest only; the source keeps just the nameless placeholder
# that creating it on dest leaves.
create_link "$DST_REST" "$DST_CID" "$HALF_LINK" "$SRC_CID" source:29092
wait_link "$DST_REST" "$DST_CID" "$HALF_LINK"
set_sync "$DST_REST" "$DST_CID" "$HALF_LINK"

echo "==> ready"
