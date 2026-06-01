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
	check            func(cfg *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, sizing types.ClusterSizing) ruleResult
}

// ruleResult is one rule's evaluation outcome. Either:
//   - Outcome=fired  + Evidence (concrete values that fired it)
//   - Outcome=not_fired + Evidence (e.g. "47 ACLs <= 4000 cap")
//   - Outcome=skipped + SkipReason (e.g. "Acls == nil; ambiguous")
type ruleResult struct {
	Outcome    types.RuleOutcome
	Evidence   string
	SkipReason string
}

func fired(evidence string) ruleResult {
	return ruleResult{Outcome: types.RuleFired, Evidence: evidence}
}

func notFired(evidence string) ruleResult {
	return ruleResult{Outcome: types.RuleNotFired, Evidence: evidence}
}

func skipped(reason string) ruleResult {
	return ruleResult{Outcome: types.RuleSkipped, SkipReason: reason}
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
		check: func(cfg *PlanConfig, _ types.PlanInputsResolved, _ types.ProcessedCluster, sizing types.ClusterSizing) ruleResult {
			pniCap := cfg.EnterpriseCaps.PNIMaxECKU
			if sizing.FinalECKU > pniCap {
				return fired(fmt.Sprintf("sized %d eCKU > PNI cap %d eCKU", sizing.FinalECKU, pniCap))
			}
			return notFired(fmt.Sprintf("sized %d eCKU ≤ PNI cap %d eCKU", sizing.FinalECKU, pniCap))
		},
	},
	{
		id:          ruleACLCountExceedsCap,
		description: "ACL count exceeds Enterprise cap",
		check: func(cfg *PlanConfig, _ types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) ruleResult {
			aclCap := cfg.EnterpriseCaps.ACLCountCap
			if aclCap <= 0 {
				return skipped("acl_count_cap not configured")
			}
			if !aclScanRan(cluster) {
				if isServerless(cluster) {
					return skipped("MSK Serverless does not expose ACLs via the admin API path kcp scans; this rule is N/A for Serverless clusters")
				}
				return skipped("no ACL list in state file — scan likely didn't run or used `--skip-acls`; rule resolves on the other rules")
			}
			count := len(cluster.KafkaAdminClientInformation.Acls)
			if count > aclCap {
				return fired(fmt.Sprintf("%d ACLs > Enterprise cap %d", count, aclCap))
			}
			return notFired(fmt.Sprintf("%d ACLs ≤ Enterprise cap %d", count, aclCap))
		},
	},
	{
		id:               ruleBrokerSideSchemaValidation,
		description:      "Broker-side schema ID validation required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) ruleResult {
			if inputs.EnforceSchemasAtTheBroker {
				return fired("`enforce_schemas_at_the_broker: true`")
			}
			return notFired("`enforce_schemas_at_the_broker: false`")
		},
	},
	{
		id:               ruleRESTProduceHighThroughput,
		description:      "High-throughput Kafka REST Produce v3 required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) ruleResult {
			if inputs.RequiresHighThroughputRESTProduceAPI {
				return fired("`requires_high_throughput_rest_produce_api: true`")
			}
			return notFired("`requires_high_throughput_rest_produce_api: false`")
		},
	},
	{
		id:               ruleSLA9995SingleZone,
		description:      "99.95% single-zone SLA required",
		customerDeclared: true,
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) ruleResult {
			if inputs.Requires9995SLAWithinSingleZone {
				return fired("`requires_99_95_sla_within_a_single_zone: true`")
			}
			return notFired("`requires_99_95_sla_within_a_single_zone: false`")
		},
	},
	{
		id:          ruleMTLSOnNonAWSTarget,
		description: "Source uses mTLS, target is non-AWS",
		check: func(_ *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) ruleResult {
			usesMTLS := sourceUsesMTLS(cluster)
			target := targetCloud(inputs)
			// Punctuation kept identical across branches so the rendered
			// evidence line reads consistently regardless of which combo fired.
			switch {
			case !usesMTLS:
				return notFired(fmt.Sprintf("no mTLS source + target_cloud=%q", target))
			case target == defaultTargetCloud:
				return notFired(fmt.Sprintf("mTLS source + target_cloud=%q (AWS supports mTLS on Enterprise/Freight)", target))
			default:
				return fired(fmt.Sprintf("mTLS source + target_cloud=%q (mTLS on non-AWS requires Dedicated)", target))
			}
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
	var firedTriggers []types.HardLimitTrigger
	evaluated := make([]types.RuleEvaluation, 0, len(hardLimitCatalog))
	for _, hl := range hardLimitCatalog {
		if hl.check == nil {
			continue
		}
		res := hl.check(cfg, inputs, c, sizing)
		evaluated = append(evaluated, types.RuleEvaluation{
			RowID:       hl.id,
			Description: hl.description,
			Outcome:     res.Outcome,
			Evidence:    res.Evidence,
			SkipReason:  res.SkipReason,
		})
		if res.Outcome == types.RuleFired {
			firedTriggers = append(firedTriggers, types.HardLimitTrigger{
				RowID:            hl.id,
				Description:      hl.description,
				Evidence:         res.Evidence,
				CustomerDeclared: hl.customerDeclared,
			})
		}
	}

	verdict := types.ClusterTypeEnterprise
	topology := types.TopologyNotApplicable
	var finalCKU *int
	if len(firedTriggers) > 0 {
		verdict = types.ClusterTypeDedicated
		// Single-Zone wins when the 99.95% SLA rule fires; otherwise
		// Multi-Zone is the Dedicated default.
		topology = types.TopologyMultiZone
		for _, t := range firedTriggers {
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
		ClusterID:      c.Name,
		EvaluatedRules: evaluated,
		Verdict:        verdict,
		Triggers:       firedTriggers,
		Topology:       topology,
		FinalCKU:       finalCKU,
	}
}
