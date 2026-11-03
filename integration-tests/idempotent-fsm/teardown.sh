#!/usr/bin/env bash
# Deletes one mode's idempotent-fsm E2E cluster (GATEWAY_MODE=dynamic, the default,
# or static; see state.sh) and that mode's generated files. The other mode's
# environment is left alone.
#
# Deletes the whole minikube profile rather than just the Confluent resources: the
# suite's premise is a clean dynamic gateway, and a half-cleaned cluster is how a
# stale CRD or a stale licence secret silently changes what the next run tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

echo "Tearing down idempotent-fsm E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${STATE_DIR}"
echo "Done."
