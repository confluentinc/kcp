#!/usr/bin/env bash
# Compiles the route-conversion suite (and the linux kcp binary the tests run as
# a subprocess) for the cluster node's architecture, ships them plus the rendered
# credential files into the rc-runner pod, and executes the test binary there.
#
# The runner pod is deployed by setup.sh, not here: this script only builds,
# copies, and execs. The suite cannot run on the developer's machine — reconcile
# reads the live gateway CR with in-cluster config, kcp verifies a hot reload on
# each gateway pod, and the clients reach the gateway by its in-cluster SNI names.
#
# Security: the destination SASL password is delivered to the pod ONLY via the
# tbm-rest-credentials Secret -> pod-spec env (see rc-runner.yaml). It is NEVER
# put on this kubectl exec argv. Only non-secret KCP_RC_* vars are passed here.
#
# Every run is saved to .reports/<date>-<time>/ (gitignored): run.log is the
# whole run, and each test gets its own folder holding the manifest it ran, the
# before/after reports, kcp's raw output per run, the client logs and the
# checker's verdict for the client tests, and test.log (its slice of run.log).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

[ -f "${ENV_FILE}" ] || { echo "FATAL: ${ENV_FILE} not found — run setup.sh first" >&2; exit 1; }
# shellcheck disable=SC1090
set -a; . "${ENV_FILE}"; set +a

PROFILE="${KCP_RC_KUBE_CONTEXT}"
NAMESPACE="${KCP_RC_NAMESPACE}"
RUNNER="${KCP_RC_RUNNER_POD}"

# Optional test selector: `run.sh 'TestFoo'` -> -test.run TestFoo, or export
# GOTEST_FLAGS for anything else. -test.v is always on; -test.timeout is raised
# because the whole suite takes about 20-25 minutes.
RUN_SELECTOR=""
[ -n "${1:-}" ] && RUN_SELECTOR="-test.run ${1}"
GOTEST_FLAGS="${GOTEST_FLAGS:-}"

RUN_ID="$(date +%Y-%m-%d-%H%M%S)"
RUN_DIR="${SCRIPT_DIR}/.reports/${RUN_ID}"
POD_RUN_DIR="/workspace/reports/${RUN_ID}"
mkdir -p "${RUN_DIR}"

k() { kubectl --context "${PROFILE}" -n "${NAMESPACE}" "$@"; }

# put_file SRC DEST — copy a local file into the runner pod without tar (the
# cp-server image has none, so `kubectl cp` cannot be used).
put_file() {
  k exec -i "${RUNNER}" -- sh -c "cat > '$2'" < "$1"
}

main() {
  # main runs inside the tee pipeline below, where the caller's errexit is off;
  # turn it back on so a failed step still stops the run.
  set -euo pipefail
  BUILD_DIR="$(mktemp -d)"
  TEST_BIN="${BUILD_DIR}/route-conversion-e2e.test"
  KCP_BIN="${BUILD_DIR}/kcp-linux"
  trap 'rm -rf "${BUILD_DIR}"' EXIT

  # Match the node, not the host: the binaries run in a pod. GOTOOLCHAIN=auto lets
  # go fetch the toolchain pinned in .go-version when the local goenv lacks it.
  NODE_ARCH="$(kubectl --context "${PROFILE}" get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
  echo "Node architecture: ${NODE_ARCH}"

  echo "Compiling the route-conversion e2e suite for linux/${NODE_ARCH}..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go test -c -tags e2e -o "${TEST_BIN}" ./integration-tests/route-conversion/
  )

  echo "Building the linux kcp binary (execute subprocess) ..."
  (
    cd "${REPO_ROOT}"
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
      go build -ldflags "-X github.com/confluentinc/kcp/internal/build_info.Version=0.0.0-localdev -X github.com/confluentinc/kcp/internal/build_info.Commit=unknown -X github.com/confluentinc/kcp/internal/build_info.Date=unknown" \
      -o "${KCP_BIN}" .
  )

  echo "Waiting for the runner pod ${RUNNER} to be Ready..."
  k wait --for=condition=Ready pod/"${RUNNER}" --timeout=120s

  echo "Copying binaries and rendered files into the runner..."
  put_file "${TEST_BIN}" /workspace/route-conversion-e2e.test
  k exec "${RUNNER}" -- chmod +x /workspace/route-conversion-e2e.test
  put_file "${KCP_BIN}" /workspace/kcp
  k exec "${RUNNER}" -- chmod +x /workspace/kcp

  k exec "${RUNNER}" -- mkdir -p /workspace/rendered
  for f in "${RENDERED_DIR}"/*; do
    [ -f "${f}" ] || continue
    put_file "${f}" "/workspace/rendered/$(basename "${f}")"
  done
  k exec "${RUNNER}" -- sh -c 'chmod 600 /workspace/rendered/*'

  echo ""
  k exec "${RUNNER}" -- mkdir -p "${POD_RUN_DIR}"

  echo "=== Running the route-conversion suite in-cluster ==="
  # Only non-secret KCP_RC_* are passed on the exec line; KCP_RC_RENDERED_DIR is
  # rewritten to the in-pod path. The destination SASL user/password are already
  # in the pod env from the tbm-rest-credentials Secret.
  # shellcheck disable=SC2086
  k exec "${RUNNER}" -- env \
    KCP_RC_NAMESPACE="${KCP_RC_NAMESPACE}" \
    KCP_RC_RENDERED_DIR="/workspace/rendered" \
    KCP_RC_REPORTS_DIR="${POD_RUN_DIR}" \
    KCP_RC_GATEWAY_NAME="${KCP_RC_GATEWAY_NAME}" \
    KCP_RC_ROUTE_NAME="${KCP_RC_ROUTE_NAME}" \
    KCP_RC_SOURCE_DOMAIN="${KCP_RC_SOURCE_DOMAIN}" \
    KCP_RC_SOURCE_BOOTSTRAP_ID="${KCP_RC_SOURCE_BOOTSTRAP_ID}" \
    KCP_RC_DEST_DOMAIN="${KCP_RC_DEST_DOMAIN}" \
    KCP_RC_DEST_BOOTSTRAP_ID="${KCP_RC_DEST_BOOTSTRAP_ID}" \
    KCP_RC_SOURCE_BOOTSTRAP="${KCP_RC_SOURCE_BOOTSTRAP}" \
    KCP_RC_DEST_BOOTSTRAP="${KCP_RC_DEST_BOOTSTRAP}" \
    KCP_RC_GATEWAY_BOOTSTRAP="${KCP_RC_GATEWAY_BOOTSTRAP}" \
    KCP_RC_REST_ENDPOINT="${KCP_RC_REST_ENDPOINT}" \
    KCP_RC_DEST_CLUSTER_ID="${KCP_RC_DEST_CLUSTER_ID}" \
    KCP_RC_CLUSTER_LINK_NAME="${KCP_RC_CLUSTER_LINK_NAME}" \
    KCP_RC_TOPIC_PREFIX="${KCP_RC_TOPIC_PREFIX}" \
    KCP_RC_ORPHAN_TOPIC="${KCP_RC_ORPHAN_TOPIC}" \
    /workspace/route-conversion-e2e.test -test.v -test.timeout 90m ${RUN_SELECTOR} ${GOTEST_FLAGS}
}

# Output goes to the terminal and run.log; the exit code is main's.
set +e
main 2>&1 | tee "${RUN_DIR}/run.log"
status=${PIPESTATUS[0]}
set -e

# Whatever the result, copy the tests' report folders out of the runner pod,
# then give each folder its test's slice of run.log (from its `=== RUN` line to
# its `--- PASS/FAIL/SKIP` line).
if k exec "${RUNNER}" -- test -d "${POD_RUN_DIR}" 2>/dev/null; then
  # No tar in the image: stream a tar built by the pod's python3 and unpack it here.
  k exec "${RUNNER}" -- python3 -c "import sys,tarfile; t=tarfile.open(fileobj=sys.stdout.buffer, mode='w|'); t.add('${POD_RUN_DIR}', arcname='.'); t.close()" |
    tar -x -C "${RUN_DIR}" ||
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
