# Sourced by setup.sh, run.sh and teardown.sh: where this suite's environment lives.
# The caller sets SCRIPT_DIR.

# The Minikube profile, which is also its kubectl context. Its own profile, so the
# suite never touches another suite's environment.
PROFILE="kcp-e2e-retries"

# setup.sh's generated files: .env (the settings run.sh reads) and rendered/ (the
# manifests and credential files run.sh copies into the runner pod).
STATE_DIR="${SCRIPT_DIR}/.state"
ENV_FILE="${STATE_DIR}/.env"
RENDERED_DIR="${STATE_DIR}/rendered"
