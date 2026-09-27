#!/usr/bin/env bash
# Compiles the idempotent-fsm resume suite (and a linux kcp binary for execute's
# subprocess in U9) for the cluster node's architecture, ships them plus the
# rendered manifests into the runner pod, and executes the test binary there.
#
# The runner pod is deployed by setup.sh, not here: this script only builds,
# copies, and execs. The suite cannot run on the developer's machine — the engine
# reads the live gateway CR with in-cluster config and kcp verifies a hot reload
# by dialling each gateway pod's IP, which is not routable from the host under the
# minikube docker driver.
#
# Security: the destination SASL password is delivered to the pod ONLY via the
# tbm-rest-credentials Secret -> pod-spec env (see kcp-runner.yaml). It is NEVER
# put on this kubectl exec argv. Only non-secret KCP_TBM_* vars are passed here.
#
# Every run's full output — build steps plus each test's before/after world
# snapshots and raw kcp runs — is also saved to
# .reports/<date>-<time>-<mode>[-<selector>].log (gitignored).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
RENDERED_DIR="${SCRIPT_DIR}/.rendered"
ENV_FILE="${SCRIPT_DIR}/.env"

[ -f "${ENV_FILE}" ] || { echo "FATAL: ${ENV_FILE} not found — run setup.sh first" >&2; exit 1; }
# shellcheck disable=SC1090
set -a; . "${ENV_FILE}"; set +a

PROFILE="${KCP_TBM_KUBE_CONTEXT}"
NAMESPACE="${KCP_TBM_NAMESPACE}"
RUNNER="${KCP_TBM_KCP_POD}"

# Optional test selector: `run.sh 'TestFoo'` -> -test.run TestFoo, or export
# GOTEST_FLAGS for anything else. -test.v is always on.
RUN_SELECTOR=""
[ -n "${1:-}" ] && RUN_SELECTOR="-test.run ${1}"
GOTEST_FLAGS="${GOTEST_FLAGS:-}"

REPORTS_DIR="${SCRIPT_DIR}/.reports"
REPORT_SUFFIX=""
[ -n "${1:-}" ] && REPORT_SUFFIX="-$(printf '%s' "${1}" | tr -c 'A-Za-z0-9_.-' '_' | cut -c1-60)"
REPORT="${REPORTS_DIR}/$(date +%Y-%m-%d-%H%M%S)-${KCP_TBM_GATEWAY_MODE}${REPORT_SUFFIX}.log"
mkdir -p "${REPORTS_DIR}"

main() {
  # main runs inside the tee pipeline below, where the caller's errexit is off;
  # turn it back on so a failed step still stops the run.
  set -euo pipefail
  TEST_BIN="${SCRIPT_DIR}/.idempotent-fsm-e2e.test"
  KCP_BIN="${SCRIPT_DIR}/.kcp-linux"
  # Clean the host-side build artefacts whatever the outcome; the test's exit code
  # is preserved because this trap runs no `exit`.
  trap 'rm -f "${TEST_BIN}" "${KCP_BIN}"' EXIT

  # Match the node, not the host: the binaries run in a pod. GOTOOLCHAIN=auto lets
  # go fetch the toolchain pinned in .go-version when the local goenv lacks it.
  NODE_ARCH="$(kubectl --context "${PROFILE}" get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
  echo "Node architecture: ${NODE_ARCH}"

  echo "Compiling the idempotent-fsm e2e suite for linux/${NODE_ARCH}..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go test -c -tags e2e -o "${TEST_BIN}" ./integration-tests/idempotent-fsm/
  )

  echo "Building the linux kcp binary (execute subprocess) ..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go build -ldflags "-X github.com/confluentinc/kcp/internal/build_info.Version=0.0.0-localdev -X github.com/confluentinc/kcp/internal/build_info.Commit=unknown -X github.com/confluentinc/kcp/internal/build_info.Date=unknown" \
      -o "${KCP_BIN}" .
  )

  echo "Waiting for the runner pod ${RUNNER} to be Ready..."
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod/"${RUNNER}" --timeout=120s

  echo "Copying binaries and rendered manifests into the runner..."
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${TEST_BIN}" "${RUNNER}:/workspace/idempotent-fsm-e2e.test"
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- chmod +x /workspace/idempotent-fsm-e2e.test
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${KCP_BIN}" "${RUNNER}:/workspace/kcp"
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- chmod +x /workspace/kcp

  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- mkdir -p /workspace/rendered
  for f in "${RENDERED_DIR}"/*; do
    [ -f "${f}" ] || continue
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${f}" "${RUNNER}:/workspace/rendered/$(basename "${f}")"
  done

  echo ""
  echo "=== Running the idempotent-fsm suite in-cluster ==="
  # Only non-secret KCP_TBM_* are passed on the exec line. KCP_TBM_RENDERED_DIR is
  # rewritten to the in-pod path. The destination SASL user/password are already in
  # the pod env from the tbm-rest-credentials Secret and are deliberately absent here.
  # shellcheck disable=SC2086
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- env \
    KCP_TBM_NAMESPACE="${KCP_TBM_NAMESPACE}" \
    KCP_TBM_RENDERED_DIR="/workspace/rendered" \
    KCP_TBM_GATEWAY_MODE="${KCP_TBM_GATEWAY_MODE}" \
    KCP_TBM_GATEWAY_NAME="${KCP_TBM_GATEWAY_NAME}" \
    KCP_TBM_ROUTE_NAME="${KCP_TBM_ROUTE_NAME}" \
    KCP_TBM_SOURCE_DOMAIN="${KCP_TBM_SOURCE_DOMAIN}" \
    KCP_TBM_SOURCE_BOOTSTRAP_ID="${KCP_TBM_SOURCE_BOOTSTRAP_ID}" \
    KCP_TBM_DEST_DOMAIN="${KCP_TBM_DEST_DOMAIN}" \
    KCP_TBM_SOURCE_BOOTSTRAP="${KCP_TBM_SOURCE_BOOTSTRAP}" \
    KCP_TBM_DEST_BOOTSTRAP="${KCP_TBM_DEST_BOOTSTRAP}" \
    KCP_TBM_REST_ENDPOINT="${KCP_TBM_REST_ENDPOINT}" \
    KCP_TBM_DEST_CLUSTER_ID="${KCP_TBM_DEST_CLUSTER_ID}" \
    KCP_TBM_CLUSTER_LINK_NAME="${KCP_TBM_CLUSTER_LINK_NAME}" \
    KCP_TBM_TOPIC_PREFIX="${KCP_TBM_TOPIC_PREFIX}" \
    /workspace/idempotent-fsm-e2e.test -test.v ${RUN_SELECTOR} ${GOTEST_FLAGS}
}

# Output goes to the terminal and the report; the exit code is main's.
set +e
main 2>&1 | tee "${REPORT}"
status=${PIPESTATUS[0]}
set -e
echo "Report saved: ${REPORT}"
exit "${status}"
