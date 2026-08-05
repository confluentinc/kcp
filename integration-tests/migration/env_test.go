//go:build e2e

package migration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// scenarioProvisioned decides whether an opt-in scenario runs or skips, so it is
// worth checking without a cluster: a false negative silently skips coverage,
// and a false positive fails a run for a scenario setup.sh never created.
//
// These are the only tests in this package that need no infrastructure.
func TestScenarioProvisioned(t *testing.T) {
	const defaultRun = "baseline,pause-sync-happy,pause-sync-refuses,pause-sync-restores-filters,pause-sync-rogue,pause-sync-drift,pause-sync-drain,batch,rogue-producer,rogue-producer-false-positive"

	tests := []struct {
		name     string
		env      map[string]string
		scenario string
		want     bool
	}{
		{
			name:     "hot-reload absent from a default run",
			env:      map[string]string{"KCP_E2E_SCENARIOS": defaultRun},
			scenario: scenarioHotReload,
			want:     false,
		},
		{
			name:     "hot-reload present when opted in",
			env:      map[string]string{"KCP_E2E_SCENARIOS": defaultRun + "," + scenarioHotReload},
			scenario: scenarioHotReload,
			want:     true,
		},
		{
			name:     "a default scenario is found",
			env:      map[string]string{"KCP_E2E_SCENARIOS": defaultRun},
			scenario: scenarioBaseline,
			want:     true,
		},
		{
			name:     "missing var provisions nothing",
			env:      map[string]string{},
			scenario: scenarioBaseline,
			want:     false,
		},
		{
			name:     "empty var provisions nothing",
			env:      map[string]string{"KCP_E2E_SCENARIOS": ""},
			scenario: scenarioBaseline,
			want:     false,
		},
		{
			// "rogue-producer" must not match "rogue-producer-false-positive",
			// and the reverse must hold too — a substring match would report a
			// scenario as provisioned when only its longer sibling exists.
			name:     "a scenario that is a prefix of another is matched exactly",
			env:      map[string]string{"KCP_E2E_SCENARIOS": "baseline,rogue-producer-false-positive"},
			scenario: scenarioRogueProducer,
			want:     false,
		},
		{
			name:     "the longer sibling is still matched",
			env:      map[string]string{"KCP_E2E_SCENARIOS": "baseline,rogue-producer-false-positive"},
			scenario: scenarioRogueProducerFalsePositive,
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scenarioProvisioned(tc.env, tc.scenario))
		})
	}
}

// The scenario constant and setup.sh's HOT_RELOAD_SCENARIO must agree, or setup
// provisions resources under a name the test never looks for.
func TestHotReloadScenarioName(t *testing.T) {
	assert.Equal(t, "hot-reload", scenarioHotReload,
		"must match HOT_RELOAD_SCENARIO in testdata/setup.sh")
}
