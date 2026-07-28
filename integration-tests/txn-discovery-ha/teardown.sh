#!/bin/bash
# Tear down the txn-discovery HA cluster and its volumes.
#
# Invoked from the Makefile target's EXIT trap, so it runs on a failing test run
# as well as a passing one. That matters more here than in the single-node suite:
# this suite deliberately kills a broker mid-run, so a failure part-way through
# leaves a stack with one dead container that `docker compose down` still has to
# clean up.
#
# It must therefore never fail the build itself: a teardown that errored on an
# already-removed stack would turn a clean failure into a confusing one.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Tearing down txn-discovery HA cluster..."
docker compose -f "$SCRIPT_DIR/docker-compose.yml" down -v --remove-orphans || true
echo "Done."
