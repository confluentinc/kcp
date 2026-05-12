package plan

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// HardLimit is one row of the Enterprise → Dedicated escalation catalog.
// Check is a Go function value (refactor-safe; compile-checked) that
// compares the relevant Plan field against the cap in plan-config.yaml's
// `enterprise_caps`. Returning true → Dedicated.
//
// Evidence carries the concrete numbers that fired the rule so the
// rendered plan can show "sized 14 eCKU > PNI cap 32" rather than an
// opaque "rule fired".
type HardLimit struct {
	ID          string
	Description string
	Check       func(cfg *PlanConfig, inputs types.PlanInputsResolved, sizing types.ClusterSizing) (fired bool, evidence string)
}

// HardLimitCatalog enumerates the rules that escalate a cluster from
// Enterprise to Dedicated. Only rules whose inputs are state-file-derived
// today are listed — adding a rule without a real signal would let a
// YAML toggle flip the verdict without evidence, undermining the
// "deterministic from the state file" guarantee.
var HardLimitCatalog = []HardLimit{
	{
		ID:          "eCKU_exceeds_pni_cap",
		Description: "Sized eCKU exceeds Enterprise PNI cap",
		Check: func(cfg *PlanConfig, _ types.PlanInputsResolved, sizing types.ClusterSizing) (bool, string) {
			cap := cfg.EnterpriseCaps.PNIMaxECKU
			if sizing.FinalECKU > cap {
				return true, fmt.Sprintf("sized %d eCKU > PNI cap %d eCKU", sizing.FinalECKU, cap)
			}
			return false, ""
		},
	},
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
		ok, evidence := hl.Check(cfg, inputs, sizing)
		if ok {
			fired = append(fired, types.HardLimitTrigger{
				RowID:       hl.ID,
				Description: hl.Description,
				Evidence:    evidence,
			})
		}
	}

	verdict := types.ClusterTypeEnterprise
	if len(fired) > 0 {
		verdict = types.ClusterTypeDedicated
	}
	return types.ClusterTypeDecision{ClusterID: c.Name, Verdict: verdict, Triggers: fired}
}
