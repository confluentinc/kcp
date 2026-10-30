#!/usr/bin/env bash
# Dumps what a failed e2e run needs to explain itself but the tests' own reports
# leave out: the CFK operator log, every Gateway CR with its status conditions
# (hot-reload-status says whether CFK's hot-reload canary passed), each gateway
# pod's log and served /config, any canary pod still around, the node's resource
# pressure and the namespace's events.
#
# Usage: dump-diagnostics.sh <kube-context> <namespace> <out-dir>
#
# Best effort by design: it runs from CI epilogues before teardown, where a
# missing cluster or a half-started one must not fail the epilogue. Secrets are
# never read; the gateway licence reaches the pods through a secretKeyRef, so
# describe output names the Secret but not its value.
set -uo pipefail

CTX="${1:?kube context}"
NS="${2:?namespace}"
OUT="${3:?out dir}"

mkdir -p "${OUT}"

k() { kubectl --context "${CTX}" --request-timeout=30s "$@"; }

if ! k get --raw /readyz >/dev/null 2>&1; then
  echo "cluster ${CTX} is not reachable; nothing to dump" | tee "${OUT}/unreachable.txt"
  exit 0
fi

{ nproc; free -m; docker stats --no-stream 2>/dev/null; } > "${OUT}/host.txt" 2>&1
k describe nodes > "${OUT}/nodes.txt" 2>&1
k -n "${NS}" get pods -o wide > "${OUT}/pods.txt" 2>&1
k -n "${NS}" get events --sort-by=.lastTimestamp -o wide > "${OUT}/events.txt" 2>&1
k -n "${NS}" get gateway -o yaml > "${OUT}/gateways.yaml" 2>&1

k -n "${NS}" logs deploy/confluent-operator --timestamps > "${OUT}/operator.log" 2>&1
k -n "${NS}" logs deploy/confluent-operator --timestamps --previous > "${OUT}/operator-previous.log" 2>/dev/null \
  || rm -f "${OUT}/operator-previous.log"

for gw in $(k -n "${NS}" get gateway -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
  for pod in $(k -n "${NS}" get pods -l "app=${gw}" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    k -n "${NS}" logs "${pod}" --all-containers --timestamps > "${OUT}/${pod}.log" 2>&1
    k -n "${NS}" logs "${pod}" --all-containers --timestamps --previous > "${OUT}/${pod}-previous.log" 2>/dev/null \
      || rm -f "${OUT}/${pod}-previous.log"
    k get --raw "/api/v1/namespaces/${NS}/pods/${pod}:9180/proxy/config" > "${OUT}/${pod}-config.json" 2>&1
  done
  # A canary pod only outlives its hot reload when it never became ready, which
  # is exactly the case worth describing.
  for pod in $(k -n "${NS}" get pods -o name 2>/dev/null | grep "^pod/${gw}-canary-"); do
    k -n "${NS}" describe "${pod}" > "${OUT}/${pod#pod/}-describe.txt" 2>&1
    k -n "${NS}" logs "${pod}" --all-containers --timestamps > "${OUT}/${pod#pod/}.log" 2>&1
  done
done

echo "diagnostics for ${CTX}/${NS} written to ${OUT}"
