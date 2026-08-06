#!/usr/bin/env bash
# Compiles the hot-reload suite for the cluster node's architecture, ships it to
# the runner pod and executes it there.
#
# The suite cannot run on the developer's machine: kcp verifies a transition by
# dialling each gateway pod's IP on the /config port, and pod IPs are not
# routable from the host under the minikube docker driver. Running it in-cluster
# is not a workaround for the harness, it is the only place the mechanism under
# test is observable.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/manifests"
RENDERED_DIR="${SCRIPT_DIR}/.rendered"
ENV_FILE="${SCRIPT_DIR}/.env"

[ -f "${ENV_FILE}" ] || { echo "FATAL: ${ENV_FILE} not found — run setup.sh first" >&2; exit 1; }
# shellcheck disable=SC1090
set -a; . "${ENV_FILE}"; set +a

PROFILE="${KCP_HR_KUBE_CONTEXT}"
NAMESPACE="${KCP_HR_NAMESPACE}"
RUNNER="kcp-hotreload-runner"
GOTEST_FLAGS="${GOTEST_FLAGS:--test.v}"

# Match the node, not the host: the test binary runs in a pod. On Apple Silicon
# the node is arm64 while the CFK operator image is amd64 under emulation, so the
# two architectures genuinely differ and assuming either one is wrong.
NODE_ARCH="$(kubectl --context "${PROFILE}" get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
echo "Node architecture: ${NODE_ARCH}"

echo "Compiling the suite for linux/${NODE_ARCH}..."
TEST_BIN="${RENDERED_DIR}/hotreload.test"
mkdir -p "${RENDERED_DIR}"
(
  cd "${REPO_ROOT}"
  CGO_ENABLED=0 GOOS=linux GOARCH="${NODE_ARCH}" \
    go test -c -tags e2e -o "${TEST_BIN}" ./integration-tests/migration-hot-reload/
)

# The runner reuses a small image already present in the node so nothing has to
# be pulled; any image with a shell will do.
RUNNER_IMAGE="${RUNNER_IMAGE:-confluentinc/confluent-init-container:${INIT_TAG:-3.3.0}}"
echo "Starting the runner pod (${RUNNER_IMAGE})..."
sed -e "s|__RUNNER_IMAGE__|${RUNNER_IMAGE}|g" "${MANIFESTS_DIR}/kcp-runner.yaml" \
  | kubectl --context "${PROFILE}" apply -f - >/dev/null

kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=Ready pod/"${RUNNER}" --timeout=180s

echo "Copying the suite and its manifests into the runner..."
kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${TEST_BIN}" "${RUNNER}:/workspace/hotreload.test"
for cr in "${KCP_HR_INITIAL_CR}" "${KCP_HR_FENCED_CR}" "${KCP_HR_SWITCHOVER_CR}"; do
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" cp "${cr}" "${RUNNER}:/workspace/$(basename "${cr}")"
done
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- chmod +x /workspace/hotreload.test

echo ""
echo "=== Running the hot-reload suite in-cluster ==="
# The CR paths are rewritten to their in-pod locations. Nothing secret is passed:
# the licence reaches the gateway through a Secret the CRs only reference by name.
kubectl --context "${PROFILE}" -n "${NAMESPACE}" exec "${RUNNER}" -- env \
  KCP_HR_NAMESPACE="${NAMESPACE}" \
  KCP_HR_GATEWAY_NAME="${KCP_HR_GATEWAY_NAME}" \
  KCP_HR_GATEWAY_REPLICAS="${KCP_HR_GATEWAY_REPLICAS}" \
  KCP_HR_INITIAL_CR="/workspace/$(basename "${KCP_HR_INITIAL_CR}")" \
  KCP_HR_FENCED_CR="/workspace/$(basename "${KCP_HR_FENCED_CR}")" \
  KCP_HR_SWITCHOVER_CR="/workspace/$(basename "${KCP_HR_SWITCHOVER_CR}")" \
  /workspace/hotreload.test ${GOTEST_FLAGS}
