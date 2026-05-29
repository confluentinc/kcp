package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// findRow returns the row matching `id` so tests don't depend on slice
// ordering. Fails the test when the row is missing.
func findRow(t *testing.T, section *types.RedFlagsSection, id string) types.RedFlag {
	t.Helper()
	require.NotNil(t, section)
	for _, r := range section.Rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("red flag row %q not present in section (got %d rows)", id, len(section.Rows))
	return types.RedFlag{}
}

// Row 1 — schemaless source. Suppressed when `schema_strategy` is
// unknown; fires when a strategy is declared AND no SR was scanned.
func TestRedFlags_SchemalessSource(t *testing.T) {
	// Need at least one MSK cluster — DetectRedFlags returns nil on
	// empty fleets so §Red Flags can be omitted cleanly. The cluster
	// itself doesn't carry an SR; the schemaless verdict comes from
	// the absence of a SchemaRegistriesState on the state file.
	state := wrapClusters(redFlagCluster("plain-cluster", "3.5.0", "", ""))
	cfg := defaultCfg(t)

	// strategy = unknown → row is Unknown, not Triggered.
	inputs := schemaInputs(SchemaStrategyUnknown)
	plan := buildPlanForRedFlags(t, state, cfg, inputs)
	row := findRow(t, plan.RedFlags, RedFlagIDSchemalessSource)
	assert.Equal(t, types.RedFlagUnknown, row.Status, "strategy=unknown must not fire row 1")

	// strategy = adopt_schemas_during_migration → row fires.
	inputs = schemaInputs(SchemaStrategyAdoptSchemasDuringMigration)
	plan = buildPlanForRedFlags(t, state, cfg, inputs)
	row = findRow(t, plan.RedFlags, RedFlagIDSchemalessSource)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "adopt_schemas_during_migration")
}

// Row 2 — Kafka version below Cluster Linking floor (2.4.0).
func TestRedFlags_KafkaVersionBelowFloor(t *testing.T) {
	below := redFlagCluster("old-cluster", "2.2.1", "", "")
	above := redFlagCluster("new-cluster", "3.5.0", "", "")
	state := wrapClusters(below, above)
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDKafkaVersionBelowCLFloor)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "old-cluster=2.2.1")
	assert.NotContains(t, row.Evidence, "new-cluster")
}

// Row 11 — MSK Express broker tier.
func TestRedFlags_ExpressBrokerTier(t *testing.T) {
	expressCluster := redFlagCluster("express-cluster", "3.5.0", "express.m7g.large", "")
	standardCluster := redFlagCluster("standard-cluster", "3.5.0", "kafka.m5.large", "")
	plan := buildPlanForRedFlags(t, wrapClusters(expressCluster, standardCluster), defaultCfg(t), defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDMSKExpressBrokerTier)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "express-cluster=express.m7g.large")
}

// Row 12 — tiered storage in use.
func TestRedFlags_TieredStorageInUse(t *testing.T) {
	tiered := redFlagCluster("tiered-cluster", "3.5.0", "", string(kafkatypes.StorageModeTiered))
	local := redFlagCluster("local-cluster", "3.5.0", "", string(kafkatypes.StorageModeLocal))
	plan := buildPlanForRedFlags(t, wrapClusters(tiered, local), defaultCfg(t), defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDTieredStorageInUse)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "tiered-cluster")
}

// Row 13 — EOS / Kafka transactions. Customer-declared only.
func TestRedFlags_EOSInUse(t *testing.T) {
	state := wrapClusters(redFlagCluster("eos-cluster", "3.5.0", "", ""))
	cfg := defaultCfg(t)

	// nil → Unknown
	plan := buildPlanForRedFlags(t, state, cfg, defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDEOSInUse)
	assert.Equal(t, types.RedFlagUnknown, row.Status)

	// true → Triggered
	inputs := defaultInputs()
	inputs.ExactlyOnceTransactionsInUse = ptrBool(true)
	plan = buildPlanForRedFlags(t, state, cfg, inputs)
	row = findRow(t, plan.RedFlags, RedFlagIDEOSInUse)
	assert.Equal(t, types.RedFlagTriggered, row.Status)

	// false → NotTriggered
	inputs.ExactlyOnceTransactionsInUse = ptrBool(false)
	plan = buildPlanForRedFlags(t, state, cfg, inputs)
	row = findRow(t, plan.RedFlags, RedFlagIDEOSInUse)
	assert.Equal(t, types.RedFlagNotTriggered, row.Status)
}

// Row 15 — broad topic-name pattern scan: catches MM2 / Connect /
// Streams / heartbeats artifacts via topic-name regex.
func TestRedFlags_BroadTopicPatternMatch(t *testing.T) {
	c := redFlagCluster("scan-target", "3.5.0", "", "")
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{
		{Name: "regular-topic"},
		{Name: "mm2-source-data"},
		{Name: "events-changelog"},
	}}
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDBroadTopicPatternMatch)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "mm2-source-data")
	assert.Contains(t, row.Evidence, "events-changelog")
}

// Empty fleet (no MSK clusters) → DetectRedFlags returns nil so the
// renderer omits the §Red Flags section entirely.
func TestDetectRedFlags_EmptyFleetReturnsNil(t *testing.T) {
	assert.Nil(t, DetectRedFlags(types.ProcessedState{}, &types.Plan{}, defaultCfg(t), defaultInputs()))
}

// ----- helpers -----

// redFlagCluster constructs a ProcessedCluster with the AWS SDK
// MskClusterConfig fields the Red Flag detectors read. Pass empty
// strings to leave a field unset.
func redFlagCluster(name, kafkaVersion, instanceType, storageMode string) types.ProcessedCluster {
	c := types.ProcessedCluster{Name: name, Region: "us-east-1"}
	prov := &kafkatypes.Provisioned{}
	if kafkaVersion != "" {
		v := kafkaVersion
		prov.CurrentBrokerSoftwareInfo = &kafkatypes.BrokerSoftwareInfo{KafkaVersion: &v}
	}
	if instanceType != "" {
		it := instanceType
		prov.BrokerNodeGroupInfo = &kafkatypes.BrokerNodeGroupInfo{InstanceType: &it}
	}
	if storageMode != "" {
		prov.StorageMode = kafkatypes.StorageMode(storageMode)
	}
	c.AWSClientInformation.MskClusterConfig.Provisioned = prov
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
	// Populate topics minimally so the MSK Connect / Self-managed
	// Connect "topics populated" disambiguation can fire on its own.
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{{Name: name + "-topic"}}}
	return c
}

func wrapClusters(clusters ...types.ProcessedCluster) types.ProcessedState {
	return types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{{Name: "us-east-1", Clusters: clusters}},
			},
		}},
	}
}

// buildPlanForRedFlags runs PlanService.Build so the test exercises
// the full integration (V2 detector + V1 plumbing) rather than
// calling DetectRedFlags in isolation. Same default time / cfg used
// by the rest of the plan tests.
func buildPlanForRedFlags(t *testing.T, state types.ProcessedState, cfg *PlanConfig, inputs types.PlanInputsResolved) *types.Plan {
	t.Helper()
	svc := NewPlanService(cfg, fixedNow)
	p, err := svc.Build(state, inputs, "redflags-test.json")
	require.NoError(t, err)
	return p
}
