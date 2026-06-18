package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findSignal returns the signal matching `id` so tests don't depend on
// slice ordering.
func findSignal(t *testing.T, section *types.EffortSignalsSection, id string) types.EffortSignal {
	t.Helper()
	require.NotNil(t, section)
	for _, s := range section.Signals {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("effort signal %q not present in section (got %d signals)", id, len(section.Signals))
	return types.EffortSignal{}
}

// Signal 1: IAM → SCRAM client count. Counts discovered_clients whose
// Auth == "IAM" across the fleet (matches the scanner-emitted token).
func TestEffortSignal_IAMClientCount(t *testing.T) {
	c := redFlagCluster("iam-cluster", "3.5.0", "", "")
	c.DiscoveredClients = []types.DiscoveredClient{
		{ClientId: "app-a", Auth: DiscoveredClientAuthIAM, Topic: "orders"},
		{ClientId: "app-b", Auth: DiscoveredClientAuthIAM, Topic: "events"},
		{ClientId: "app-c", Auth: "SASL_SCRAM", Topic: "orders"},
	}
	state := wrapClusters(c)
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	sig := findSignal(t, plan.EffortSignals, EffortSignalIDIAMClientCount)
	require.NotNil(t, sig.Count)
	assert.Equal(t, 2, *sig.Count, "two IAM-auth clients, one SCRAM client")
}

// Signal 2: MM2 checkpoint topics. Matches `*.checkpoints.internal`.
func TestEffortSignal_MM2CheckpointTopics(t *testing.T) {
	c := redFlagCluster("mm2-cluster", "3.5.0", "", "")
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{
		{Name: "us-east.checkpoints.internal", Partitions: 1},
		{Name: "us-west.checkpoints.internal", Partitions: 1},
		{Name: "regular-topic", Partitions: 1},
	}}
	state := wrapClusters(c)
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	sig := findSignal(t, plan.EffortSignals, EffortSignalIDMM2CheckpointTopics)
	require.NotNil(t, sig.Count)
	assert.Equal(t, 2, *sig.Count)
	assert.Contains(t, sig.Note, "IdentityReplicationPolicy")
}

// Signal 3: self-managed Connect fleets. Counts distinct prefixes
// where both `connect-configs` AND `connect-status` topics exist.
func TestEffortSignal_SelfManagedConnectFleets(t *testing.T) {
	c := redFlagCluster("connect-cluster", "3.5.0", "", "")
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{
		// Fleet A — has all three triad topics with prefix "team-a-"
		{Name: "team-a-connect-configs", Partitions: 1},
		{Name: "team-a-connect-offsets", Partitions: 25},
		{Name: "team-a-connect-status", Partitions: 5},
		// Fleet B — only two of three (configs + status, no offsets)
		{Name: "team-b-connect-configs", Partitions: 1},
		{Name: "team-b-connect-status", Partitions: 5},
		// Partial — only configs, NOT counted
		{Name: "team-c-connect-configs", Partitions: 1},
	}}
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	sig := findSignal(t, plan.EffortSignals, EffortSignalIDSelfManagedConnectFleets)
	require.NotNil(t, sig.Count)
	assert.Equal(t, 2, *sig.Count, "fleet A + fleet B; fleet C has only configs")
}

// Signal 4: Glue → CC SR client serializer migration count. Counts
// clients whose Topic matches a Glue schema name (direct match or
// via `-value` / `-key` suffix).
func TestEffortSignal_GlueSerializerMigration(t *testing.T) {
	c := redFlagCluster("glue-cluster", "3.5.0", "", "")
	c.DiscoveredClients = []types.DiscoveredClient{
		{ClientId: "client-a", Topic: "orders"},       // direct match
		{ClientId: "client-b", Topic: "events-value"}, // -value suffix variant
		{ClientId: "client-c", Topic: "other-topic"},  // no Glue match
	}
	state := wrapClusters(c)
	// Attach Glue registry with two schemas.
	state.SchemaRegistries = &types.SchemaRegistriesState{
		AWSGlue: []types.GlueSchemaRegistryInformation{{
			RegistryName: "my-glue",
			Schemas: []types.GlueSchema{
				{SchemaName: "orders"},
				{SchemaName: "events"},
			},
		}},
	}
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	sig := findSignal(t, plan.EffortSignals, EffortSignalIDGlueSerializerMigration)
	require.NotNil(t, sig.Count)
	assert.Equal(t, 2, *sig.Count)
}

// Glue absent → signal still surfaces but with count 0 and an
// explanatory note.
func TestEffortSignal_GlueAbsent_ZeroWithNote(t *testing.T) {
	state := wrapClusters(redFlagCluster("plain-cluster", "3.5.0", "", ""))
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	sig := findSignal(t, plan.EffortSignals, EffortSignalIDGlueSerializerMigration)
	require.NotNil(t, sig.Count)
	assert.Equal(t, 0, *sig.Count)
	assert.Contains(t, sig.Note, "no Glue Schema Registry detected")
}

// detectEffortSignals returns nil on an empty fleet so the renderer
// omits §Effort Signals.
func TestDetectEffortSignals_EmptyFleetReturnsNil(t *testing.T) {
	assert.Nil(t, detectEffortSignals(report.ProcessedState{}))
}
