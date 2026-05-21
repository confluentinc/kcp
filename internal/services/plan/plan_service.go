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
		sizing.ScanIncomplete = scanIncomplete(c)
		ct := DecideClusterType(c, sizing, s.cfg, inputs)
		net := DecideNetworking(sizing, ct, s.cfg, inputs)

		plan.Sizing = append(plan.Sizing, sizing)
		plan.ClusterTypeDecision = append(plan.ClusterTypeDecision, ct)
		plan.NetworkingDecision = append(plan.NetworkingDecision, net)
		plan.SourceEnvironment.Clusters = append(plan.SourceEnvironment.Clusters, types.SourceClusterSummary{
			ClusterID:    c.Name,
			Region:       c.Region,
			TopicCount:   topicCount(c),
			BrokerCount:  brokerCount(c),
			IsServerless: isServerless(c),
		})
		plan.OpenQuestions = append(plan.OpenQuestions, detectOpenQuestions(c, sizing, ct, net, s.cfg, inputs)...)
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
// the ACL rule is skipped, etc.) — the OQ tells the customer what action
// will upgrade that recommendation. SERVERLESS-specific suppressions for
// "ACLs not populated" and "0 brokers" live in cluster_signals.go so the
// same logic is shared with the rule evaluator.
func detectOpenQuestions(c types.ProcessedCluster, sizing types.ClusterSizing, ct types.ClusterTypeDecision, net types.NetworkingDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if sizing.Degraded {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "missing_p95_metrics",
			ClusterID:  c.Name,
			Title:      fmt.Sprintf("No %s throughput metrics — sizing fell back to SLA floor", percentileHeader(inputs.SizingPercentile)),
			Body:       sizing.DegradedReason,
			HowToClose: fmt.Sprintf("Re-run `kcp scan metrics%s` to backfill CloudWatch metrics for the affected cluster(s).", regionFlag(c.Region)),
		})
	}
	if !aclScanRan(c) && !isServerless(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "acls_not_scanned",
			ClusterID:  c.Name,
			Title:      "Admin scan didn't populate ACLs — cap-vs-Enterprise rule was skipped",
			Body:       "The `acl_count_exceeds_cap` hard-limit rule needs a successful ACL scan to evaluate. Without one the rule is treated as inconclusive; the verdict resolves on the other rules.",
			HowToClose: fmt.Sprintf("Re-run `kcp scan clusters%s` with admin Kafka credentials (SASL/IAM or SCRAM) so the ACL list populates.", regionFlag(c.Region)),
		})
	}
	if brokerInventoryGap(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "broker_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 brokers — likely an incomplete scan",
			Body:       "`AWSClientInformation.Nodes` is empty for this cluster. The Source Environment table reads as `Brokers: 0`, which is almost certainly wrong for an MSK Provisioned cluster.",
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account to populate the broker inventory.", regionFlag(c.Region)),
		})
	}
	if topicCount(c) == 0 {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "topic_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 topics — likely an incomplete scan",
			Body:       "`KafkaAdminClientInformation.Topics.Summary.Topics` is 0. The Source Environment table reads as `Topics: 0`, which is almost certainly wrong for a real MSK cluster (system topics alone usually push the count above zero).",
			HowToClose: fmt.Sprintf("Re-run `kcp scan clusters%s` with admin Kafka credentials to populate the topic list.", regionFlag(c.Region)),
		})
	}
	if privateLinkSizingExceedsCap(sizing, ct, net, cfg) {
		cap := cfg.EnterpriseCaps.PrivateLinkMaxECKU
		oqs = append(oqs, types.OpenQuestion{
			ID:         "networking_privatelink_over_cap",
			ClusterID:  c.Name,
			Title:      fmt.Sprintf("PrivateLink trigger fired but cluster sizing exceeds the %d-eCKU PrivateLink cap on Enterprise", cap),
			Body:       fmt.Sprintf("PrivateLink networking on Enterprise is capped at %d eCKU. One or more clusters sized above that cap, and a PrivateLink trigger fired (target_cloud, cc_egress_required, or projected_pni_gateway_count), so the plan still recommends PrivateLink — an infeasible combination above the %d-eCKU cap. See the Sizing & Cluster Decisions table above for each affected cluster's final eCKU.", cap, cap),
			HowToClose: fmt.Sprintf("Raise this with your Confluent account team. Dedicated supports PrivateLink without the %d-eCKU Enterprise cap and is the most likely path to keep both the PrivateLink requirement and the throughput.", cap),
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
	oqPriorityNetworkingOverCap = iota // infeasible PrivateLink recommendation — blocker
	oqPriorityMissingMetrics           // missing P95 → sizing fell back to floor
	oqPriorityBrokerInventory          // broker count == 0 (scan gap)
	oqPriorityTopicInventory           // topic count == 0 (scan gap)
	oqPriorityACLsNotScanned           // ACL admin scan didn't run
	oqPriorityUnknown                  // fallback for new IDs added without a priority entry
)

func openQuestionPriority(id string) int {
	switch id {
	case "networking_privatelink_over_cap":
		return oqPriorityNetworkingOverCap
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

// regionFlag returns " --region <region>" when region is non-empty,
// otherwise an empty string — so HowToClose commands rendered against a
// hand-edited or partially-populated state file don't produce an invalid
// `--region ` flag with a trailing empty value.
func regionFlag(region string) string {
	if region == "" {
		return ""
	}
	return " --region " + region
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
			Formula: fmt.Sprintf("CEIL(max(%sIn/%d, %sOut/%d, partitions/%d) * (1 + %.2f headroom))",
				percentileHeader(inputs.SizingPercentile), caps.PerECKUIngressMBps,
				percentileHeader(inputs.SizingPercentile), caps.PerECKUEgressMBps,
				caps.PerECKUPartitionRate, inputs.HeadroomFraction),
			IntermediateSteps: []string{
				fmt.Sprintf("ingress ratio = %.2f MBps / %d = %.4f", s.SizedInMBps, caps.PerECKUIngressMBps, s.IngressRatio),
				fmt.Sprintf("egress ratio  = %.2f MBps / %d = %.4f", s.SizedOutMBps, caps.PerECKUEgressMBps, s.EgressRatio),
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
