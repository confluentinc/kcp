#!/bin/bash
# Tear down the txn-discovery broker and its volumes.
#
# Invoked from the Makefile target's EXIT trap, so it runs on a failing test run
# as well as a passing one. It must therefore never fail the build itself: a
# teardown that errors on an already-removed stack would turn a clean failure
# into a confusing one.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Tearing down txn-discovery broker..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" down -v --remove-orphans || true
echo "Done."
