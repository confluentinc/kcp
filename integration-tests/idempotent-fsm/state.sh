# Sourced by setup.sh, run.sh and teardown.sh: where the environment for one
# route mode lives. Each mode (GATEWAY_MODE=dynamic|static|dynamic-rollout,
# default dynamic) has its own Minikube profile and its own generated files, so
# the environments can be up, and run tests, at the same time. dynamic-rollout
# is a dynamic route on a gateway with hot reload OFF, so every route change
# rolls the gateway pods (TBM's data-plane exposure to pod turnover).
#
# The caller sets SCRIPT_DIR.

GATEWAY_MODE="${GATEWAY_MODE:-dynamic}"
case "${GATEWAY_MODE}" in
  dynamic | static | dynamic-rollout) ;;
  *)
    echo "FATAL: GATEWAY_MODE must be 'dynamic', 'static' or 'dynamic-rollout', got '${GATEWAY_MODE}'" >&2
    exit 1
    ;;
esac

# The Minikube profile for this mode, which is also its kubectl context.
PROFILE="kcp-e2e-idempotent-${GATEWAY_MODE}"

# setup.sh's generated files for this mode: .env (the settings run.sh reads) and
# rendered/ (the manifests and credential files run.sh copies into the runner pod).
STATE_DIR="${SCRIPT_DIR}/.state/${GATEWAY_MODE}"
ENV_FILE="${STATE_DIR}/.env"
RENDERED_DIR="${STATE_DIR}/rendered"
