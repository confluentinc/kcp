# Sourced by setup.sh, run.sh and teardown.sh: where the route-conversion
# environment lives. It has one Minikube profile (also its kubectl context) and
# one folder of generated files, so it can be up at the same time as either
# idempotent-fsm environment.
#
# The caller sets SCRIPT_DIR.

PROFILE="kcp-e2e-route-conversion"

# setup.sh's generated files: .env (the settings run.sh reads) and rendered/
# (the credential files run.sh copies into the runner pod).
STATE_DIR="${SCRIPT_DIR}/.state"
ENV_FILE="${STATE_DIR}/.env"
RENDERED_DIR="${STATE_DIR}/rendered"
