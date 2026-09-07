#!/usr/bin/env bash
# Run every integration suite in sequence and print ONE grand total across all of
# them (like `go test` does per-package, but aggregated across suites that each
# need their own docker/Minikube environment).
#
#   bash integration-tests/run-all.sh              # all suites incl. migration (Minikube)
#   bash integration-tests/run-all.sh --no-migration # skip the heavy Minikube suite
#
# Each suite: bring its env up, run `go test -json` (tee to a per-suite log), tear
# the env down, record the suite's exit code. At the end, aggregate every per-test
# result (incl. subtests — matches the `--- PASS/FAIL:` lines `go test -v` prints)
# and print per-suite + grand totals. Exits non-zero if any test failed or any
# suite errored (e.g. a build failure that emits no test event).
#
# Requires: go, jq, docker; minikube+kubectl for the migration suite.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
LOG="$ROOT/integration-tests/.run-all-logs"

# Exclusive lock: these suites use FIXED docker container names, host ports, and a
# single shared Minikube profile (kcp-e2e), so two runs at once silently corrupt
# each other (port clashes, "resource already exists", one run tearing down the
# other's env mid-flight). mkdir is atomic, so this refuses a concurrent run
# instead of producing bogus failures. Also warn on leftover fixtures from a
# crashed run.
LOCK="$ROOT/integration-tests/.run-all.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
  echo "✗ Another integration run appears to be in progress (lock: $LOCK)."
  echo "  Integration suites cannot run concurrently — they share fixed ports,"
  echo "  container names, and the Minikube 'kcp-e2e' profile."
  echo "  If you are certain no other run is active, remove the lock: rm -rf \"$LOCK\""
  exit 1
fi
echo "$$" > "$LOCK/pid"
trap 'rm -rf "$LOCK"' EXIT
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^kcp-test'; then
  echo "⚠ leftover kcp-test containers are already running — a previous run may not have torn down."
  echo "  They may cause port/name clashes. Consider: docker ps | grep kcp-test"
fi

rm -rf "$LOG"; mkdir -p "$LOG"

WITH_MIGRATION=1
[[ "${1:-}" == "--no-migration" ]] && WITH_MIGRATION=0

# Build once up front (each Make target also builds, but the binary must exist for
# the exec'd `./kcp` in the scan suites).
echo "▶ building kcp + frontend..."
make build > "$LOG/build.log" 2>&1 || { echo "✗ build failed — see $LOG/build.log"; exit 1; }

SUITE_NAMES=()
SUITE_CODES=()

# run_suite <name> <dir> <tag> <up-cmd> <down-cmd>
run_suite() {
  local name="$1" dir="$2" tag="$3" up="$4" down="$5"
  echo ""
  echo "══════════════════════════════════════════"
  echo "  SUITE: $name"
  echo "══════════════════════════════════════════"

  if ! eval "$up" > "$LOG/$name.env.log" 2>&1; then
    echo "✗ $name: environment setup failed — see $LOG/$name.env.log"
    SUITE_NAMES+=("$name"); SUITE_CODES+=(1)
    eval "$down" >> "$LOG/$name.env.log" 2>&1 || true
    return
  fi

  ( cd "$dir" && go test -tags "$tag" -json ./... ) > "$LOG/$name.json" 2>"$LOG/$name.stderr"
  local code=$?
  # Human-readable per-test output for the console, derived from the JSON.
  jq -r 'select(.Action=="pass" or .Action=="fail" or .Action=="skip") | select(.Test != null)
         | "  \(.Action | ascii_upcase)  \(.Test)"' "$LOG/$name.json" 2>/dev/null || true

  eval "$down" >> "$LOG/$name.env.log" 2>&1 || true
  SUITE_NAMES+=("$name"); SUITE_CODES+=("$code")
}

run_suite migrate         integration-tests/migrate         integration \
  "make test-env-up-migrate" "make test-env-down-migrate"
run_suite osk-scan        integration-tests/osk-scan        integration \
  "bash integration-tests/osk-scan/setup.sh" "bash integration-tests/osk-scan/teardown.sh"
run_suite schema-registry integration-tests/schema-registry integration \
  "bash integration-tests/schema-registry/setup.sh" "bash integration-tests/schema-registry/teardown.sh"
run_suite connect-scan    integration-tests/connect-scan    integration \
  "bash integration-tests/connect-scan/setup.sh" "bash integration-tests/connect-scan/teardown.sh"
if [[ "$WITH_MIGRATION" == "1" ]]; then
  run_suite migration integration-tests/migration e2e \
    "bash integration-tests/migration/testdata/setup.sh" "bash integration-tests/migration/testdata/teardown.sh"
fi

# ── Aggregate ────────────────────────────────────────────────────────────────
echo ""
echo "══════════════════════════════════════════"
echo "  INTEGRATION TEST SUMMARY"
echo "══════════════════════════════════════════"

total_pass=0 total_fail=0 total_skip=0 suites_errored=0
count() { # <suite> <action>
  jq -r --arg a "$2" 'select(.Action==$a) | select(.Test != null) | .Test' "$LOG/$1.json" 2>/dev/null | wc -l | tr -d ' '
}

printf "%-18s %6s %6s %6s %8s\n" "SUITE" "PASS" "FAIL" "SKIP" "EXIT"
for i in "${!SUITE_NAMES[@]}"; do
  n="${SUITE_NAMES[$i]}"; code="${SUITE_CODES[$i]}"
  p=$(count "$n" pass); f=$(count "$n" fail); s=$(count "$n" skip)
  [[ -z "$p" ]] && p=0; [[ -z "$f" ]] && f=0; [[ -z "$s" ]] && s=0
  total_pass=$((total_pass + p)); total_fail=$((total_fail + f)); total_skip=$((total_skip + s))
  [[ "$code" != "0" ]] && suites_errored=$((suites_errored + 1))
  printf "%-18s %6s %6s %6s %8s\n" "$n" "$p" "$f" "$s" "$code"
done
echo "------------------------------------------------------"
printf "%-18s %6s %6s %6s\n" "TOTAL" "$total_pass" "$total_fail" "$total_skip"
echo ""
echo "$((total_pass + total_fail + total_skip)) tests across ${#SUITE_NAMES[@]} suites: $total_pass passed, $total_fail failed, $total_skip skipped"

if [[ "$total_fail" -gt 0 || "$suites_errored" -gt 0 ]]; then
  echo "✗ integration tests FAILED ($suites_errored suite(s) errored; see $LOG/)"
  exit 1
fi
echo "✅ all integration tests passed"
