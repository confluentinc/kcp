package plan

import (
	"fmt"
	"sort"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// PlanService orchestrates the deterministic plan-generation pipeline.
// MVP scope: source-environment summary, sizing, cluster-type, networking.
// Auth approach, switchover, red flags, etc. land in follow-up PRs.
type PlanService struct {
	cfg *PlanConfig
	now func() time.Time
}

// NewPlanService wires a PlanConfig into a PlanService. now is overridable
// for deterministic tests; pass nil for time.Now.
func NewPlanService(cfg *PlanConfig, now func() time.Time) *PlanService {
	if now == nil {
		now = time.Now
	}
	return &PlanService{cfg: cfg, now: now}
}

// Build produces a Plan from a ProcessedState and resolved plan-inputs.
// Each step is a pure function so the test surface is the orchestration,
// not its parts.
func (s *PlanService) Build(state types.ProcessedState, inputs types.PlanInputsResolved, stateFilePath string) (*types.Plan, error) {
	clusters := collectClusters(state)
	// Stable sort by (Region, Name, Arn) so two clusters that share a name
	// across regions still get a deterministic order.
	sort.SliceStable(clusters, func(i, j int) bool {
		if clusters[i].Region != clusters[j].Region {
			return clusters[i].Region < clusters[j].Region
		}
		if clusters[i].Name != clusters[j].Name {
			return clusters[i].Name < clusters[j].Name
		}
		return clusters[i].Arn < clusters[j].Arn
	})

	plan := &types.Plan{
		Header: types.PlanHeader{
			Source:            "Amazon MSK",
			StateFilePath:     stateFilePath,
			KCPVersion:        build_info.Version,
			GeneratedAt:       s.now().UTC(),
			PlanSchemaVersion: "1-experimental",
		},
		Inputs: inputs,
	}

	for _, c := range clusters {
		// ProcessState doesn't populate Aggregates on the top-level path —
		// only the per-subcommand date-filtered helpers do. Fill it in if
		// the cluster has raw metrics but no aggregates, so sizing has P95.
		if len(c.ClusterMetrics.Aggregates) == 0 && len(c.ClusterMetrics.Metrics) > 0 {
			c.ClusterMetrics.Aggregates = report.CalculateMetricsAggregates(c.ClusterMetrics.Metrics)
		}
		sizing := ComputeClusterSizing(c, s.cfg, inputs)
		ct := DecideClusterType(c, sizing, s.cfg, inputs)
		net := DecideNetworking(sizing, ct, s.cfg, inputs)

		plan.Sizing = append(plan.Sizing, sizing)
		plan.ClusterTypeDecision = append(plan.ClusterTypeDecision, ct)
		plan.NetworkingDecision = append(plan.NetworkingDecision, net)
		plan.SourceEnvironment.Clusters = append(plan.SourceEnvironment.Clusters, types.SourceClusterSummary{
			ClusterID:   c.Name,
			Region:      c.Region,
			TopicCount:  topicCount(c),
			BrokerCount: brokerCount(c),
		})
		plan.OpenQuestions = append(plan.OpenQuestions, detectOpenQuestions(c, sizing)...)
	}
	plan.SourceEnvironment.TotalRegions = countRegions(state)
	plan.SizingAppendix = buildSizingAppendix(plan.Sizing, s.cfg, inputs)

	// Stable order: blocker OQs first, acknowledgement OQs last; tie-break
	// on cluster ID so two clusters of equal priority sort alphabetically.
	sort.SliceStable(plan.OpenQuestions, func(i, j int) bool {
		pi, pj := openQuestionPriority(plan.OpenQuestions[i].ID), openQuestionPriority(plan.OpenQuestions[j].ID)
		if pi != pj {
			return pi < pj
		}
		return plan.OpenQuestions[i].ClusterID < plan.OpenQuestions[j].ClusterID
	})

	return plan, nil
}

// detectOpenQuestions surfaces state-file gaps and inferred-signal quirks
// per cluster. The Plan still ships a recommendation in each case (the
// SLA floor for degraded sizing, the verdict from the other rules when
// Rule 2 is skipped, etc.) — the OQ tells the customer what action will
// upgrade that recommendation.
func detectOpenQuestions(c types.ProcessedCluster, sizing types.ClusterSizing) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if sizing.Degraded {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "missing_p95_metrics",
			ClusterID:  c.Name,
			Title:      "No P95 throughput metrics — sizing fell back to SLA floor",
			Body:       sizing.DegradedReason,
			HowToClose: "Re-run `kcp scan metrics` against this cluster.",
		})
	}
	if c.KafkaAdminClientInformation.Acls == nil {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "acls_not_scanned",
			ClusterID:  c.Name,
			Title:      "Admin scan didn't populate ACLs — cap-vs-Enterprise rule was skipped",
			Body:       "The `acl_count_exceeds_cap` hard-limit rule needs the ACL list to evaluate. With `acls == null` the rule is treated as inconclusive; the verdict resolves on the other rules.",
			HowToClose: "Re-run `kcp scan clusters` with admin credentials to populate the ACL list.",
		})
	}
	if brokerCount(c) == 0 {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "broker_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 brokers — likely an incomplete scan",
			Body:       "`AWSClientInformation.Nodes` is empty for this cluster. The Source Environment table reads as `Brokers: 0`, which is almost certainly wrong for a real MSK cluster.",
			HowToClose: "Re-run `kcp discover` against the source account / region to populate the broker inventory.",
		})
	}
	if topicCount(c) == 0 {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "topic_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 topics — likely an incomplete scan",
			Body:       "`KafkaAdminClientInformation.Topics.Summary.Topics` is 0. The Source Environment table reads as `Topics: 0`, which is almost certainly wrong for a real MSK cluster (system topics alone usually push the count above zero).",
			HowToClose: "Re-run `kcp scan clusters` with admin credentials to populate the topic list.",
		})
	}
	// Spiky workload is an informational signal, not an Open Question:
	// the P95-based sizing + Enterprise elasticity absorbs occasional
	// spikes, so there's nothing the customer MUST do. The rationale
	// renderer surfaces a one-line note inline so the customer still
	// sees the signal and knows the override path (`sizing_percentile: p99`)
	// exists if they want tighter sizing.
	return oqs
}

// Priority order for OQs. Lower number renders first. Constants here so
// every OQ has an explicit rank — a new ID without a priority entry
// falls through to oqPriorityUnknown and renders last.
const (
	oqPriorityMissingMetrics  = iota // missing P95 → sizing fell back to floor
	oqPriorityBrokerInventory        // broker count == 0 (scan gap)
	oqPriorityTopicInventory         // topic count == 0 (scan gap)
	oqPriorityACLsNotScanned         // Acls == nil (admin-cred gap)
	oqPriorityUnknown                // fallback for new IDs added without a priority entry
)

func openQuestionPriority(id string) int {
	switch id {
	case "missing_p95_metrics":
		return oqPriorityMissingMetrics
	case "broker_inventory_empty":
		return oqPriorityBrokerInventory
	case "topic_inventory_empty":
		return oqPriorityTopicInventory
	case "acls_not_scanned":
		return oqPriorityACLsNotScanned
	default:
		return oqPriorityUnknown
	}
}

func collectClusters(state types.ProcessedState) []types.ProcessedCluster {
	var out []types.ProcessedCluster
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		for _, region := range src.MSKData.Regions {
			out = append(out, region.Clusters...)
		}
	}
	return out
}

func countRegions(state types.ProcessedState) int {
	regions := map[string]struct{}{}
	for _, src := range state.Sources {
		if src.MSKData != nil {
			for _, r := range src.MSKData.Regions {
				regions[r.Name] = struct{}{}
			}
		}
	}
	return len(regions)
}

func topicCount(c types.ProcessedCluster) int {
	if c.KafkaAdminClientInformation.Topics == nil {
		return 0
	}
	return c.KafkaAdminClientInformation.Topics.Summary.Topics
}

func brokerCount(c types.ProcessedCluster) int {
	return len(c.AWSClientInformation.Nodes)
}

func buildSizingAppendix(sizings []types.ClusterSizing, cfg *PlanConfig, inputs types.PlanInputsResolved) []types.SizingMathDetail {
	caps := cfg.EnterpriseCaps
	out := make([]types.SizingMathDetail, 0, len(sizings))
	for _, s := range sizings {
		if s.Degraded {
			continue
		}
		out = append(out, types.SizingMathDetail{
			ClusterID: s.ClusterID,
			Formula:   fmt.Sprintf("CEIL(max(P95In/%d, P95Out/%d, partitions/%d) * (1 + %.2f headroom))", caps.PerECKUIngressMBps, caps.PerECKUEgressMBps, caps.PerECKUPartitionRate, inputs.HeadroomFraction),
			IntermediateSteps: []string{
				fmt.Sprintf("ingress ratio = %.2f MBps / %d = %.4f", s.P95InMBps, caps.PerECKUIngressMBps, s.IngressRatio),
				fmt.Sprintf("egress ratio  = %.2f MBps / %d = %.4f", s.P95OutMBps, caps.PerECKUEgressMBps, s.EgressRatio),
				fmt.Sprintf("partition ratio = %d / %d = %.4f", s.UserPartitions, caps.PerECKUPartitionRate, s.PartitionRatio),
				fmt.Sprintf("max ratio = %.4f", s.MaxRatio),
				fmt.Sprintf("sized = CEIL(%.4f * %.2f) = %d  (1 + %.2f headroom)", s.MaxRatio, 1+inputs.HeadroomFraction, s.SizedECKU, inputs.HeadroomFraction),
				fmt.Sprintf("final = max(%d sized, %d SLA floor) = %d eCKU", s.SizedECKU, s.SLAFloorECKU, s.FinalECKU),
			},
			Citations: s.Citations,
		})
	}
	return out
}
