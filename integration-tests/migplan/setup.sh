#!/usr/bin/env bash
# Bring up the migplan integration environment and provision it to the exact
# state the e2e tests expect: two cp-server clusters (plaintext), a
# destination-initiated cluster link, and three ACTIVE mirror topics. The
# un-mirrored source topic team-b.audit is left as a fail-fast (F3) case.
#
# Idempotent / safe to re-run: topic + link + mirror creation all tolerate
# "already exists" responses, and readiness is polled (no fixed sleeps).
#
# Usage: ./setup.sh   then   go test -tags e2e ./integration-tests/migplan/ -v
set -euo pipefail
cd "$(dirname "$0")"

SRC_CID='6ub6fPVJRzKjE4i-REkq-A'
DST_CID='LKsbYRvfTM-TVXKjdjgdxA'
SRC_REST='http://localhost:18090'
DST_REST='http://localhost:28090'
LINK='migplan-link'
SRC_TOPICS=(team-a.orders team-a.payments billing-v2 team-b.audit)
MIRRORS=(team-a.orders team-a.payments billing-v2)   # NOTE: team-b.audit is intentionally NOT mirrored

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
for t in "${SRC_TOPICS[@]}"; do
  docker exec kcp-migplan-source kafka-topics --bootstrap-server localhost:29092 \
    --create --if-not-exists --topic "$t" --partitions 1 --replication-factor 1
done

echo "==> creating cluster link $LINK on dest (reaches source at source:29092)"
# 409 if the link already exists — tolerate it.
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  "$DST_REST/kafka/v3/clusters/$DST_CID/links?link_name=$LINK" \
  -d "{\"source_cluster_id\":\"$SRC_CID\",\"configs\":[{\"name\":\"bootstrap.servers\",\"value\":\"source:29092\"}]}")
if [[ "$code" != "200" && "$code" != "201" && "$code" != "409" ]]; then
  echo "unexpected status $code creating link" >&2; exit 1
fi

echo "==> creating mirror topics"
for t in "${MIRRORS[@]}"; do
  # the request field is source_topic_name; 400/409 if it already exists.
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
    -d "{\"source_topic_name\":\"$t\"}")
  if [[ "$code" != "200" && "$code" != "201" && "$code" != "400" && "$code" != "409" ]]; then
    echo "unexpected status $code creating mirror $t" >&2; exit 1
  fi
done

echo "==> polling mirrors until all ACTIVE"
for _ in $(seq 1 60); do
  active=$(curl -s "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
    | grep -o '"mirror_status":"ACTIVE"' | wc -l | tr -d ' ')
  echo "    ACTIVE mirrors: $active/${#MIRRORS[@]}"
  [[ "$active" == "${#MIRRORS[@]}" ]] && { echo "==> ready"; exit 0; }
  sleep 2
done
echo "mirrors did not all reach ACTIVE" >&2
exit 1
