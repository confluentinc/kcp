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
	// Need at least one MSK cluster — detectRedFlags returns nil on
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

// Empty fleet (no MSK clusters) → detectRedFlags returns nil so the
// renderer omits the §Red Flags section entirely.
func TestDetectRedFlags_EmptyFleetReturnsNil(t *testing.T) {
	assert.Nil(t, detectRedFlags(types.ProcessedState{}, &types.Plan{}, defaultCfg(t), defaultInputs()))
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
// calling detectRedFlags in isolation. Same default time / cfg used
// by the rest of the plan tests.
func buildPlanForRedFlags(t *testing.T, state types.ProcessedState, cfg *PlanConfig, inputs types.PlanInputsResolved) *types.Plan {
	t.Helper()
	svc := NewPlanService(cfg, fixedNow)
	p, err := svc.Build(state, inputs, "redflags-test.json")
	require.NoError(t, err)
	return p
}

// serverlessCluster builds a ProcessedCluster shaped like an MSK
// Serverless cluster: ClusterType=Serverless, `Provisioned` left nil,
// and the Serverless block carries the (IAM-only) ClientAuthentication.
// Mirrors the JSON shape AWS returns — see PR #317 review by adrian-januzi.
func serverlessCluster(name string) types.ProcessedCluster {
	c := types.ProcessedCluster{Name: name, Region: "us-east-1"}
	enabled := true
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
	c.AWSClientInformation.MskClusterConfig.Serverless = &kafkatypes.Serverless{
		ClientAuthentication: &kafkatypes.ServerlessClientAuthentication{
			Sasl: &kafkatypes.ServerlessSasl{Iam: &kafkatypes.Iam{Enabled: &enabled}},
		},
	}
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{{Name: name + "-topic"}}}
	return c
}

// Serverless clusters must not trigger Provisioned-only Red Flag rows
// (Kafka version below floor, Express tier, tiered storage) just
// because their Provisioned-shaped fields are nil. Without the explicit
// skips, the version row falsely reports "kafka_version missing" for
// every Serverless cluster.
func TestRedFlags_ServerlessSkipsProvisionedOnlyRows(t *testing.T) {
	srv := serverlessCluster("serverless-only")
	state := wrapClusters(srv)
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)

	kafkaRow := findRow(t, plan.RedFlags, RedFlagIDKafkaVersionBelowCLFloor)
	assert.Equal(t, types.RedFlagNotTriggered, kafkaRow.Status, "Serverless has no Kafka version — row must NOT fire Unknown")

	expressRow := findRow(t, plan.RedFlags, RedFlagIDMSKExpressBrokerTier)
	assert.Equal(t, types.RedFlagNotTriggered, expressRow.Status, "Serverless is a distinct tier from Express")

	tieredRow := findRow(t, plan.RedFlags, RedFlagIDTieredStorageInUse)
	assert.Equal(t, types.RedFlagNotTriggered, tieredRow.Status, "Serverless has no StorageMode concept")
}

// Mixed-fleet variant: Serverless cluster alongside a Provisioned
// cluster with a real Kafka-version-below-floor hit. The Provisioned
// hit must still fire; the Serverless cluster must NOT show up in
// the row's evidence or trip the row to Unknown.
func TestRedFlags_ServerlessDoesntPolluteVersionEvidence(t *testing.T) {
	below := redFlagCluster("old-provisioned", "2.2.1", "", "")
	srv := serverlessCluster("serverless-mixed")
	state := wrapClusters(below, srv)
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)

	row := findRow(t, plan.RedFlags, RedFlagIDKafkaVersionBelowCLFloor)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "old-provisioned")
	assert.NotContains(t, row.Evidence, "serverless-mixed", "Serverless cluster must not appear in version-row evidence")
}

// Serverless cluster with NO ClientAuthentication block at all (the
// AWS API allows this — auth may be wired later). The auth-detection
// path must NOT panic, and the cluster must show up in the §Source
// Environment table with `_none detected_` rather than crashing or
// being silently dropped.
func TestRedFlags_ServerlessNoClientAuthentication(t *testing.T) {
	c := types.ProcessedCluster{Name: "srv-noauth", Region: "us-east-1"}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
	c.AWSClientInformation.MskClusterConfig.Serverless = &kafkatypes.Serverless{
		VpcConfigs: []kafkatypes.VpcConfig{{SubnetIds: []string{"subnet-x"}, SecurityGroupIds: []string{"sg-x"}}},
		// ClientAuthentication intentionally nil
	}
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{{Name: "t"}}}
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	iamRow := findRow(t, plan.RedFlags, RedFlagIDIAMAuthEnabled)
	assert.Equal(t, types.RedFlagNotTriggered, iamRow.Status, "no auth block → no IAM detected")
	// Cluster surfaces as Serverless and the auth fallback is empty.
	require.Len(t, plan.Auth, 1)
	assert.Empty(t, plan.Auth[0].SourceAuths)
}

// Multi-region Serverless: two Serverless clusters across two regions
// must both surface in §1 caveats, neither pollutes the Provisioned-
// only Red Flag rows, and the multi-region Red Flag fires correctly
// based on REGION count (not cluster count).
func TestRedFlags_ServerlessMultiRegion(t *testing.T) {
	srvEast := serverlessCluster("srv-east")
	srvEast.Region = "us-east-1"
	srvWest := serverlessCluster("srv-west")
	srvWest.Region = "us-west-2"
	state := types.ProcessedState{
		Sources: []types.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: []types.ProcessedRegion{
					{Name: "us-east-1", Clusters: []types.ProcessedCluster{srvEast}},
					{Name: "us-west-2", Clusters: []types.ProcessedCluster{srvWest}},
				},
			},
		}},
	}
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)

	multiRegion := findRow(t, plan.RedFlags, RedFlagIDMultiRegionSource)
	assert.Equal(t, types.RedFlagTriggered, multiRegion.Status, "two regions → multi-region fires")

	kafkaRow := findRow(t, plan.RedFlags, RedFlagIDKafkaVersionBelowCLFloor)
	assert.Equal(t, types.RedFlagNotTriggered, kafkaRow.Status, "Serverless-only multi-region must not trip the version row")
}

// Mixed fleet variant for Express + Tiered: a single Provisioned
// cluster hits both rows; a Serverless cluster in the same fleet
// must NOT show up in either row's evidence. Regression guard for
// future helper changes that might re-introduce silent Serverless
// fall-through into these two Red Flag detectors.
func TestRedFlags_ServerlessDoesntPolluteExpressOrTieredEvidence(t *testing.T) {
	express := redFlagCluster("prov-express", "3.5.0", "express.m7g.large", "TIERED")
	srv := serverlessCluster("serverless-mixed")
	plan := buildPlanForRedFlags(t, wrapClusters(express, srv), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)

	exp := findRow(t, plan.RedFlags, RedFlagIDMSKExpressBrokerTier)
	assert.Equal(t, types.RedFlagTriggered, exp.Status)
	assert.Contains(t, exp.Evidence, "prov-express=express.m7g.large")
	assert.NotContains(t, exp.Evidence, "serverless-mixed")

	tier := findRow(t, plan.RedFlags, RedFlagIDTieredStorageInUse)
	assert.Equal(t, types.RedFlagTriggered, tier.Status)
	assert.Contains(t, tier.Evidence, "prov-express")
	assert.NotContains(t, tier.Evidence, "serverless-mixed")
}

// Serverless+IAM cluster in the fleet means the Zero-ACLs-with-IAM
// row can't actually evaluate (Serverless ACLs aren't exposed via the
// kcp scan path). Verdict must be Unknown, not Not Triggered.
func TestRedFlags_ServerlessIAMMakesZeroACLsRowUnknown(t *testing.T) {
	srv := serverlessCluster("srv-iam")
	plan := buildPlanForRedFlags(t, wrapClusters(srv), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	row := findRow(t, plan.RedFlags, RedFlagIDZeroACLsWithIAM)
	assert.Equal(t, types.RedFlagUnknown, row.Status, "Serverless+IAM clusters make the ACL row inconclusive — must be Unknown not Not Triggered")
	assert.Contains(t, row.Evidence, "srv-iam")
}

// Mixed-fleet variant for the ACL row: a Provisioned IAM cluster with
// 0 ACLs SHOULD still fire Triggered, but the evidence must mention
// that any Serverless+IAM clusters were excluded from the count.
func TestRedFlags_ZeroACLsWithIAM_MixedFleetSurfacesExclusion(t *testing.T) {
	provIAM := redFlagCluster("prov-iam", "3.5.0", "", "")
	// Wire IAM on the Provisioned cluster + zero ACLs (scan ran, returned empty).
	enabled := true
	provIAM.AWSClientInformation.MskClusterConfig.Provisioned.ClientAuthentication = &kafkatypes.ClientAuthentication{
		Sasl: &kafkatypes.Sasl{Iam: &kafkatypes.Iam{Enabled: &enabled}},
	}
	provIAM.KafkaAdminClientInformation.Acls = []types.Acls{}
	srv := serverlessCluster("srv-iam")
	plan := buildPlanForRedFlags(t, wrapClusters(provIAM, srv), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	row := findRow(t, plan.RedFlags, RedFlagIDZeroACLsWithIAM)
	assert.Equal(t, types.RedFlagTriggered, row.Status, "Provisioned IAM + 0 ACLs still triggers the row")
	assert.Contains(t, row.Evidence, "prov-iam")
	assert.Contains(t, row.Evidence, "srv-iam", "Serverless+IAM exclusion must be surfaced in the row evidence")
}

// Serverless cluster with no Serverless.ClientAuthentication block
// fires `auth_posture_unknown` with Serverless-flavored body/how-to-close.
// Pre-fix, the OQ was suppressed entirely for Serverless, leaving §4's
// "see Actions Needed" pointer dangling.
func TestRedFlags_ServerlessNoAuthFiresAuthPostureOQ(t *testing.T) {
	c := types.ProcessedCluster{Name: "srv-noauth", Region: "us-east-1"}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
	c.AWSClientInformation.MskClusterConfig.Serverless = &kafkatypes.Serverless{
		VpcConfigs: []kafkatypes.VpcConfig{{SubnetIds: []string{"s"}, SecurityGroupIds: []string{"g"}}},
	}
	c.KafkaAdminClientInformation.Topics = &types.Topics{Details: []types.TopicDetails{{Name: "t"}}}
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	var authOQ *types.OpenQuestion
	for i, oq := range plan.OpenQuestions {
		if oq.ID == "auth_posture_unknown" && oq.ClusterID == "srv-noauth" {
			authOQ = &plan.OpenQuestions[i]
			break
		}
	}
	require.NotNil(t, authOQ, "Serverless with no ClientAuthentication must surface auth_posture_unknown")
	assert.Contains(t, authOQ.Body, "Serverless", "OQ body must call out the Serverless-specific cause")
}

// Provisioned IAM cluster with NIL ACLs (--skip-acls / scan didn't run)
// must NOT silently fall under "Not triggered" — row should be Unknown
// and the evidence should name the excluded cluster.
func TestRedFlags_ZeroACLs_SkipACLsCaseSurfacesAsUnknown(t *testing.T) {
	c := redFlagCluster("prov-iam-skipped", "3.5.0", "", "")
	enabled := true
	c.AWSClientInformation.MskClusterConfig.Provisioned.ClientAuthentication = &kafkatypes.ClientAuthentication{
		Sasl: &kafkatypes.Sasl{Iam: &kafkatypes.Iam{Enabled: &enabled}},
	}
	// Acls left nil — simulates --skip-acls / scan-didn't-run case.
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	row := findRow(t, plan.RedFlags, RedFlagIDZeroACLsWithIAM)
	assert.Equal(t, types.RedFlagUnknown, row.Status, "IAM cluster with nil ACLs makes the row Unknown")
	assert.Contains(t, row.Evidence, "prov-iam-skipped")
}

// Row 16 fires Triggered when §Cost Reconciliation surfaces an
// instance type AWS billed for but `kcp discover` didn't enumerate.
// Boolean signal — the §Cost Reconciliation section carries the per-
// candidate detail. Same pattern as row 12 (Tiered Storage) + the
// §Tiered Storage section.
func TestRedFlags_CostInventoryHidden_FiresOnUndiscoveredInstanceType(t *testing.T) {
	discovered := redFlagCluster("known-cluster", "3.5.0", "kafka.m5.large", "")
	state := wrapClusters(discovered)
	// Cost line for an instance type NOT in inventory.
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m7g.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 250.00}},
		},
	}
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	row := findRow(t, plan.RedFlags, RedFlagIDCostInventoryHidden)
	assert.Equal(t, types.RedFlagTriggered, row.Status)
	assert.Contains(t, row.Evidence, "kafka.m7g.large")
	assert.Contains(t, row.Evidence, "us-east-1")
}

// No cost data in the state file → row stays Unknown (the
// cost_data_not_collected OQ already nudges the customer to run
// `kcp report costs`). Don't misleadingly claim NotTriggered.
func TestRedFlags_CostInventoryHidden_UnknownWhenNoCostData(t *testing.T) {
	c := redFlagCluster("c", "3.5.0", "kafka.m5.large", "")
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	row := findRow(t, plan.RedFlags, RedFlagIDCostInventoryHidden)
	assert.Equal(t, types.RedFlagUnknown, row.Status)
}

// Cost data populated AND every billed instance type maps to a
// discovered cluster → NotTriggered (clean diff).
func TestRedFlags_CostInventoryHidden_NotTriggeredOnCleanDiff(t *testing.T) {
	c := redFlagCluster("known", "3.5.0", "kafka.m5.large", "")
	state := wrapClusters(c)
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-Kafka.m5.large", Values: types.ProcessedCostBreakdown{UnblendedCost: 100.00}},
		},
	}
	plan := buildPlanForRedFlags(t, state, defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	row := findRow(t, plan.RedFlags, RedFlagIDCostInventoryHidden)
	assert.Equal(t, types.RedFlagNotTriggered, row.Status)
}

// Discovered Serverless cluster, but NO Serverless-Hours cost line:
// the inventory still registers the Serverless cluster, but cost
// reconciliation just doesn't include the Serverless row in its diff
// — the section should either omit or render empty without flagging.
func TestDetectCostReconciliation_ServerlessClusterWithoutMatchingCostLine(t *testing.T) {
	srv := serverlessCluster("srv-no-billing")
	state := wrapClusters(srv)
	state.Sources[0].MSKData.Regions[0].Costs = types.ProcessedRegionCosts{
		Region: "us-east-1",
		Results: []types.ProcessedCost{
			{Start: "2026-04-01", UsageType: "USE1-DataTransfer-Out-Bytes", Values: types.ProcessedCostBreakdown{UnblendedCost: 12.50}},
		},
	}
	section := detectCostReconciliation(state, defaultCfg(t))
	assert.Nil(t, section, "no broker-shaped cost lines → section omitted; Serverless cluster shouldn't surface as a hidden candidate")
}

// Serverless cluster that ALSO has an admin-probe sasl_mechanism set
// (e.g. the admin client connected via SCRAM bridging). The Serverless
// SASL/IAM detection on the MskClusterConfig path is the source of
// truth — it should NOT fall through to the admin-probe fallback
// (which would only fire if AWS-side detection returned empty).
func TestRedFlags_ServerlessAdminProbeFallback(t *testing.T) {
	c := serverlessCluster("srv-iam-real")
	// Even with a probed mechanism set, the AWS Serverless IAM takes precedence.
	c.KafkaAdminClientInformation.SaslMechanism = "AWS_MSK_IAM"
	plan := buildPlanForRedFlags(t, wrapClusters(c), defaultCfg(t), defaultInputs())
	require.NotNil(t, plan.RedFlags)
	iamRow := findRow(t, plan.RedFlags, RedFlagIDIAMAuthEnabled)
	assert.Equal(t, types.RedFlagTriggered, iamRow.Status)
	require.Len(t, plan.Auth, 1)
	assert.Equal(t, []string{SourceAuthIAM}, plan.Auth[0].SourceAuths)
}
