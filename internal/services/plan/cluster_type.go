package plan

import (
	"fmt"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

// HardLimit is one row of the Enterprise → Dedicated escalation catalog.
// Check is a Go function value (refactor-safe; compile-checked) that
// compares the relevant Plan field against the cap in plan-config.yaml's
// `enterprise_caps`, or evaluates a customer-declared flag. Returning
// true → Dedicated.
//
// Evidence carries the concrete numbers / values that fired the rule so
// the rendered plan can show "sized 14 eCKU > PNI cap 32" rather than
// an opaque "rule fired".
//
// CustomerDeclared marks rules whose only signal is a `plan-inputs.yaml`
// flag. Renderer surfaces a ⚠ cost callout on these because a wrong
// `true` flips the verdict from Enterprise to Dedicated (5–10× monthly).
type HardLimit struct {
	ID               string
	Description      string
	CustomerDeclared bool
	Check            func(cfg *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, sizing types.ClusterSizing) (fired bool, evidence string)
}

// HardLimitCatalog enumerates the rules that escalate a cluster from
// Enterprise to Dedicated.
//
//  1. sized eCKU exceeds Enterprise PNI cap (state-derived)
//  2. ACL count exceeds Enterprise cap (state-derived)
//  3. broker-side schema validation required (customer-declared)
//  4. high-throughput REST Produce v3 required (customer-declared)
//  5. 99.95% SLA within a single zone required (customer-declared)
//  6. mTLS source auth AND target_cloud != aws (state + plan-input;
//     mTLS is supported on AWS Enterprise + AWS Freight + Dedicated
//     all clouds — on any non-AWS target only Dedicated has mTLS).
var HardLimitCatalog = []HardLimit{
	{
		ID:          "eCKU_exceeds_pni_cap",
		Description: "Sized eCKU exceeds Enterprise PNI cap",
		Check: func(cfg *PlanConfig, _ types.PlanInputsResolved, _ types.ProcessedCluster, sizing types.ClusterSizing) (bool, string) {
			cap := cfg.EnterpriseCaps.PNIMaxECKU
			if sizing.FinalECKU > cap {
				return true, fmt.Sprintf("sized %d eCKU > PNI cap %d eCKU", sizing.FinalECKU, cap)
			}
			return false, ""
		},
	},
	{
		ID:          "acl_count_exceeds_cap",
		Description: "ACL count exceeds Enterprise cap",
		Check: func(cfg *PlanConfig, _ types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			cap := cfg.EnterpriseCaps.ACLCountCap
			if cap <= 0 {
				return false, ""
			}
			acls := cluster.KafkaAdminClientInformation.Acls
			// `null` (not populated) is distinct from `0 ACLs` — skip when
			// admin scan didn't run rather than emit a wrong verdict.
			if acls == nil {
				return false, ""
			}
			count := len(acls)
			if count > cap {
				return true, fmt.Sprintf("%d ACLs > Enterprise cap %d", count, cap)
			}
			return false, ""
		},
	},
	{
		ID:               "broker_side_schema_validation_required",
		Description:      "Broker-side schema ID validation required (customer-declared)",
		CustomerDeclared: true,
		Check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.EnforceSchemasAtTheBroker {
				return true, "plan-inputs.yaml `enforce_schemas_at_the_broker: true`"
			}
			return false, ""
		},
	},
	{
		ID:               "rest_produce_api_high_throughput",
		Description:      "High-throughput Kafka REST Produce v3 required (customer-declared)",
		CustomerDeclared: true,
		Check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.RequiresHighThroughputRESTProduceAPI {
				return true, "plan-inputs.yaml `requires_high_throughput_rest_produce_api: true`"
			}
			return false, ""
		},
	},
	{
		ID:               "sla_99_95_single_zone",
		Description:      "99.95% SLA within a single zone required (customer-declared)",
		CustomerDeclared: true,
		Check: func(_ *PlanConfig, inputs types.PlanInputsResolved, _ types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
			if inputs.Requires9995SLAWithinSingleZone {
				return true, "plan-inputs.yaml `requires_99_95_sla_within_a_single_zone: true` (Dedicated Single-Zone is the only tier with this SLA)"
			}
			return false, ""
		},
	},
	{
		ID:          "mtls_on_non_aws_target",
		Description: "Source uses mTLS AND target cloud is not AWS (only Dedicated has mTLS off AWS)",
		Check: func(_ *PlanConfig, inputs types.PlanInputsResolved, cluster types.ProcessedCluster, _ types.ClusterSizing) (bool, string) {
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
			return true, fmt.Sprintf("source uses mTLS AND target_cloud=%q (mTLS off AWS requires Dedicated)", target)
		},
	},
}

// sourceUsesMTLS reads the MSK `ClientAuthentication.Tls.Enabled` flag
// from `kcp scan clusters` output. Returns false when the config is
// unpopulated (the admin scan didn't run or the cluster is OSK).
func sourceUsesMTLS(c types.ProcessedCluster) bool {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil {
		return false
	}
	auth := prov.ClientAuthentication
	if auth == nil || auth.Tls == nil {
		return false
	}
	return tlsEnabled(auth.Tls)
}

// tlsEnabled handles the AWS SDK pointer-bool surface.
func tlsEnabled(t *kafkatypes.Tls) bool {
	if t == nil || t.Enabled == nil {
		return false
	}
	return *t.Enabled
}

// DecideClusterType returns the recommended Confluent Cloud cluster type
// for one source cluster.
//
// Standard is intentionally not a verdict here: MSK migration workloads
// benefit from Enterprise's elastic scaling and PrivateLink/PNI options
// that Standard doesn't offer. The design pins Enterprise as the default;
// hard-limit rules escalate to Dedicated when an Enterprise cap is
// exceeded or a workload constraint can't be served on Enterprise.
func DecideClusterType(c types.ProcessedCluster, sizing types.ClusterSizing, cfg *PlanConfig, inputs types.PlanInputsResolved) types.ClusterTypeDecision {
	var fired []types.HardLimitTrigger
	for _, hl := range HardLimitCatalog {
		if hl.Check == nil {
			continue
		}
		ok, evidence := hl.Check(cfg, inputs, c, sizing)
		if ok {
			fired = append(fired, types.HardLimitTrigger{
				RowID:            hl.ID,
				Description:      hl.Description,
				Evidence:         evidence,
				CustomerDeclared: hl.CustomerDeclared,
			})
		}
	}

	verdict := types.ClusterTypeEnterprise
	if len(fired) > 0 {
		verdict = types.ClusterTypeDedicated
	}
	return types.ClusterTypeDecision{ClusterID: c.Name, Verdict: verdict, Triggers: fired}
}
