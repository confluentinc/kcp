package plan

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// hardLimit is one row of the Enterprise → Dedicated escalation catalog.
// Evidence carries the concrete values that fired the rule so the rendered
// plan can show "sized 14 eCKU > PNI cap 32" rather than "rule fired".
// CustomerDeclared marks rules whose only signal is a `plan-inputs.yaml`
// flag — renderer surfaces a ⚠ cost callout for those.
type hardLimit struct {
	id               string
	description      string
	customerDeclared bool
	check            func(cfg *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, sizing types.ClusterSizing) (fired bool, evidence string)
}

// Rule IDs — referenced by both the catalog and the topology logic.
// Kept as constants so a typo in either site is a compile error rather
// than a silently-skipped rule.
const (
	ruleECKUExceedsPNICap          = "eCKU_exceeds_pni_cap"
	ruleACLCountExceedsCap         = "acl_count_exceeds_cap"
	ruleBrokerSideSchemaValidation = "broker_side_schema_validation_required"
	ruleRESTProduceHighThroughput  = "rest_produce_api_high_throughput"
	ruleSLA9995SingleZone          = "sla_99_95_single_zone"
	ruleMTLSOnNonAWSTarget         = "mtls_on_non_aws_target"
)

// hardLimitCatalog enumerates the rules that escalate a cluster from
// Enterprise to Dedicated. Rules 1-2 are state-derived; rules 3-5 read
// a `plan-inputs.yaml` flag and are marked customerDeclared; rule 6
// combines source state with the plan-input `target_cloud`.
var hardLimitCatalog = []hardLimit{
	{
		id:          ruleECKUExceedsPNICap,
		description: "Sized eCKU exceeds Enterprise PNI cap",
		check: func(cfg *PlanConfig, _ types.PlanInputsResolved, _ types.ProcessedCluster, sizing types.ClusterSizing) (bool, string) {
			pniCap := cfg.EnterpriseCaps.PNIMaxECKU
			if sizing.FinalECKU > pniCap {
				return true, fmt.Sprintf("sized %d eCKU > PNI cap %d eCKU", sizing.FinalECKU, pniCap)
			}
			return false, ""
		},
	},
	{
		id:          ruleACLCountExceedsCap,
		description: "ACL count exceeds Enterprise cap",
		check: func(cfg *PlanConfig, _ types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			aclCap := cfg.EnterpriseCaps.ACLCountCap
			if aclCap <= 0 {
				return false, ""
			}
			acls := cluster.KafkaAdminClientInformation.Acls
			// `null` (admin scan didn't run) is distinct from `0 ACLs` — skip
			// rather than emit a wrong verdict.
			if acls == nil {
				return false, ""
			}
			if count := len(acls); count > aclCap {
				return true, fmt.Sprintf("%d ACLs > Enterprise cap %d", count, aclCap)
			}
			return false, ""
		},
	},
	{
		id:               ruleBrokerSideSchemaValidation,
		description:      "Broker-side schema ID validation required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.EnforceSchemasAtTheBroker {
				return true, "`enforce_schemas_at_the_broker: true`"
			}
			return false, ""
		},
	},
	{
		id:               ruleRESTProduceHighThroughput,
		description:      "High-throughput Kafka REST Produce v3 required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.RequiresHighThroughputRESTProduceAPI {
				return true, "`requires_high_throughput_rest_produce_api: true`"
			}
			return false, ""
		},
	},
	{
		id:               ruleSLA9995SingleZone,
		description:      "99.95% single-zone SLA required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.Requires9995SLAWithinSingleZone {
				return true, "`requires_99_95_sla_within_a_single_zone: true`"
			}
			return false, ""
		},
	},
	{
		id:          ruleMTLSOnNonAWSTarget,
		description: "Source uses mTLS, target is non-AWS",
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if !sourceUsesMTLS(cluster) {
				return false, ""
			}
			target := inputs.TargetCloud
			if target == "" {
				target = "aws"
			}
			if target == "aws" {
				return false, ""
			}
			return true, fmt.Sprintf("target_cloud=%q", target)
		},
	},
}

// sourceUsesMTLS reads `MskClusterConfig.Provisioned.ClientAuthentication.Tls.Enabled`
// from `kcp scan clusters` output. Returns false when any layer is unpopulated
// (the admin scan didn't run, or the cluster is OSK).
func sourceUsesMTLS(c types.ProcessedCluster) bool {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.ClientAuthentication == nil || prov.ClientAuthentication.Tls == nil {
		return false
	}
	tls := prov.ClientAuthentication.Tls
	return tls.Enabled != nil && *tls.Enabled
}

// DecideClusterType returns the recommended Confluent Cloud cluster type
// for one source cluster.
//
// Standard is intentionally not a verdict: MSK migration workloads
// benefit from Enterprise's elastic scaling and PrivateLink/PNI options
// that Standard doesn't offer. Enterprise is the default; hard-limit
// rules escalate to Dedicated when a cap is exceeded or a workload
// constraint can't be served on Enterprise.
func DecideClusterType(c types.ProcessedCluster, sizing types.ClusterSizing, cfg *PlanConfig, inputs types.PlanInputsResolved) types.ClusterTypeDecision {
	var fired []types.HardLimitTrigger
	for _, hl := range hardLimitCatalog {
		if hl.check == nil {
			continue
		}
		ok, evidence := hl.check(cfg, inputs, c, sizing)
		if ok {
			fired = append(fired, types.HardLimitTrigger{
				RowID:            hl.id,
				Description:      hl.description,
				Evidence:         evidence,
				CustomerDeclared: hl.customerDeclared,
			})
		}
	}

	verdict := types.ClusterTypeEnterprise
	topology := types.TopologyNotApplicable
	var finalCKU *int
	if len(fired) > 0 {
		verdict = types.ClusterTypeDedicated
		// Single-Zone wins when the 99.95% SLA rule fires; otherwise
		// Multi-Zone is the Dedicated default.
		topology = types.TopologyMultiZone
		for _, t := range fired {
			if t.RowID == ruleSLA9995SingleZone {
				topology = types.TopologySingleZone
				break
			}
		}
		// Dedicated tier is sized in CKU. Same number as eCKU, different unit.
		cku := sizing.FinalECKU
		finalCKU = &cku
	}
	return types.ClusterTypeDecision{
		ClusterID: c.Name,
		Verdict:   verdict,
		Triggers:  fired,
		Topology:  topology,
		FinalCKU:  finalCKU,
	}
}
