package migration_test

import (
	"strings"
	"testing"
)

// producerClientID is the scenario-scoped Kafka client.id passed to the source
// producer (`--client-id`). Built solely from the controlled scenario constant.
func producerClientID(scenario string) string { return "kcp-e2e-producer-" + scenario }

// producerKillPattern is the `pkill -f` pattern that terminates ONLY this
// scenario's producer. It is exactly the scenario-scoped client.id, which
// appears in the producer's argv, so it cannot match a sibling scenario.
func producerKillPattern(scenario string) string { return producerClientID(scenario) }

// consumerGroupID is the scenario-scoped consumer group.id. Distinct groups keep
// concurrent scenarios' consumers off a shared coordinator, avoiding rebalance
// churn under t.Parallel().
func consumerGroupID(scenario string) string { return "kcp-e2e-consumer-group-" + scenario }

// scenarioWorkdir is the per-scenario in-pod working directory kcp runs from, so
// its cwd-relative kcp.log becomes /workspace/<scenario>/kcp.log — isolated from
// other scenarios' kcp runs under t.Parallel().
func scenarioWorkdir(scenario string) string { return "/workspace/" + scenario }

// allScenarios mirrors setup.sh's SCENARIOS array and the scenario constants in
// migration_e2e_test.go. Kept in sync by hand; drift would make the abuse test
// below cover the wrong set.
var allScenarios = []string{
	"baseline", "pause-sync-happy", "pause-sync-refuses",
	"pause-sync-restores-filters", "pause-sync-rogue", "pause-sync-drift",
	"pause-sync-drain", "batch", "rogue-producer",
}

// producerArgv reproduces the argv that startProducerOnSource launches, so the
// kill pattern is matched against a realistic command line.
func producerArgv(scenario string) string {
	return "/workspace/producer --bootstrap src:9071 --topic e2e-test-topic-" + scenario +
		" --duration 5m0s --rate 10 --client-id " + producerClientID(scenario)
}

// pkillMatches models `pkill -f <pattern>`: the pattern is matched against the
// full argv. Our patterns contain no ERE metacharacters, so substring is exact.
func pkillMatches(pattern, argv string) bool { return strings.Contains(argv, pattern) }

func TestProducerClientIDIsScenarioScoped(t *testing.T) {
	if got := producerClientID("baseline"); got != "kcp-e2e-producer-baseline" {
		t.Fatalf("producerClientID(baseline) = %q, want kcp-e2e-producer-baseline", got)
	}
}

func TestConsumerGroupIDIsScenarioScoped(t *testing.T) {
	if got := consumerGroupID("batch"); got != "kcp-e2e-consumer-group-batch" {
		t.Fatalf("consumerGroupID(batch) = %q, want kcp-e2e-consumer-group-batch", got)
	}
}

// Abuse case: a scenario's producer kill pattern must match ONLY its own
// producer and never a sibling's. A sibling match means one parallel scenario's
// stopProducer would kill another scenario's producer.
func TestProducerKillPatternMatchesOnlyOwnScenario(t *testing.T) {
	for _, a := range allScenarios {
		pattern := producerKillPattern(a)
		if !pkillMatches(pattern, producerArgv(a)) {
			t.Errorf("kill pattern %q does not match its own producer (%s)", pattern, a)
		}
		for _, b := range allScenarios {
			if a == b {
				continue
			}
			if pkillMatches(pattern, producerArgv(b)) {
				t.Errorf("kill pattern for %q ALSO matches sibling %q — would kill the wrong producer", a, b)
			}
		}
	}
}

// Abuse case: the kill pattern is derived solely from the controlled scenario
// constant, never interpolated external input.
func TestProducerKillPatternDerivedFromScenarioConstant(t *testing.T) {
	for _, s := range allScenarios {
		if producerKillPattern(s) != producerClientID(s) {
			t.Errorf("kill pattern for %q must equal its scenario-derived client-id", s)
		}
	}
}

// scenarioWorkdir gives each scenario its own kcp working directory so kcp's
// cwd-relative kcp.log lands per-scenario — otherwise the batch test's exact
// log-line counts are contaminated by concurrent scenarios' kcp runs.
func TestScenarioWorkdirIsScenarioScoped(t *testing.T) {
	if got := scenarioWorkdir("batch"); got != "/workspace/batch" {
		t.Fatalf("scenarioWorkdir(batch) = %q, want /workspace/batch", got)
	}
	seen := map[string]bool{}
	for _, s := range allScenarios {
		d := scenarioWorkdir(s)
		if seen[d] {
			t.Errorf("scenarioWorkdir collision at %q", d)
		}
		seen[d] = true
	}
}
