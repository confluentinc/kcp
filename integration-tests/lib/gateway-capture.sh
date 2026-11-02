#!/usr/bin/env bash
# Evidence for gateway hot-reload misses, collected while a suite runs.
#
# A missed hot reload leaves every gateway pod on the old configId although the CR
# has moved on. kcp only sees that the pods never changed, and by the time a wait
# times out the evidence is gone: CFK's canary is a short-lived pod and the suite
# tears the cluster down. This records, for the whole run:
#
#   timeline.tsv   CR configId/generation next to each pod's applied configId
#   pods.log       every pod phase change (canary pods included), timestamped
#   events.log     every Kubernetes event (Unschedulable, OOMKilling, probe failures)
#   operator.log   the CFK operator's log
#   pod-*.log      each gateway and canary pod's log, followed from first sight
#   describe-*     each canary pod described when first seen and again shortly after
#   final-*        cluster state when the run ends, including node allocations
#
# Source it, then call gateway_capture_start before the tests and gateway_capture_stop
# after. Nothing here may fail the run, so every command is allowed to fail.

_GWC_PIDS=""

_gwc_k() { kubectl --context "${_GWC_CTX}" -n "${_GWC_NS}" "$@"; }
_gwc_now() { date -u +%H:%M:%S; }

# _gwc_loop FILE CMD... — run CMD, appending to FILE; if it ends (a dropped watch),
# start it again.
_gwc_loop() {
  local out="$1"; shift
  while :; do
    "$@" >> "${out}" 2>&1 || true
    sleep 2
  done
}

_gwc_stamp() { while IFS= read -r line; do printf '%s %s\n' "$(_gwc_now)" "${line}"; done; }

_gwc_applied() {
  kubectl --context "${_GWC_CTX}" get --raw \
    "/api/v1/namespaces/${_GWC_NS}/pods/$1:9180/proxy/config" 2>/dev/null |
    sed -n 's/.*"configId":"\([^"]*\)".*/\1/p'
}

_gwc_timeline() {
  local last="" now="" tick=0 want pod got
  printf 'time\tcr_configId\tgeneration\tobservedGeneration\tpods(name=appliedConfigId)\n' > "${_GWC_DIR}/timeline.tsv"
  while :; do
    want="$(_gwc_k get gateway "${_GWC_GW}" -o jsonpath='{.spec.configId}{"\t"}{.metadata.generation}{"\t"}{.status.observedGeneration}' 2>/dev/null)"
    now="${want}"
    for pod in $(_gwc_k get pods -o name 2>/dev/null | sed 's|pod/||' | grep "^${_GWC_GW}-" | grep -v canary); do
      got="$(_gwc_applied "${pod}")"
      now="${now}	${pod}=${got:-?}"
    done
    # Record a change at once, and an unchanged state every 10s so a stall shows as a long run of rows.
    if [ "${now}" != "${last}" ] || [ $((tick % 5)) -eq 0 ]; then
      printf '%s\t%s\n' "$(_gwc_now)" "${now}" >> "${_GWC_DIR}/timeline.tsv"
      last="${now}"
    fi
    tick=$((tick + 1))
    sleep 2
  done
}

# Follow every gateway and canary pod's log from the moment it appears. Canary pods
# live seconds, so they are described at once and again a few seconds later.
_gwc_follow_pods() {
  local seen=" " pod
  while :; do
    for pod in $(_gwc_k get pods -o name 2>/dev/null | sed 's|pod/||' | grep "^${_GWC_GW}-"); do
      case "${seen}" in *" ${pod} "*) continue ;; esac
      seen="${seen}${pod} "
      (
        for _ in 1 2 3 4 5; do
          _gwc_k logs -f --all-containers --timestamps "${pod}" >> "${_GWC_DIR}/pod-${pod}.log" 2>&1 && break
          sleep 1
        done
      ) &
      _gwc_k describe pod "${pod}" > "${_GWC_DIR}/describe-${pod}-first.txt" 2>&1 || true
      ( sleep 8; _gwc_k describe pod "${pod}" > "${_GWC_DIR}/describe-${pod}-later.txt" 2>&1 || true ) &
    done
    sleep 1
  done
}

# gateway_capture_start CONTEXT NAMESPACE GATEWAY_NAME OUTDIR
gateway_capture_start() {
  _GWC_CTX="$1"; _GWC_NS="$2"; _GWC_GW="$3"; _GWC_DIR="$4"
  mkdir -p "${_GWC_DIR}" 2>/dev/null || return 0
  echo "Capturing gateway hot-reload evidence into ${_GWC_DIR}"

  _gwc_loop "${_GWC_DIR}/events.log" _gwc_k get events -w \
    -o 'custom-columns=LAST:.lastTimestamp,TYPE:.type,REASON:.reason,OBJECT:.involvedObject.name,MESSAGE:.message' &
  _GWC_PIDS="${_GWC_PIDS} $!"; disown "$!" 2>/dev/null || true
  ( _gwc_k get pods -w -o wide 2>&1 | _gwc_stamp >> "${_GWC_DIR}/pods.log" ) &
  _GWC_PIDS="${_GWC_PIDS} $!"; disown "$!" 2>/dev/null || true
  _gwc_loop "${_GWC_DIR}/operator.log" _gwc_k logs -f --timestamps --since=10m deploy/confluent-operator &
  _GWC_PIDS="${_GWC_PIDS} $!"; disown "$!" 2>/dev/null || true
  _gwc_timeline &
  _GWC_PIDS="${_GWC_PIDS} $!"; disown "$!" 2>/dev/null || true
  _gwc_follow_pods &
  _GWC_PIDS="${_GWC_PIDS} $!"; disown "$!" 2>/dev/null || true
}

# gateway_capture_stop — end the streams and take a last snapshot of the cluster.
gateway_capture_stop() {
  [ -n "${_GWC_DIR:-}" ] || return 0
  local pid
  for pid in ${_GWC_PIDS}; do
    pkill -P "${pid}" 2>/dev/null || true
    kill "${pid}" 2>/dev/null || true
  done
  _GWC_PIDS=""
  # Followed `kubectl logs` children belong to subshells of the follower; reap leftovers.
  pkill -f "kubectl --context ${_GWC_CTX} -n ${_GWC_NS} logs -f" 2>/dev/null || true

  _gwc_k get gateway "${_GWC_GW}" -o yaml > "${_GWC_DIR}/final-gateway-cr.yaml" 2>&1 || true
  _gwc_k get pods -o wide > "${_GWC_DIR}/final-pods.txt" 2>&1 || true
  _gwc_k get events --sort-by=.lastTimestamp > "${_GWC_DIR}/final-events.txt" 2>&1 || true
  kubectl --context "${_GWC_CTX}" describe nodes > "${_GWC_DIR}/final-nodes.txt" 2>&1 || true
  kubectl --context "${_GWC_CTX}" top nodes > "${_GWC_DIR}/final-top-nodes.txt" 2>&1 || true
  kubectl --context "${_GWC_CTX}" top pods -A > "${_GWC_DIR}/final-top-pods.txt" 2>&1 || true
}
