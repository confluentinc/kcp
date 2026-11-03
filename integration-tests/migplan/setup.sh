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
SRC_TOPICS=(team-a.orders team-a.payments billing-v2 team-b.audit resume.switchonly)
MIRRORS=(team-a.orders team-a.payments billing-v2)   # NOTE: team-b.audit is intentionally NOT mirrored
# resume.switchonly is mirrored then PROMOTED to a durable STOPPED, feeding the
# resume verdicts: SWITCH-ONLY (stopped + route still on source) and, via the
# gateway-resume-unchanged.yaml fixture, UNCHANGED (stopped + route already on
# target). Seeded after the ACTIVE poll below so it never perturbs the 3-ACTIVE
# invariant the happy-path test asserts. (AWAIT-STOPPED needs a durable
# PENDING_STOPPED, which is a transient the engine tier cannot seed reliably —
# it is covered by the reconcile unit tests and the idempotent-fsm live suite.)
STOPPED_TOPIC=resume.switchonly

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
# An already-existing link is reported as 400 by cp-server (not 409), with an
# "already exists" message — same quirk as the duplicate-mirror case below. Keep
# setup.sh idempotent by tolerating that specific 400, but nothing else.
resp=$(curl -s -w $'\n%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  "$DST_REST/kafka/v3/clusters/$DST_CID/links?link_name=$LINK" \
  -d "{\"source_cluster_id\":\"$SRC_CID\",\"configs\":[{\"name\":\"bootstrap.servers\",\"value\":\"source:29092\"}]}")
code=$(tail -n1 <<<"$resp"); body=$(sed '$d' <<<"$resp")
if [[ "$code" != "200" && "$code" != "201" && "$code" != "409" ]] \
   && ! { [[ "$code" == "400" ]] && grep -qi 'already exists' <<<"$body"; }; then
  echo "unexpected status $code creating link: $body" >&2; exit 1
fi

# Link creation above only means the request was accepted — propagation through
# the broker's controller is asynchronous. On a warm local Docker this always
# won the race against the immediately-following mirror creation below, but on
# a cold CI pull it can lose: the first mirror POST 404s ("link not found")
# before the link is queryable yet. Wait for it the same way wait_rest does.
echo "==> waiting for cluster link $LINK to be queryable"
curl -sf --retry 60 --retry-delay 2 --retry-all-errors \
  "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK" >/dev/null

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
ready=false
for _ in $(seq 1 60); do
  active=$(curl -s "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
    | grep -o '"mirror_status":"ACTIVE"' | wc -l | tr -d ' ')
  echo "    ACTIVE mirrors: $active/${#MIRRORS[@]}"
  [[ "$active" == "${#MIRRORS[@]}" ]] && { ready=true; break; }
  sleep 2
done
[[ "$ready" == true ]] || { echo "mirrors did not all reach ACTIVE" >&2; exit 1; }

# --- Seed one durable STOPPED mirror for the resume verdict tests ---
# Mirror STOPPED_TOPIC, wait for it to catch up (ACTIVE), then promote it. On a
# tiny idle topic promote reaches STOPPED near-instantly, so this is reliable
# (unlike PENDING_STOPPED, which needs an in-flight promote to be interrupted).
echo "==> mirroring $STOPPED_TOPIC then promoting it to STOPPED"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors" \
  -d "{\"source_topic_name\":\"$STOPPED_TOPIC\"}")
[[ "$code" == "200" || "$code" == "201" || "$code" == "400" || "$code" == "409" ]] \
  || { echo "unexpected status $code creating mirror $STOPPED_TOPIC" >&2; exit 1; }
# wait for it to be ACTIVE (caught up) before promoting
for _ in $(seq 1 30); do
  st=$(curl -s "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors/$STOPPED_TOPIC" \
    | grep -o '"mirror_status":"[^"]*"' | cut -d'"' -f4)
  [[ "$st" == "ACTIVE" || "$st" == "STOPPED" ]] && break
  sleep 1
done
# promote (idempotent: an already-STOPPED mirror just stays STOPPED)
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
  "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors:promote" \
  -d "{\"mirror_topic_names\":[\"$STOPPED_TOPIC\"]}"
for _ in $(seq 1 30); do
  st=$(curl -s "$DST_REST/kafka/v3/clusters/$DST_CID/links/$LINK/mirrors/$STOPPED_TOPIC" \
    | grep -o '"mirror_status":"[^"]*"' | cut -d'"' -f4)
  echo "    $STOPPED_TOPIC: $st"
  [[ "$st" == "STOPPED" ]] && { echo "==> ready"; exit 0; }
  sleep 1
done
echo "$STOPPED_TOPIC did not reach STOPPED" >&2
exit 1
