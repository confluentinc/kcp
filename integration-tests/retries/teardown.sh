#!/usr/bin/env bash
# Deletes this suite's Minikube profile (kcp-e2e-retries, see state.sh) and its
# generated files. Deletes the whole profile rather than just the Confluent resources:
# a half-cleaned cluster is how a stale CRD, licence secret or leftover fault rule
# silently changes what the next run tests. No other suite's environment is touched.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=state.sh
. "${SCRIPT_DIR}/state.sh"

echo "Tearing down retries E2E profile '${PROFILE}'..."
minikube delete --profile "${PROFILE}" || true

rm -rf "${STATE_DIR}"
echo "Done."
