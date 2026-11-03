#!/usr/bin/env bash
# Compiles the retries suite (and the linux kcp binary the tests run as a subprocess)
# for the cluster node's architecture, ships them plus the rendered manifests into the
# runner pod, and executes the test binary there. The suite cannot run on the
# developer's machine: kcp reads the live gateway CR with in-cluster config, verifies a
# hot reload by dialling each gateway pod's IP, and the tests inject their faults with
# iptables inside the runner pod.
#
# Usage: ./run.sh [TestNameRegex]      (GOTEST_FLAGS="-test.timeout 90m" for anything else)
#
# Security: the destination SASL password reaches the pod ONLY via the
# retries-rest-credentials Secret -> pod-spec env (see kcp-runner.yaml); it is never put
# on a kubectl exec argv. Only non-secret KCP_RETRIES_* vars are passed here.
#
# Every run is saved to .reports/<date>-<time>/ (gitignored): run.log is the whole run,
# and each test gets a folder with the manifest it ran, kcp's raw output per run, the
# world before and after, and test.log (its slice of run.log).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

[ -f "${ENV_FILE}" ] || { echo "FATAL: ${ENV_FILE} not found — run setup.sh first" >&2; exit 1; }
# shellcheck disable=SC1090
set -a; . "${ENV_FILE}"; set +a

PROFILE="${KCP_RETRIES_KUBE_CONTEXT}"
NAMESPACE="${KCP_RETRIES_NAMESPACE}"
RUNNER="${KCP_RETRIES_KCP_POD}"

RUN_SELECTOR=""
[ -n "${1:-}" ] && RUN_SELECTOR="-test.run ${1}"
GOTEST_FLAGS="${GOTEST_FLAGS:--test.timeout 120m}"

RUN_ID="$(date +%Y-%m-%d-%H%M%S)"
RUN_DIR="${SCRIPT_DIR}/.reports/${RUN_ID}"
POD_RUN_DIR="/workspace/reports/${RUN_ID}"
mkdir -p "${RUN_DIR}"

main() {
  # main runs inside the tee pipeline below, where the caller's errexit is off.
  set -euo pipefail
  BUILD_DIR="$(mktemp -d)"
  TEST_BIN="${BUILD_DIR}/retries-e2e.test"
  KCP_BIN="${BUILD_DIR}/kcp-linux"
  trap 'rm -rf "${BUILD_DIR}"' EXIT

  NODE_ARCH="$(kubectl --context "${PROFILE}" get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
  echo "Node architecture: ${NODE_ARCH}"

  echo "Compiling the retries e2e suite for linux/${NODE_ARCH}..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go test -c -tags e2e -o "${TEST_BIN}" ./integration-tests/retries/
  )

  echo "Building the linux kcp binary (the execute subprocess)..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go build -ldflags "-X github.com/confluentinc/kcp/internal/build_info.Version=0.0.0-localdev -X github.com/confluentinc/kcp/internal/build_info.Commit=unknown -X github.com/confluentinc/kcp/internal/build_info.Date=unknown" \
      -o "${KCP_BIN}" .
  )

  echo "Waiting for the runner pod ${RUNNER} to be Ready (iptables installed)..."
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod/"${RUNNER}" --timeout=180s

  echo "Copying binaries and rendered manifests into the runner..."
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${TEST_BIN}" "${RUNNER}:/workspace/retries-e2e.test"
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- chmod +x /workspace/retries-e2e.test
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${KCP_BIN}" "${RUNNER}:/workspace/kcp"
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- chmod +x /workspace/kcp

  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- mkdir -p /workspace/rendered
  for f in "${RENDERED_DIR}"/*; do
    [ -f "${f}" ] || continue
    kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${f}" "${RUNNER}:/workspace/rendered/$(basename "${f}")"
  done

  echo ""
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- mkdir -p "${POD_RUN_DIR}"

  echo "=== Running the retries suite in-cluster ==="
  # shellcheck disable=SC2086
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- env \
    KCP_RETRIES_NAMESPACE="${KCP_RETRIES_NAMESPACE}" \
    KCP_RETRIES_RENDERED_DIR="/workspace/rendered" \
    KCP_RETRIES_REPORTS_DIR="${POD_RUN_DIR}" \
    KCP_RETRIES_GATEWAY_NAME="${KCP_RETRIES_GATEWAY_NAME}" \
    KCP_RETRIES_ROUTE_NAME="${KCP_RETRIES_ROUTE_NAME}" \
    KCP_RETRIES_DEST_DOMAIN="${KCP_RETRIES_DEST_DOMAIN}" \
    KCP_RETRIES_SOURCE_BOOTSTRAP="${KCP_RETRIES_SOURCE_BOOTSTRAP}" \
    KCP_RETRIES_DEST_BOOTSTRAP="${KCP_RETRIES_DEST_BOOTSTRAP}" \
    KCP_RETRIES_REST_ENDPOINT="${KCP_RETRIES_REST_ENDPOINT}" \
    KCP_RETRIES_DEST_CLUSTER_ID="${KCP_RETRIES_DEST_CLUSTER_ID}" \
    KCP_RETRIES_CLUSTER_LINK_NAME="${KCP_RETRIES_CLUSTER_LINK_NAME}" \
    KCP_RETRIES_TOPIC_PREFIX="${KCP_RETRIES_TOPIC_PREFIX}" \
    /workspace/retries-e2e.test -test.v ${RUN_SELECTOR} ${GOTEST_FLAGS}
}

# Output goes to the terminal and run.log; the exit code is main's.
set +e
main 2>&1 | tee "${RUN_DIR}/run.log"
status=${PIPESTATUS[0]}
set -e

# Whatever the result, copy the tests' report folders out of the runner pod, and make
# sure no fault rule is left behind in it, then give each folder its test's slice of
# run.log.
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- sh -c 'iptables -F INPUT; iptables -F OUTPUT' 2>/dev/null || true
if kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- test -d "${POD_RUN_DIR}" 2>/dev/null; then
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${RUNNER}:${POD_RUN_DIR}/." "${RUN_DIR}/" >/dev/null ||
    echo "WARN: could not copy the per-test reports out of ${RUNNER}" >&2
fi
for dir in "${RUN_DIR}"/*/; do
  [ -d "${dir}" ] || continue
  name="$(basename "${dir}")"
  awk -v start="=== RUN   ${name}" -v end="^--- (PASS|FAIL|SKIP): ${name} " \
    '$0 == start { on = 1 } on { print } on && $0 ~ end { exit }' "${RUN_DIR}/run.log" > "${dir}test.log"
done
echo "Reports saved: ${RUN_DIR}"
exit "${status}"
