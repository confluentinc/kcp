package plan

import (
	"time"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// Shared scan/metric helpers used by the engine-driven plan. Relocated here when
// the legacy decision modules were removed, so the new path is self-contained.

// PlanHeader is the plan document header: provenance for the generated plan.
type PlanHeader struct {
	Source            string    `json:"source"`
	StateFilePath     string    `json:"state_file_path"`
	KCPVersion        string    `json:"kcp_version"`
	GeneratedAt       time.Time `json:"generated_at"`
	StateGeneratedAt  time.Time `json:"state_generated_at,omitempty"`
	PlanSchemaVersion string    `json:"plan_schema_version"`
}

// collectClusters flattens every scanned MSK cluster out of the processed state.
func collectClusters(state report.ProcessedState) []report.ProcessedCluster {
	var out []report.ProcessedCluster
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

// FilterState returns a processed state keeping only clusters that match the given
// cluster id (name or ARN) and/or region. An empty filter value matches
// everything: with both empty the original state is returned unchanged; otherwise
// a new state is built with fresh Sources/Regions/Clusters slices (the cluster
// values, and the data they point at, are shared with the original — this is a
// structural prune, not a deep copy). Regions and sources that end up empty are
// dropped. The second return is the number of clusters that matched.
func FilterState(state report.ProcessedState, clusterID, region string) (report.ProcessedState, int) {
	if clusterID == "" && region == "" {
		return state, len(collectClusters(state))
	}
	matched := 0
	out := state
	out.Sources = nil
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		newSrc := src
		md := *src.MSKData
		md.Regions = nil
		for _, r := range src.MSKData.Regions {
			if region != "" && r.Name != region {
				continue
			}
			kept := r
			kept.Clusters = nil
			for _, c := range r.Clusters {
				if clusterID != "" && c.Name != clusterID && c.Arn != clusterID {
					continue
				}
				if region != "" && c.Region != "" && c.Region != region {
					continue
				}
				kept.Clusters = append(kept.Clusters, c)
				matched++
			}
			if len(kept.Clusters) > 0 {
				md.Regions = append(md.Regions, kept)
			}
		}
		if len(md.Regions) > 0 {
			newSrc.MSKData = &md
			out.Sources = append(out.Sources, newSrc)
		}
	}
	return out, matched
}

// detectSchemaRegistryKind returns the scan-detected Schema Registry kind for the
// fleet: "confluent", "glue", or "" when the schema-registry scan found nothing
// (or didn't run). Confluent wins if both are present. Environment-level — the
// scan associates registries with the state, not individual clusters.
func detectSchemaRegistryKind(state report.ProcessedState) string {
	sr := state.SchemaRegistries
	if sr == nil {
		return ""
	}
	if len(sr.ConfluentSchemaRegistry) > 0 {
		return "confluent"
	}
	if len(sr.AWSGlue) > 0 {
		return "glue"
	}
	return ""
}

// oskClusterCount counts Apache Kafka (OSK) clusters present in the state, which
// the MSK-only plan does not process.
func oskClusterCount(state report.ProcessedState) int {
	n := 0
	for _, src := range state.Sources {
		if src.OSKData != nil {
			n += len(src.OSKData.Clusters)
		}
	}
	return n
}

// countRegions counts the distinct regions present in the state.
func countRegions(state report.ProcessedState) int {
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

// backfillAggregates populates each cluster's metric aggregates in place from its
// raw metric series when they weren't precomputed.
func backfillAggregates(state *report.ProcessedState) {
	for i := range state.Sources {
		if state.Sources[i].MSKData == nil {
			continue
		}
		for j := range state.Sources[i].MSKData.Regions {
			for k := range state.Sources[i].MSKData.Regions[j].Clusters {
				c := &state.Sources[i].MSKData.Regions[j].Clusters[k]
				if len(c.ClusterMetrics.Aggregates) == 0 && len(c.ClusterMetrics.Metrics) > 0 {
					c.ClusterMetrics.Aggregates = report.CalculateMetricsAggregates(c.ClusterMetrics.Metrics)
				}
			}
		}
	}
}

func topicCount(c report.ProcessedCluster) int {
	if c.KafkaAdminClientInformation.Topics == nil {
		return 0
	}
	return c.KafkaAdminClientInformation.Topics.Summary.Topics
}

func brokerCount(c report.ProcessedCluster) int {
	if n := len(c.AWSClientInformation.Nodes); n > 0 {
		return n
	}
	// Fall back to the cluster config's declared broker count when the node list
	// wasn't captured (Provisioned only; Serverless has no fixed broker count).
	if prov := c.AWSClientInformation.MskClusterConfig.Provisioned; prov != nil && prov.NumberOfBrokerNodes != nil {
		return int(*prov.NumberOfBrokerNodes)
	}
	return 0
}

// userPartitionsOf returns the source cluster's total user-topic partition count
// (the engine's sizing anchor).
func userPartitionsOf(c report.ProcessedCluster) int {
	if c.KafkaAdminClientInformation.Topics == nil {
		return 0
	}
	return c.KafkaAdminClientInformation.Topics.Summary.TotalPartitions
}

// kafkaVersionOf reads the source broker Kafka version, or "" when unavailable.
func kafkaVersionOf(c report.ProcessedCluster) string {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.CurrentBrokerSoftwareInfo == nil || prov.CurrentBrokerSoftwareInfo.KafkaVersion == nil {
		return ""
	}
	return *prov.CurrentBrokerSoftwareInfo.KafkaVersion
}

// bytesPerMBps converts a CloudWatch byte-rate to MBps.
const bytesPerMBps = 1_048_576.0

// pickPercentile reads one percentile/aggregate field from a metric's aggregates.
func pickPercentile(aggs map[string]types.MetricAggregate, label, field string) (float64, bool) {
	a, ok := aggs[label]
	if !ok {
		return 0, false
	}
	var ptr *float64
	switch field {
	case "p95":
		ptr = a.P95
	case "p99":
		ptr = a.P99
	case "max":
		ptr = a.Maximum
	case "min":
		ptr = a.Minimum
	case "avg":
		ptr = a.Average
	}
	if ptr == nil {
		return 0, false
	}
	return *ptr, true
}
