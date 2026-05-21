package plan

import (
	"testing"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
}

func twoClusterState() types.ProcessedState {
	// b-cluster intentionally listed first so the sort proves it works.
	return types.ProcessedState{
		Sources: []types.ProcessedSource{
			{
				Type: types.SourceTypeMSK,
				MSKData: &types.ProcessedMSKSource{
					Regions: []types.ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []types.ProcessedCluster{
								fixtureCluster("b-cluster", 100, 5.0, 5.0, 6.0, 6.0),
								fixtureCluster("a-cluster", 50, 1.0, 1.0, 2.0, 2.0),
							},
						},
					},
				},
			},
		},
	}
}

func TestPlanServiceBuild_BasicShape(t *testing.T) {
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(twoClusterState(), defaultInputs(), "kcp-state.json")
	require.NoError(t, err)

	assert.Equal(t, "Amazon MSK", p.Header.Source)
	assert.Equal(t, "1-experimental", p.Header.PlanSchemaVersion)
	assert.Equal(t, "kcp-state.json", p.Header.StateFilePath)
	assert.Equal(t, 1, p.SourceEnvironment.TotalRegions)
	assert.Len(t, p.Sizing, 2)
	assert.Len(t, p.ClusterTypeDecision, 2)
	assert.Len(t, p.NetworkingDecision, 2)
	assert.Len(t, p.SizingAppendix, 2)
}

func TestPlanServiceBuild_DeterministicByteIdenticalJSON(t *testing.T) {
	// Build the same plan twice and ensure the rendered JSON is byte-equal.
	// This is the load-bearing reproducibility test.
	svc := NewPlanService(defaultCfg(t), fixedNow)
	state := twoClusterState()
	inputs := defaultInputs()

	p1, err := svc.Build(state, inputs, "x.json")
	require.NoError(t, err)
	p2, err := svc.Build(state, inputs, "x.json")
	require.NoError(t, err)

	j1, err := RenderJSON(p1)
	require.NoError(t, err)
	j2, err := RenderJSON(p2)
	require.NoError(t, err)
	assert.Equal(t, string(j1), string(j2), "rendered JSON must be byte-identical across builds")
}

func TestPlanServiceBuild_StableSortAcrossRegions(t *testing.T) {
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{
					// Same cluster name in two regions to prove (Region, Name) sort.
					{Name: "us-west-2", Clusters: []types.ProcessedCluster{
						withRegion(fixtureCluster("collide", 10, 1.0, 1.0, 1.0, 1.0), "us-west-2"),
					}},
					{Name: "us-east-1", Clusters: []types.ProcessedCluster{
						withRegion(fixtureCluster("collide", 10, 1.0, 1.0, 1.0, 1.0), "us-east-1"),
					}},
				},
			},
		}},
	}
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, defaultInputs(), "x.json")
	require.NoError(t, err)

	require.Len(t, p.SourceEnvironment.Clusters, 2)
	// us-east-1 sorts before us-west-2.
	assert.Equal(t, "us-east-1", p.SourceEnvironment.Clusters[0].Region)
	assert.Equal(t, "us-west-2", p.SourceEnvironment.Clusters[1].Region)
}

// The Source Environment table renders BrokerCount + TopicCount per
// cluster. Regression guard: both values must come from the populated
// state fields (`AWSClientInformation.Nodes` length and
// `KafkaAdminClientInformation.Topics.Summary.Topics`) rather than
// silently defaulting to zero — a zero in the rendered plan reads as
// "no brokers / no topics" which is a meaningfully wrong impression.
func TestPlanServiceBuild_SourceEnvironmentBrokerAndTopicCount(t *testing.T) {
	c := fixtureCluster("populated", 200, 10, 20, 12, 22)
	c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
	c.KafkaAdminClientInformation.Topics.Summary.Topics = 42

	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{
					{Name: "us-east-1", Clusters: []types.ProcessedCluster{c}},
				},
			},
		}},
	}
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, defaultInputs(), "x.json")
	require.NoError(t, err)
	require.Len(t, p.SourceEnvironment.Clusters, 1)
	got := p.SourceEnvironment.Clusters[0]
	assert.Equal(t, 3, got.BrokerCount, "BrokerCount must reflect len(AWSClientInformation.Nodes)")
	assert.Equal(t, 42, got.TopicCount, "TopicCount must reflect KafkaAdminClientInformation.Topics.Summary.Topics")
}

// Unit-level coverage for the helpers — catches a bad rename / wrong
// field path before the integration test does.
func TestBrokerCountAndTopicCountReadFromState(t *testing.T) {
	c := types.ProcessedCluster{Name: "ut"}
	assert.Equal(t, 0, brokerCount(c))
	assert.Equal(t, 0, topicCount(c))

	c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 5)
	c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 17}}
	assert.Equal(t, 5, brokerCount(c))
	assert.Equal(t, 17, topicCount(c))
}

// Each MVP open-question detector surfaces a specific state-file gap.
// The shipping recommendation still exists for every cluster; the OQ is
// the action that upgrades it.
func TestDetectOpenQuestions(t *testing.T) {
	cfg := defaultCfg(t)
	provisioned := func(name string) types.ProcessedCluster {
		c := types.ProcessedCluster{Name: name}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		return c
	}
	ent := types.ClusterTypeDecision{Verdict: types.ClusterTypeEnterprise}
	plNet := func(id string) types.NetworkingDecision {
		return types.NetworkingDecision{ClusterID: id, Verdict: types.NetworkingPNI}
	}

	t.Run("missing P95 metrics → missing_p95_metrics OQ", func(t *testing.T) {
		c := provisioned("noMetrics")
		sizing := types.ClusterSizing{ClusterID: "noMetrics", Degraded: true, DegradedReason: "no BytesInPerSec p95"}
		oqs := detectOpenQuestions(c, sizing, ent, plNet("noMetrics"), cfg)
		assertContainsOQ(t, oqs, "missing_p95_metrics", "kcp scan metrics")
	})

	t.Run("nil ACLs on PROVISIONED with topics scan → acls_not_scanned suppressed (scan ran with 0 ACLs)", func(t *testing.T) {
		// With Topics populated + PROVISIONED, aclScanRan() returns true,
		// so a successful scan that found 0 ACLs is NOT a gap.
		c := provisioned("provisioned-zero-acls")
		c.KafkaAdminClientInformation.Acls = nil
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "provisioned-zero-acls", FinalECKU: 1}, ent, plNet("provisioned-zero-acls"), cfg)
		for _, oq := range oqs {
			assert.NotEqual(t, "acls_not_scanned", oq.ID, "scan-with-0 must NOT emit the OQ")
		}
	})

	t.Run("no topics scan + nil ACLs → acls_not_scanned OQ", func(t *testing.T) {
		c := types.ProcessedCluster{Name: "noAcls"}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		// Topics nil → scan didn't run; ACLs nil follows from same gap
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "noAcls", FinalECKU: 1}, ent, plNet("noAcls"), cfg)
		assertContainsOQ(t, oqs, "acls_not_scanned", "admin Kafka credentials")
	})

	t.Run("SERVERLESS cluster suppresses acls_not_scanned and broker_inventory_empty", func(t *testing.T) {
		c := types.ProcessedCluster{Name: "serverless"}
		c.AWSClientInformation.Nodes = nil
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = nil
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "serverless", FinalECKU: 1}, ent, plNet("serverless"), cfg)
		for _, oq := range oqs {
			assert.NotEqual(t, "acls_not_scanned", oq.ID, "serverless does not expose ACLs via this API")
			assert.NotEqual(t, "broker_inventory_empty", oq.ID, "serverless has no broker nodes by design")
		}
	})

	t.Run("zero brokers on PROVISIONED → broker_inventory_empty OQ", func(t *testing.T) {
		c := provisioned("noBrokers")
		c.AWSClientInformation.Nodes = nil
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "noBrokers", FinalECKU: 1}, ent, plNet("noBrokers"), cfg)
		assertContainsOQ(t, oqs, "broker_inventory_empty", "kcp discover")
	})

	t.Run("zero topics → topic_inventory_empty OQ", func(t *testing.T) {
		c := provisioned("noTopics")
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 0}}
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "noTopics", FinalECKU: 1}, ent, plNet("noTopics"), cfg)
		assertContainsOQ(t, oqs, "topic_inventory_empty", "kcp scan clusters")
	})

	t.Run("PrivateLink trigger + sized > cap → networking_privatelink_over_cap OQ", func(t *testing.T) {
		c := provisioned("overCap")
		sizing := types.ClusterSizing{ClusterID: "overCap", FinalECKU: 15} // > 10 eCKU PL cap
		net := types.NetworkingDecision{ClusterID: "overCap", Verdict: types.NetworkingPrivateLink}
		oqs := detectOpenQuestions(c, sizing, ent, net, cfg)
		assertContainsOQ(t, oqs, "networking_privatelink_over_cap", "account team")
	})

	t.Run("PrivateLink with sized <= cap → no over-cap OQ", func(t *testing.T) {
		c := provisioned("underCap")
		sizing := types.ClusterSizing{ClusterID: "underCap", FinalECKU: 5}
		net := types.NetworkingDecision{ClusterID: "underCap", Verdict: types.NetworkingPrivateLink}
		oqs := detectOpenQuestions(c, sizing, ent, net, cfg)
		for _, oq := range oqs {
			assert.NotEqual(t, "networking_privatelink_over_cap", oq.ID)
		}
	})

	t.Run("PNI with any sizing → no over-cap OQ (only fires on PrivateLink verdict)", func(t *testing.T) {
		c := provisioned("pniBig")
		sizing := types.ClusterSizing{ClusterID: "pniBig", FinalECKU: 25}
		net := types.NetworkingDecision{ClusterID: "pniBig", Verdict: types.NetworkingPNI}
		oqs := detectOpenQuestions(c, sizing, ent, net, cfg)
		for _, oq := range oqs {
			assert.NotEqual(t, "networking_privatelink_over_cap", oq.ID)
		}
	})

	t.Run("spiky workload does NOT generate an OQ (FYI only)", func(t *testing.T) {
		c := provisioned("spiky")
		sizing := types.ClusterSizing{ClusterID: "spiky", FinalECKU: 2, SpikyIngress: true}
		oqs := detectOpenQuestions(c, sizing, ent, plNet("spiky"), cfg)
		assert.Empty(t, oqs, "spiky clusters should NOT emit an OQ — informational only")
	})

	t.Run("fully populated cluster emits no OQs", func(t *testing.T) {
		c := provisioned("complete")
		oqs := detectOpenQuestions(c, types.ClusterSizing{ClusterID: "complete", FinalECKU: 2}, ent, plNet("complete"), cfg)
		assert.Empty(t, oqs, "happy-path clusters should not surface OQs")
	})
}

func assertContainsOQ(t *testing.T, oqs []types.OpenQuestion, id, expectedSubstring string) {
	t.Helper()
	for _, oq := range oqs {
		if oq.ID == id {
			if expectedSubstring != "" {
				assert.Contains(t, oq.HowToClose, expectedSubstring)
			}
			return
		}
	}
	t.Fatalf("expected an OpenQuestion with ID %q, got: %+v", id, oqs)
}

func TestPlanServiceBuild_EmptyState(t *testing.T) {
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(types.ProcessedState{}, defaultInputs(), "")
	require.NoError(t, err)
	assert.Empty(t, p.Sizing)
	assert.Empty(t, p.SourceEnvironment.Clusters)
	assert.Equal(t, 0, p.SourceEnvironment.TotalRegions)
}

func TestPlanServiceBuild_DegradedClusterStillRenders(t *testing.T) {
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []types.ProcessedCluster{{
						Name:                        "no-metrics",
						Region:                      "us-east-1",
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: 5}}},
					}},
				}},
			},
		}},
	}
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, defaultInputs(), "x.json")
	require.NoError(t, err)
	require.Len(t, p.Sizing, 1)
	assert.True(t, p.Sizing[0].Degraded)
	// No appendix entry for degraded clusters (nothing to show).
	assert.Empty(t, p.SizingAppendix)
}

func withRegion(c types.ProcessedCluster, region string) types.ProcessedCluster {
	c.Region = region
	return c
}
