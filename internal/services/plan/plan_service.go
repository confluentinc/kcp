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
	}
	plan.SourceEnvironment.TotalRegions = countRegions(state)
	plan.SizingAppendix = buildSizingAppendix(plan.Sizing, s.cfg, inputs)

	return plan, nil
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
