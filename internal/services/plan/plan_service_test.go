package plan

import (
	"testing"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
}

func twoClusterState() report.ProcessedState {
	// b-cluster intentionally listed first so the sort proves it works.
	return report.ProcessedState{
		Sources: []report.ProcessedSource{
			{
				Type: types.SourceTypeMSK,
				MSKData: &report.ProcessedMSKSource{
					Regions: []report.ProcessedRegion{
						{
							Name: "us-east-1",
							Clusters: []report.ProcessedCluster{
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
	assert.Equal(t, "1", p.Header.PlanSchemaVersion)
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
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{
					// Same cluster name in two regions to prove (Region, Name) sort.
					{Name: "us-west-2", Clusters: []report.ProcessedCluster{
						withRegion(fixtureCluster("collide", 10, 1.0, 1.0, 1.0, 1.0), "us-west-2"),
					}},
					{Name: "us-east-1", Clusters: []report.ProcessedCluster{
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

	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{
					{Name: "us-east-1", Clusters: []report.ProcessedCluster{c}},
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

// Each MVP open-question detector surfaces a specific state-file gap.
// The shipping recommendation still exists for every cluster; the OQ is
// the action that upgrades it.
func TestDetectOpenQuestions(t *testing.T) {
	cfg := defaultCfg(t)
	provisioned := func(name string) report.ProcessedCluster {
		c := report.ProcessedCluster{Name: name}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		c.AWSClientInformation.MskClusterConfig.Provisioned = &kafkatypes.Provisioned{} // non-nil to satisfy cluster_type_unrecognised guard
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = []types.Acls{} // non-nil means "scan ran" under aclScanRan's current heuristic
		return c
	}
	ent := ClusterTypeDecision{Verdict: ClusterTypeEnterprise}
	pniNet := func(id string) NetworkingDecision {
		return NetworkingDecision{ClusterID: id, Verdict: NetworkingPNI}
	}
	// scramAuth is the default test fixture for AuthDecision — a single
	// SCRAM source. Non-empty SourceAuths suppresses the
	// `auth_posture_unknown` OQ, which is what every test here cares
	// about (those tests assert on other OQ IDs).
	scramAuth := func(id string) AuthDecision {
		return AuthDecision{ClusterID: id, SourceAuths: []string{SourceAuthSCRAM}}
	}

	t.Run("missing P95 metrics → missing_p95_metrics OQ", func(t *testing.T) {
		c := provisioned("noMetrics")
		sizing := ClusterSizing{ClusterID: "noMetrics", Degraded: true, DegradedReason: "no BytesInPerSec p95"}
		oqs := detectOpenQuestions(c, sizing, ent, pniNet("noMetrics"), scramAuth("noMetrics"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		// Metrics are collected by `kcp discover` (not a separate `scan metrics` subcommand).
		assertContainsOQ(t, oqs, "missing_p95_metrics", "kcp discover")
	})

	t.Run("scan ran with 0 ACLs (non-nil empty slice) → acls_not_scanned suppressed", func(t *testing.T) {
		// kafka_service.go now writes `[]types.Acls{}` on a successful
		// scan with 0 ACLs, so a non-nil-but-empty slice is the
		// trustworthy "scan ran" signal — aclScanRan returns true and
		// the OQ is suppressed.
		c := provisioned("provisioned-zero-acls") // helper sets Acls = []types.Acls{}
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "provisioned-zero-acls", FinalECKU: 1}, ent, pniNet("provisioned-zero-acls"), scramAuth("provisioned-zero-acls"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		for _, oq := range oqs {
			assert.NotEqual(t, "acls_not_scanned", oq.ID, "scan-ran-with-0 must NOT emit the OQ")
		}
	})

	t.Run("nil ACLs on PROVISIONED (scan didn't run / --skip-acls / 0-ACL scan) → acls_not_scanned OQ", func(t *testing.T) {
		// The current scanner can't distinguish these three states —
		// they all produce Acls=nil. The OQ fires conservatively so
		// the customer can rule out the ACL-cap risk.
		c := provisioned("provisioned-nil-acls")
		c.KafkaAdminClientInformation.Acls = nil
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "provisioned-nil-acls", FinalECKU: 1}, ent, pniNet("provisioned-nil-acls"), scramAuth("provisioned-nil-acls"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assertContainsOQ(t, oqs, "acls_not_scanned", "--skip-acls")
	})

	t.Run("no topics scan + nil ACLs → acls_not_scanned OQ", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "noAcls"}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		// Topics nil → scan didn't run; ACLs nil follows from same gap
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "noAcls", FinalECKU: 1}, ent, pniNet("noAcls"), scramAuth("noAcls"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assertContainsOQ(t, oqs, "acls_not_scanned", "admin Kafka credentials")
	})

	t.Run("SERVERLESS cluster suppresses acls_not_scanned and broker_inventory_empty", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "serverless"}
		c.AWSClientInformation.Nodes = nil
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = nil
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "serverless", FinalECKU: 1}, ent, pniNet("serverless"), scramAuth("serverless"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		for _, oq := range oqs {
			assert.NotEqual(t, "acls_not_scanned", oq.ID, "serverless does not expose ACLs via this API")
			assert.NotEqual(t, "broker_inventory_empty", oq.ID, "serverless has no broker nodes by design")
		}
	})

	t.Run("zero brokers on PROVISIONED → broker_inventory_empty OQ", func(t *testing.T) {
		c := provisioned("noBrokers")
		c.AWSClientInformation.Nodes = nil
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "noBrokers", FinalECKU: 1}, ent, pniNet("noBrokers"), scramAuth("noBrokers"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assertContainsOQ(t, oqs, "broker_inventory_empty", "kcp discover")
	})

	t.Run("zero topics → topic_inventory_empty OQ", func(t *testing.T) {
		c := provisioned("noTopics")
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 0}}
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "noTopics", FinalECKU: 1}, ent, pniNet("noTopics"), scramAuth("noTopics"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assertContainsOQ(t, oqs, "topic_inventory_empty", "kcp scan clusters")
	})

	t.Run("Serverless with nil Topics → scan-gap body, not 'Summary.Topics is 0'", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "srv-nil"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		c.AWSClientInformation.MskClusterConfig.Serverless = &kafkatypes.Serverless{
			VpcConfigs: []kafkatypes.VpcConfig{{SubnetIds: []string{"s"}}},
		}
		// Topics intentionally left nil.
		auth := AuthDecision{ClusterID: "srv-nil", SourceAuths: []string{SourceAuthIAM}}
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "srv-nil", FinalECKU: 1}, ent, pniNet("srv-nil"), auth, cfg, PlanInputsResolved{SizingPercentile: "p95"})
		var topicOQ *OpenQuestion
		for i, oq := range oqs {
			if oq.ID == "topic_inventory_empty" {
				topicOQ = &oqs[i]
				break
			}
		}
		require.NotNil(t, topicOQ, "Serverless + nil Topics must still fire topic_inventory_empty")
		assert.Contains(t, topicOQ.Body, "is absent on this cluster", "nil Topics must use scan-gap wording")
		assert.NotContains(t, topicOQ.Body, "Summary.Topics` is 0", "nil case must NOT use the 'Summary observed as 0' wording")
	})

	t.Run("PrivateLink trigger + sized > cap → networking_privatelink_over_cap OQ", func(t *testing.T) {
		c := provisioned("overCap")
		sizing := ClusterSizing{ClusterID: "overCap", FinalECKU: 15} // > 10 eCKU PL cap
		net := NetworkingDecision{ClusterID: "overCap", Verdict: NetworkingPrivateLink}
		oqs := detectOpenQuestions(c, sizing, ent, net, scramAuth(c.Name), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assertContainsOQ(t, oqs, "networking_privatelink_over_cap", "account team")
	})

	t.Run("PrivateLink with sized <= cap → no over-cap OQ", func(t *testing.T) {
		c := provisioned("underCap")
		sizing := ClusterSizing{ClusterID: "underCap", FinalECKU: 5}
		net := NetworkingDecision{ClusterID: "underCap", Verdict: NetworkingPrivateLink}
		oqs := detectOpenQuestions(c, sizing, ent, net, scramAuth(c.Name), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		for _, oq := range oqs {
			assert.NotEqual(t, "networking_privatelink_over_cap", oq.ID)
		}
	})

	t.Run("PNI with any sizing → no over-cap OQ (only fires on PrivateLink verdict)", func(t *testing.T) {
		c := provisioned("pniBig")
		sizing := ClusterSizing{ClusterID: "pniBig", FinalECKU: 25}
		net := NetworkingDecision{ClusterID: "pniBig", Verdict: NetworkingPNI}
		oqs := detectOpenQuestions(c, sizing, ent, net, scramAuth(c.Name), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		for _, oq := range oqs {
			assert.NotEqual(t, "networking_privatelink_over_cap", oq.ID)
		}
	})

	t.Run("spiky workload does NOT generate an OQ (FYI only)", func(t *testing.T) {
		c := provisioned("spiky")
		sizing := ClusterSizing{ClusterID: "spiky", FinalECKU: 2, SpikyIngress: true}
		oqs := detectOpenQuestions(c, sizing, ent, pniNet("spiky"), scramAuth("spiky"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assert.Empty(t, oqs, "spiky clusters should NOT emit an OQ — informational only")
	})

	t.Run("fully populated cluster emits no OQs", func(t *testing.T) {
		c := provisioned("complete")
		oqs := detectOpenQuestions(c, ClusterSizing{ClusterID: "complete", FinalECKU: 2}, ent, pniNet("complete"), scramAuth("complete"), cfg, PlanInputsResolved{SizingPercentile: "p95"})
		assert.Empty(t, oqs, "happy-path clusters should not surface OQs")
	})
}

func assertContainsOQ(t *testing.T, oqs []OpenQuestion, id, expectedSubstring string) {
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
	p, err := svc.Build(report.ProcessedState{}, defaultInputs(), "")
	require.NoError(t, err)
	assert.Empty(t, p.Sizing)
	assert.Empty(t, p.SourceEnvironment.Clusters)
	assert.Equal(t, 0, p.SourceEnvironment.TotalRegions)
}

func TestPlanServiceBuild_DegradedClusterStillRenders(t *testing.T) {
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{{
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

func withRegion(c report.ProcessedCluster, region string) report.ProcessedCluster {
	c.Region = region
	return c
}

// State with OSKSources but no MSK clusters MUST surface the
// osk_source_unsupported OQ so the customer knows on-prem clusters
// were silently dropped. Today the plan only covers MSK.
func TestDetectOSKSourceOpenQuestion_FiresWhenOSKClustersPresent(t *testing.T) {
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{
			{Type: types.SourceTypeOSK, OSKData: &report.ProcessedOSKSource{
				Clusters: []report.ProcessedOSKCluster{{ID: "onprem-1"}, {ID: "onprem-2"}},
			}},
		},
	}
	oqs := detectOSKSourceOpenQuestion(state)
	require.Len(t, oqs, 1)
	assert.Equal(t, "osk_source_unsupported", oqs[0].ID)
	assert.Contains(t, oqs[0].Title, "2 on-prem Kafka clusters")
}

func TestDetectOSKSourceOpenQuestion_NoOQWhenOSKAbsent(t *testing.T) {
	state := report.ProcessedState{}
	assert.Empty(t, detectOSKSourceOpenQuestion(state))
}

// Singular vs plural verb agreement in the OQ title.
func TestDetectOSKSourceOpenQuestion_VerbAgreement(t *testing.T) {
	t.Run("1 cluster → 'isn't' + singular noun", func(t *testing.T) {
		state := report.ProcessedState{Sources: []report.ProcessedSource{
			{Type: types.SourceTypeOSK, OSKData: &report.ProcessedOSKSource{
				Clusters: []report.ProcessedOSKCluster{{ID: "solo"}},
			}},
		}}
		oqs := detectOSKSourceOpenQuestion(state)
		require.Len(t, oqs, 1)
		assert.Contains(t, oqs[0].Title, "1 on-prem Kafka cluster in the state file isn't")
	})
	t.Run("2 clusters → 'aren't' + plural noun", func(t *testing.T) {
		state := report.ProcessedState{Sources: []report.ProcessedSource{
			{Type: types.SourceTypeOSK, OSKData: &report.ProcessedOSKSource{
				Clusters: []report.ProcessedOSKCluster{{ID: "a"}, {ID: "b"}},
			}},
		}}
		oqs := detectOSKSourceOpenQuestion(state)
		require.Len(t, oqs, 1)
		assert.Contains(t, oqs[0].Title, "2 on-prem Kafka clusters in the state file aren't")
	})
}

// MskClusterConfig.ClusterType outside the recognised enum (empty or
// future variant) MUST surface cluster_type_unrecognised so the
// customer knows the Provisioned-shaped helpers silently returned
// empty for that cluster.
func TestInputsMissing_UnknownClusterTypeReportsGap(t *testing.T) {
	t.Run("empty ClusterType → msk_cluster_config", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "no-type"}
		// ClusterType left as zero-value
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		assert.Contains(t, inputsMissing(c), "msk_cluster_config")
	})
	t.Run("PROVISIONED with nil Provisioned → msk_cluster_config", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "nil-prov"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		// Provisioned block left as nil
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		assert.Contains(t, inputsMissing(c), "msk_cluster_config")
	})
	t.Run("Future variant ClusterType → msk_cluster_config", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "future"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = "HYBRID"
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		assert.Contains(t, inputsMissing(c), "msk_cluster_config")
	})
	t.Run("Serverless does NOT report msk_cluster_config", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "srv"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		c.AWSClientInformation.MskClusterConfig.Serverless = &kafkatypes.Serverless{
			VpcConfigs: []kafkatypes.VpcConfig{{SubnetIds: []string{"s"}}},
		}
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
		assert.NotContains(t, inputsMissing(c), "msk_cluster_config")
	})
}

func TestInputsMissing_AllSignalsTracked(t *testing.T) {
	t.Run("fully-scanned PROVISIONED cluster reports no missing inputs", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "ok"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		// Provisioned block must be populated — without it, the
		// Provisioned-only helpers return empty and the new
		// `msk_cluster_config` inputs-missing entry fires.
		c.AWSClientInformation.MskClusterConfig.Provisioned = &kafkatypes.Provisioned{}
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		assert.Empty(t, inputsMissing(c))
	})
	t.Run("Topics nil → 'topics' in the list", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "no-topics"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		assert.Contains(t, inputsMissing(c), "topics")
	})
	t.Run("Acls nil on PROVISIONED → 'acls' in the list", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "no-acls"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		c.AWSClientInformation.Nodes = make([]kafkatypes.NodeInfo, 3)
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		assert.Contains(t, inputsMissing(c), "acls")
	})
	t.Run("Serverless suppresses 'acls' and 'brokers'", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "sl"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		missing := inputsMissing(c)
		assert.NotContains(t, missing, "acls", "serverless: ACLs not exposed; not a gap")
		assert.NotContains(t, missing, "brokers", "serverless: no broker nodes by design; not a gap")
	})
	t.Run("0 broker Nodes on PROVISIONED → 'brokers' in the list", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "no-brokers"}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
		c.KafkaAdminClientInformation.Topics = &types.Topics{Summary: types.TopicSummary{Topics: 5}}
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
		assert.Contains(t, inputsMissing(c), "brokers")
	})
}

func TestDecideClusterType_EvaluatedRules_CarriesAllOutcomes(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", FinalECKU: 5}

	t.Run("nil-Acls PROVISIONED: skipped rule has SkipReason", func(t *testing.T) {
		c := provisionedClusterWithScan("nil-acls", nil)
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		require.Len(t, d.EvaluatedRules, len(hardLimitCatalog), "EvaluatedRules must carry every catalog entry")
		for _, r := range d.EvaluatedRules {
			if r.RowID == ruleACLCountExceedsCap {
				assert.Equal(t, RuleSkipped, r.Outcome)
				assert.NotEmpty(t, r.SkipReason, "skipped rule must carry a SkipReason")
				assert.Empty(t, r.Evidence, "skipped rule must NOT carry Evidence")
				return
			}
		}
		t.Fatalf("acl_count_exceeds_cap row missing from EvaluatedRules")
	})
	t.Run("non-fired rule carries negative evidence", func(t *testing.T) {
		c := provisionedClusterWithScan("ok", []types.Acls{})
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		for _, r := range d.EvaluatedRules {
			if r.RowID == ruleACLCountExceedsCap {
				assert.Equal(t, RuleNotFired, r.Outcome)
				assert.Contains(t, r.Evidence, "≤", "not_fired rule must surface negative evidence")
				return
			}
		}
		t.Fatalf("acl_count_exceeds_cap row missing")
	})
	t.Run("customer-declared fired rule carries Evidence + fires", func(t *testing.T) {
		c := provisionedClusterWithScan("schema-cluster", []types.Acls{})
		in := defaultInputs()
		in.EnforceSchemasAtTheBroker = true
		d := decideClusterType(c, sizing, cfg, in)
		var found bool
		for _, r := range d.EvaluatedRules {
			if r.RowID == ruleBrokerSideSchemaValidation {
				assert.Equal(t, RuleFired, r.Outcome)
				assert.NotEmpty(t, r.Evidence)
				found = true
				break
			}
		}
		assert.True(t, found, "broker_side_schema_validation_required row missing")
	})
}

// Per-cluster overrides win on a per-cluster basis: one cluster gets
// Dedicated SZ from a per-cluster SLA flag; the other stays Enterprise
// despite the global default. This is the regression guard for the
// heterogeneous-fleet failure mode that motivated A3.
func TestPlanServiceBuild_PerClusterOverride_FlipsOnlyTargetCluster(t *testing.T) {
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{
						fixtureCluster("alpha", 100, 5.0, 5.0, 6.0, 6.0),
						fixtureCluster("bravo", 100, 5.0, 5.0, 6.0, 6.0),
					},
				}},
			},
		}},
	}
	flag := true
	rawInputs := &PlanInputs{
		Clusters: map[string]ClusterPlanInputs{
			"alpha": {Requires9995SLAWithinSingleZone: &flag}, // SZ for alpha only
		},
	}
	resolved := ResolvePlanInputs(rawInputs, defaultCfg(t))
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, resolved, "x.json")
	require.NoError(t, err)
	require.Len(t, p.ClusterTypeDecision, 2)

	byID := map[string]ClusterTypeDecision{}
	for _, d := range p.ClusterTypeDecision {
		byID[d.ClusterID] = d
	}

	assert.Equal(t, ClusterTypeDedicated, byID["alpha"].Verdict, "per-cluster override must flip alpha to Dedicated")
	assert.Equal(t, TopologySingleZone, byID["alpha"].Topology, "SLA flag drives SZ topology")
	assert.Equal(t, ClusterTypeEnterprise, byID["bravo"].Verdict, "non-overridden cluster must keep the global default (Enterprise)")
}

// Per-cluster target_auth_method override flips the effective target
// only for the named cluster; everything else stays on the per-source
// auth_mapping default.
func TestPlanServiceBuild_PerClusterTargetAuthOverride(t *testing.T) {
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{
						attachAuth(fixtureCluster("alpha", 100, 5.0, 5.0, 6.0, 6.0), SourceAuthSCRAM),
						attachAuth(fixtureCluster("bravo", 100, 5.0, 5.0, 6.0, 6.0), SourceAuthSCRAM),
					},
				}},
			},
		}},
	}
	oauth := TargetAuthOAuth
	rawInputs := &PlanInputs{
		Clusters: map[string]ClusterPlanInputs{
			"alpha": {TargetAuthMethod: &oauth},
		},
	}
	resolved := ResolvePlanInputs(rawInputs, defaultCfg(t))
	svc := NewPlanService(defaultCfg(t), fixedNow)
	p, err := svc.Build(state, resolved, "x.json")
	require.NoError(t, err)
	require.Len(t, p.Auth, 2)

	byID := map[string]AuthDecision{}
	for _, a := range p.Auth {
		byID[a.ClusterID] = a
	}
	require.Len(t, byID["alpha"].TargetMappings, 1)
	require.Len(t, byID["bravo"].TargetMappings, 1)
	assert.Equal(t, TargetAuthOAuth, byID["alpha"].TargetMappings[0].EffectiveTarget, "alpha override must replace the default API-Keys target")
	assert.Equal(t, TargetAuthAPIKeys, byID["bravo"].TargetMappings[0].EffectiveTarget, "bravo must keep the per-source default (SCRAM → API Keys)")
}

// detectStaleStateOQ fires only when the source state file is older
// than the configured threshold. Threshold-1 day is silent; threshold
// + 1 day surfaces a 🟡 OQ with the day delta in the title.
func TestDetectStaleStateOQ_HonorsThreshold(t *testing.T) {
	gen := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	// Boundary: exactly threshold-days old → silent.
	stamp := gen.AddDate(0, 0, -7).Add(time.Hour) // 6d 23h
	require.Empty(t, detectStaleStateOQ(stamp, gen, 7))
	// 8 days old → fires.
	stamp = gen.AddDate(0, 0, -8)
	oqs := detectStaleStateOQ(stamp, gen, 7)
	require.Len(t, oqs, 1)
	assert.Equal(t, "state_file_stale", oqs[0].ID)
	assert.Contains(t, oqs[0].Title, "8 days old")
	// Zero timestamp → suppressed (no state-file stamp recorded).
	require.Empty(t, detectStaleStateOQ(time.Time{}, gen, 7))
}

// detectAuthFleetOpenQuestions emits target_auth_method_unknown when
// the global override is a typo, and stays silent for the recognised
// values + the empty default.
func TestDetectAuthFleetOpenQuestions_TargetAuthMethodTypo(t *testing.T) {
	resolved := PlanInputsResolved{TargetAuthMethod: "oauthhh"}
	oqs := detectAuthFleetOpenQuestions(nil, resolved)
	require.Len(t, oqs, 1)
	assert.Equal(t, "target_auth_method_unknown", oqs[0].ID)
	assert.Contains(t, oqs[0].Title, "oauthhh")

	// Recognised value → silent.
	resolved.TargetAuthMethod = TargetAuthOAuth
	assert.Empty(t, detectAuthFleetOpenQuestions(nil, resolved))
	// Empty (default) → silent.
	resolved.TargetAuthMethod = ""
	assert.Empty(t, detectAuthFleetOpenQuestions(nil, resolved))
}

// Per-cluster target_auth_method typos surface as cluster-scoped OQs
// so the affected cluster is obvious; without this the override
// silently falls back to the per-source default.
func TestDetectAuthFleetOpenQuestions_PerClusterTargetAuthTypo(t *testing.T) {
	typo := "oauthhh"
	good := TargetAuthOAuth
	raw := &PlanInputs{
		Clusters: map[string]ClusterPlanInputs{
			"alpha": {TargetAuthMethod: &typo},
			"bravo": {TargetAuthMethod: &good},
		},
	}
	clusters := []report.ProcessedCluster{{Name: "alpha"}, {Name: "bravo"}}
	resolved := PlanInputsResolved{Raw: raw}
	oqs := detectAuthFleetOpenQuestions(clusters, resolved)
	require.Len(t, oqs, 1, "only the typo cluster should emit an OQ")
	assert.Equal(t, "target_auth_method_unknown", oqs[0].ID)
	assert.Equal(t, "alpha", oqs[0].ClusterID)
	assert.Contains(t, oqs[0].Title, "oauthhh")
}

// ambiguousGatewayIntent ignores `iam_pre_migration_status` for fleets
// that don't use IAM — otherwise a non-IAM fleet that accidentally
// flipped that prereq would lose the `gateway_intent_unconfirmed` OQ.
func TestAmbiguousGatewayIntent_IgnoresIAMPrereqWhenNoIAM(t *testing.T) {
	inputs := PlanInputsResolved{
		PreferGateway:                true,
		ConfluentForKubernetesStatus: PrereqNotStarted,
		CCGatewayLicenseStatus:       PrereqNotStarted,
		IAMPreMigrationStatus:        PrereqStatusInProgressInput,
	}
	assert.True(t, ambiguousGatewayIntent(inputs, false),
		"non-IAM fleet: stray iam_pre_migration_status must not disqualify the ambiguous state")
	assert.False(t, ambiguousGatewayIntent(inputs, true),
		"IAM fleet: an advanced iam_pre_migration_status moves past ambiguity")
}

// promoteSeverity flips gateway_intent_unconfirmed and
// gateway_prereqs_pending from 🟢 to 🔴 — and rewrites the title — when
// auth_target_gateway_incompatible is present in the same Plan. Without
// the sibling the base severity + original title pass through.
func TestPromoteSeverity_SiblingTriggersBlockerForGatewayOQs(t *testing.T) {
	siblings := map[string]bool{"auth_target_gateway_incompatible": true}
	emptySiblings := map[string]bool{}

	for _, id := range []string{"gateway_intent_unconfirmed", "gateway_prereqs_pending"} {
		meta := oqMetaFor(id)
		sev, title := promoteSeverity(meta, "original title", siblings)
		assert.Equal(t, "🔴", sev, id+" must promote to 🔴 when auth sibling fires")
		assert.NotEqual(t, "original title", title, id+" must rewrite its title when promoted")

		sev, title = promoteSeverity(meta, "original title", emptySiblings)
		assert.Equal(t, "🟢", sev, id+" must stay 🟢 without the sibling")
		assert.Equal(t, "original title", title, id+" must keep original title without promotion")
	}
}

// severityLegend renders only the severities present in this Plan —
// no 🔴 entry when nothing surfaces as blocker, etc.
func TestSeverityLegend_OnlyPresent(t *testing.T) {
	assert.Empty(t, severityLegend(map[string]bool{}))

	yellowOnly := severityLegend(map[string]bool{"🟡": true})
	assert.Contains(t, yellowOnly, "🟡")
	assert.NotContains(t, yellowOnly, "🔴")
	assert.NotContains(t, yellowOnly, "🟢")

	all := severityLegend(map[string]bool{"🔴": true, "🟡": true, "🟢": true})
	assert.Contains(t, all, "🔴")
	assert.Contains(t, all, "🟡")
	assert.Contains(t, all, "🟢")
}

// attachAuth gives a fixture cluster a SCRAM (or other) source auth so
// it surfaces in decideAuth output. Mirrors withSourceAuth in
// cutover_test.go but layers on top of an existing fixtureCluster.
func attachAuth(c report.ProcessedCluster, sourceAuth string) report.ProcessedCluster {
	enabled := true
	clientAuth := &kafkatypes.ClientAuthentication{}
	switch sourceAuth {
	case SourceAuthSCRAM:
		clientAuth.Sasl = &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: &enabled}}
	case SourceAuthIAM:
		clientAuth.Sasl = &kafkatypes.Sasl{Iam: &kafkatypes.Iam{Enabled: &enabled}}
	}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
	c.AWSClientInformation.MskClusterConfig.Provisioned = &kafkatypes.Provisioned{ClientAuthentication: clientAuth}
	return c
}
