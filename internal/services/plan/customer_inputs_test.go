package plan

import (
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(v string) *string   { return &v }
func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }

// applyClusterDeclarations synthesises a new ProcessedCluster when the
// state file has no MSK clusters but plan-inputs declares one. The
// synthesised cluster lands in the named region bucket and carries the
// declared facts (cluster type, broker count, auth, partition count,
// throughput aggregates). Downstream decisions consume the merged state
// as if it had come from a scan.
func TestApplyClusterDeclarations_SynthesisFromEmptyState(t *testing.T) {
	state := types.ProcessedState{}
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"events-platform": {
				Region:              sp("us-west-2"),
				ClusterTypeFromScan: sp("PROVISIONED"),
				KafkaVersion:        sp("3.6.0"),
				BrokerCount:         ip(80),
				BrokerInstanceType:  sp("kafka.m5.24xlarge"),
				StorageMode:         sp("LOCAL"),
				AuthMethods:         []string{SourceAuthSCRAM},
				PeakIngressMBps:     fp(3500),
				PeakEgressMBps:      fp(8400),
				PartitionCount:      ip(98000),
				TopicCount:          ip(1200),
				ACLCount:            ip(3100),
			},
		},
	}
	applyClusterDeclarations(&state, raw)

	require.Len(t, state.Sources, 1, "MSK source bucket created")
	require.NotNil(t, state.Sources[0].MSKData)
	require.Len(t, state.Sources[0].MSKData.Regions, 1)
	require.Equal(t, "us-west-2", state.Sources[0].MSKData.Regions[0].Name)
	require.Len(t, state.Sources[0].MSKData.Regions[0].Clusters, 1)

	c := state.Sources[0].MSKData.Regions[0].Clusters[0]
	assert.Equal(t, "events-platform", c.Name)
	assert.Equal(t, kafkatypes.ClusterTypeProvisioned, c.AWSClientInformation.MskClusterConfig.ClusterType)
	require.NotNil(t, c.AWSClientInformation.MskClusterConfig.Provisioned)

	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	require.NotNil(t, prov.CurrentBrokerSoftwareInfo)
	assert.Equal(t, "3.6.0", *prov.CurrentBrokerSoftwareInfo.KafkaVersion)
	require.NotNil(t, prov.BrokerNodeGroupInfo)
	assert.Equal(t, "kafka.m5.24xlarge", *prov.BrokerNodeGroupInfo.InstanceType)
	assert.Equal(t, kafkatypes.StorageModeLocal, prov.StorageMode)
	require.NotNil(t, prov.ClientAuthentication.Sasl)
	require.NotNil(t, prov.ClientAuthentication.Sasl.Scram)
	assert.True(t, *prov.ClientAuthentication.Sasl.Scram.Enabled)

	assert.Len(t, c.AWSClientInformation.Nodes, 80, "broker_count → len(Nodes)")
	assert.Equal(t, 1200, c.KafkaAdminClientInformation.Topics.Summary.Topics)
	assert.Equal(t, 98000, c.KafkaAdminClientInformation.Topics.Summary.TotalPartitions)
	assert.Len(t, c.KafkaAdminClientInformation.Acls, 3100, "acl_count → len(Acls)")

	in := c.ClusterMetrics.Aggregates["BytesInPerSec"]
	require.NotNil(t, in.Maximum)
	require.NotNil(t, in.P95)
	assert.InDelta(t, 3500*bytesPerMB, *in.Maximum, 1)
	assert.InDelta(t, 3500*bytesPerMB, *in.P95, 1, "P95 falls back to peak when not declared")
}

// When a state file already carries the cluster, plan-inputs OVERLAYS
// — declared fields win; un-declared fields keep their scan values.
func TestApplyClusterDeclarations_OverlayWithExistingScan(t *testing.T) {
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []types.ProcessedCluster{{
						Name:   "events",
						Region: "us-east-1",
						AWSClientInformation: types.AWSClientInformation{
							MskClusterConfig: kafkatypes.Cluster{
								ClusterType: kafkatypes.ClusterTypeProvisioned,
								Provisioned: &kafkatypes.Provisioned{
									CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{KafkaVersion: sp("3.5.0")},
									BrokerNodeGroupInfo:       &kafkatypes.BrokerNodeGroupInfo{InstanceType: sp("kafka.m5.large")},
								},
							},
							Nodes: make([]kafkatypes.NodeInfo, 3),
						},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							Topics: &types.Topics{Summary: types.TopicSummary{Topics: 50}},
							Acls:   []types.Acls{},
						},
					}},
				}},
			},
		}},
	}
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"events": {
				PeakIngressMBps: fp(120),
				PeakEgressMBps:  fp(360),
				PartitionCount:  ip(4000),
				ACLCount:        ip(2500),
				// KafkaVersion NOT overridden — scan's 3.5.0 should remain
			},
		},
	}
	applyClusterDeclarations(&state, raw)

	c := state.Sources[0].MSKData.Regions[0].Clusters[0]
	// Overlay applied:
	in := c.ClusterMetrics.Aggregates["BytesInPerSec"]
	require.NotNil(t, in.Maximum)
	assert.InDelta(t, 120*bytesPerMB, *in.Maximum, 1)
	assert.Equal(t, 4000, c.KafkaAdminClientInformation.Topics.Summary.TotalPartitions)
	assert.Len(t, c.KafkaAdminClientInformation.Acls, 2500)
	// Scan values preserved where not declared:
	assert.Equal(t, "3.5.0", *c.AWSClientInformation.MskClusterConfig.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	assert.Equal(t, "kafka.m5.large", *c.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.InstanceType)
	assert.Equal(t, 50, c.KafkaAdminClientInformation.Topics.Summary.Topics, "TopicCount not overridden → scan value")
}

// Synthesis requires Region — entries without it are dropped so they
// don't land in an unnamed bucket.
func TestApplyClusterDeclarations_SynthesisRequiresRegion(t *testing.T) {
	state := types.ProcessedState{}
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"no-region": {
				PeakIngressMBps: fp(100),
				PeakEgressMBps:  fp(300),
				PartitionCount:  ip(500),
			},
		},
	}
	applyClusterDeclarations(&state, raw)
	assert.Empty(t, state.Sources, "no Region → no synthesis")
}

// Customer declares cluster_type: SERVERLESS — synthesis builds the
// Serverless block with IAM auth only.
func TestApplyClusterDeclarations_SynthesisServerless(t *testing.T) {
	state := types.ProcessedState{}
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"srv": {
				Region:              sp("us-east-1"),
				ClusterTypeFromScan: sp("SERVERLESS"),
				AuthMethods:         []string{SourceAuthIAM},
				PartitionCount:      ip(180),
				TopicCount:          ip(28),
			},
		},
	}
	applyClusterDeclarations(&state, raw)
	require.Len(t, state.Sources, 1)
	c := state.Sources[0].MSKData.Regions[0].Clusters[0]
	assert.Equal(t, kafkatypes.ClusterTypeServerless, c.AWSClientInformation.MskClusterConfig.ClusterType)
	require.NotNil(t, c.AWSClientInformation.MskClusterConfig.Serverless)
	assert.Nil(t, c.AWSClientInformation.MskClusterConfig.Provisioned, "SERVERLESS clears the Provisioned block")
	srv := c.AWSClientInformation.MskClusterConfig.Serverless
	require.NotNil(t, srv.ClientAuthentication.Sasl.Iam)
	assert.True(t, *srv.ClientAuthentication.Sasl.Iam.Enabled)
}

// Entries that only override decision-level fields (DowntimeTolerance,
// TargetAuthMethod) MUST NOT trigger synthesis. Those are layered
// through applyClusterOverride separately.
func TestApplyClusterDeclarations_DecisionOnlyOverrideDoesNotSynthesise(t *testing.T) {
	state := types.ProcessedState{}
	dt := DowntimeZero
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"some-cluster": {DowntimeTolerance: &dt},
		},
	}
	applyClusterDeclarations(&state, raw)
	assert.Empty(t, state.Sources, "decision-level override should not trigger synthesis")
}
