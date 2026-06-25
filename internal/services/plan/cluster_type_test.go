package plan

import (
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideClusterType_DefaultEnterprise(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", FinalECKU: 5}
	d := decideClusterType(report.ProcessedCluster{Name: "x"}, sizing, cfg, defaultInputs())
	assert.Equal(t, ClusterTypeEnterprise, d.Verdict)
	assert.Empty(t, d.Triggers)
}

func TestDecideClusterType_DedicatedWhenSizedOverPNICap(t *testing.T) {
	cfg := defaultCfg(t)
	// pni_max_eCKU = 32; size at 33 to fire the rule.
	sizing := ClusterSizing{ClusterID: "huge", FinalECKU: 33}
	d := decideClusterType(report.ProcessedCluster{Name: "huge"}, sizing, cfg, defaultInputs())
	assert.Equal(t, ClusterTypeDedicated, d.Verdict)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "eCKU_exceeds_pni_cap", d.Triggers[0].RowID)
	assert.Contains(t, d.Triggers[0].Evidence, "33 eCKU")
	assert.Contains(t, d.Triggers[0].Evidence, "32 eCKU")
}

// provisionedClusterWithScan returns a ProcessedCluster shaped like a
// successful `kcp scan clusters` run against a PROVISIONED MSK cluster —
// Topics populated, MskClusterConfig.ClusterType = PROVISIONED. The ACL
// slice is what the caller wants the rule to evaluate against.
// provisionedClusterWithScan builds a ProcessedCluster shaped like a
// successful `kcp scan clusters` run against a PROVISIONED MSK cluster:
// Topics populated and Acls set to the caller's slice (use `nil` to
// model "scan didn't run / --skip-acls" and `[]Acls{}` to model
// "successful scan with 0 ACLs"). Pass non-empty Acls to exercise rules
// that need real ACL counts.
func provisionedClusterWithScan(name string, acls []types.Acls) report.ProcessedCluster {
	c := report.ProcessedCluster{Name: name}
	c.KafkaAdminClientInformation.Acls = acls
	c.KafkaAdminClientInformation.Topics = &types.Topics{}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
	return c
}

func TestDecideClusterType_DedicatedWhenACLsOverCap(t *testing.T) {
	cfg := defaultCfg(t)
	// acl_count_cap = 4000; emit 4001 ACLs against a successful scan.
	c := provisionedClusterWithScan("many-acls", make([]types.Acls, 4001))
	sizing := ClusterSizing{ClusterID: "many-acls", FinalECKU: 5}

	d := decideClusterType(c, sizing, cfg, defaultInputs())
	assert.Equal(t, ClusterTypeDedicated, d.Verdict)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "acl_count_exceeds_cap", d.Triggers[0].RowID)
	assert.Contains(t, d.Triggers[0].Evidence, "4001")
	assert.False(t, d.Triggers[0].CustomerDeclared, "state-derived rules must not carry the cost-callout marker")
}

// Three nil-vs-empty cases for rule 2:
//
//  1. ACL list is non-nil (caller passes `[]Acls{}` or N entries) → rule
//     evaluates against the count. Tests the helper logic; in practice
//     the current scanner produces `[]` only when at least one ACL was
//     appended, but the helper must still behave correctly if a non-nil
//     empty slice ever lands in state.
//  2. Acls == nil (either scan didn't run, `--skip-acls` was passed, or
//     a successful scan returned 0 ACLs — all three indistinguishable
//     from the current state schema) → rule skipped.
//  3. Acls == nil on SERVERLESS → ACLs aren't exposed via this API; rule
//     skipped (don't false-positive on serverless deployments).
func TestDecideClusterType_ACLRuleNilVsEmpty(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", FinalECKU: 5}

	t.Run("non-nil ACL slice evaluates the rule (under cap)", func(t *testing.T) {
		c := provisionedClusterWithScan("provisioned-empty-acls", []types.Acls{})
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict, "0 ACLs is under the 4000 cap — rule should evaluate, not fire")
	})

	t.Run("nil ACLs (scan didn't run or --skip-acls or successful 0-ACL scan) — rule skipped", func(t *testing.T) {
		// Current scanner persists Acls=nil for all three states;
		// without an explicit "scanned" flag we take the conservative
		// position that nil means "uncertain" and skip the rule.
		c := provisionedClusterWithScan("provisioned-nil-acls", nil)
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict, "nil ACLs must skip rule 2 (count is unknown), not fire it")
	})

	t.Run("SERVERLESS cluster — rule skipped regardless of ACL slice", func(t *testing.T) {
		c := report.ProcessedCluster{Name: "serverless"}
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict, "SERVERLESS doesn't expose ACLs via this API — rule should skip")
	})
}

func TestDecideClusterType_CustomerDeclaredFlags(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "small", FinalECKU: 5}
	c := report.ProcessedCluster{Name: "small"}

	cases := []struct {
		name        string
		mutator     func(*PlanInputsResolved)
		ruleID      string
		evidenceHas string // substring the evidence must name (the flag, not just its value)
	}{
		{
			name:        "broker-side schema validation forces Dedicated",
			mutator:     func(in *PlanInputsResolved) { in.EnforceSchemasAtTheBroker = true },
			ruleID:      ruleBrokerSideSchemaValidation,
			evidenceHas: "enforce_schemas_at_the_broker",
		},
		{
			name:        "REST Produce v3 high-throughput forces Dedicated",
			mutator:     func(in *PlanInputsResolved) { in.RequiresHighThroughputRESTProduceAPI = true },
			ruleID:      ruleRESTProduceHighThroughput,
			evidenceHas: "requires_high_throughput_rest_produce_api",
		},
		{
			name:        "99.95 single-zone SLA forces Dedicated",
			mutator:     func(in *PlanInputsResolved) { in.Requires9995SLAWithinSingleZone = true },
			ruleID:      ruleSLA9995SingleZone,
			evidenceHas: "requires_99_95_sla_within_a_single_zone",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := defaultInputs()
			tc.mutator(&in)
			d := decideClusterType(c, sizing, cfg, in)
			assert.Equal(t, ClusterTypeDedicated, d.Verdict)
			assert.Len(t, d.Triggers, 1)
			assert.Equal(t, tc.ruleID, d.Triggers[0].RowID)
			assert.True(t, d.Triggers[0].CustomerDeclared, "customer-declared rules must carry the cost-callout marker")
			assert.Contains(t, d.Triggers[0].Evidence, tc.evidenceHas, "evidence must name the flag the customer flipped")
		})
	}
}

func TestDecideClusterType_MTLSOnNonAWSTarget(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "mtls", FinalECKU: 5}
	enabled := true
	c := report.ProcessedCluster{Name: "mtls"}
	c.AWSClientInformation.MskClusterConfig.Provisioned = &kafkatypes.Provisioned{
		ClientAuthentication: &kafkatypes.ClientAuthentication{
			Tls: &kafkatypes.Tls{Enabled: &enabled},
		},
	}

	t.Run("aws target leaves Enterprise", func(t *testing.T) {
		in := defaultInputs()
		in.TargetCloud = "aws"
		d := decideClusterType(c, sizing, cfg, in)
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict)
	})

	t.Run("azure target forces Dedicated", func(t *testing.T) {
		in := defaultInputs()
		in.TargetCloud = "azure"
		d := decideClusterType(c, sizing, cfg, in)
		assert.Equal(t, ClusterTypeDedicated, d.Verdict)
		assert.Len(t, d.Triggers, 1)
		assert.Equal(t, "mtls_on_non_aws_target", d.Triggers[0].RowID)
		assert.False(t, d.Triggers[0].CustomerDeclared, "mTLS rule is state-derived, not a wrong-click risk")
	})

	t.Run("no mTLS source — rule skipped regardless of target", func(t *testing.T) {
		c2 := report.ProcessedCluster{Name: "scram"}
		in := defaultInputs()
		in.TargetCloud = "gcp"
		d := decideClusterType(c2, sizing, cfg, in)
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict)
	})
}

func TestDecideClusterType_Topology(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", FinalECKU: 4}
	c := report.ProcessedCluster{Name: "x"}

	t.Run("Enterprise verdict has no topology", func(t *testing.T) {
		d := decideClusterType(c, sizing, cfg, defaultInputs())
		assert.Equal(t, ClusterTypeEnterprise, d.Verdict)
		assert.Equal(t, TopologyNotApplicable, d.Topology)
		assert.Nil(t, d.FinalCKU, "Enterprise clusters are sized in eCKU only — no FinalCKU mirror")
	})

	t.Run("Dedicated via eCKU cap → Multi-Zone", func(t *testing.T) {
		big := ClusterSizing{ClusterID: "big", FinalECKU: 33}
		d := decideClusterType(c, big, cfg, defaultInputs())
		assert.Equal(t, ClusterTypeDedicated, d.Verdict)
		assert.Equal(t, TopologyMultiZone, d.Topology)
		require.NotNil(t, d.FinalCKU)
		assert.Equal(t, 33, *d.FinalCKU, "FinalCKU mirrors the sizing's FinalECKU value")
	})

	t.Run("Dedicated via 99.95 single-zone SLA → Single-Zone", func(t *testing.T) {
		in := defaultInputs()
		in.Requires9995SLAWithinSingleZone = true
		d := decideClusterType(c, sizing, cfg, in)
		assert.Equal(t, ClusterTypeDedicated, d.Verdict)
		assert.Equal(t, TopologySingleZone, d.Topology)
		require.NotNil(t, d.FinalCKU)
		assert.Equal(t, 4, *d.FinalCKU)
	})

	t.Run("rule 5 wins topology when combined with other rules", func(t *testing.T) {
		// 99.95 SLA flag + ACL-cap exceeded → both rules fire; topology
		// must escalate to Single-Zone (more restrictive).
		c2 := provisionedClusterWithScan("combo", make([]types.Acls, 4001))
		in := defaultInputs()
		in.Requires9995SLAWithinSingleZone = true
		d := decideClusterType(c2, sizing, cfg, in)
		assert.Equal(t, ClusterTypeDedicated, d.Verdict)
		assert.Equal(t, TopologySingleZone, d.Topology)
		assert.GreaterOrEqual(t, len(d.Triggers), 2, "both rules should have fired")
	})
}

func TestHardLimitCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, hl := range hardLimitCatalog {
		assert.NotEmpty(t, hl.id)
		assert.NotEmpty(t, hl.description)
		assert.NotNil(t, hl.check, "all listed rules must be wired (no nil check until the input is real)")
		assert.False(t, seen[hl.id], "duplicate id %q", hl.id)
		seen[hl.id] = true
	}
}
