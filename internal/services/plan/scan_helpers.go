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

// collectClusters flattens every scanned cluster out of the processed state, from
// both source kinds: MSK clusters are taken as-is; each OSK (Apache Kafka /
// Confluent Platform) cluster is mapped into the shared ProcessedCluster shape
// (oskToProcessedCluster) and tagged SourceType "osk", so the whole engine path
// consumes one cluster type. MSK clusters are untagged (SourceType ""), preserving
// every MSK code path unchanged.
func collectClusters(state report.ProcessedState) []report.ProcessedCluster {
	var out []report.ProcessedCluster
	for _, src := range state.Sources {
		switch {
		case src.MSKData != nil:
			for _, region := range src.MSKData.Regions {
				out = append(out, region.Clusters...)
			}
		case src.OSKData != nil:
			for _, oc := range src.OSKData.Clusters {
				out = append(out, oskToProcessedCluster(oc))
			}
		}
	}
	return out
}

// oskToProcessedCluster flattens an OSK (Apache Kafka / Confluent Platform) cluster
// into the shared ProcessedCluster shape the engine adapter consumes. Only the
// fields OSK actually carries are populated — the Kafka Admin info (topics, ACLs,
// connect, sasl_mechanism), the flattened metrics, and the discovered clients; the
// MSK-only AWS fields stay zero, which every MSK helper reads as "not scanned". The
// OSK cluster ID becomes the cluster name (OSK has no ARN or region), and SourceType
// is stamped "osk" so buildProfile takes the OSK path even when no source_platform
// was declared.
func oskToProcessedCluster(oc report.ProcessedOSKCluster) report.ProcessedCluster {
	c := report.ProcessedCluster{
		Name:                        oc.ID,
		SourceType:                  types.SourceTypeOSK,
		KafkaAdminClientInformation: oc.KafkaAdminClientInformation,
		DiscoveredClients:           oc.DiscoveredClients,
	}
	if oc.ClusterMetrics != nil {
		c.ClusterMetrics = *oc.ClusterMetrics
		// Mirror backfillAggregates for OSK: precompute aggregates from the raw series
		// when the collector didn't (so peak-throughput/storage reads have data).
		if len(c.ClusterMetrics.Aggregates) == 0 && len(c.ClusterMetrics.Metrics) > 0 {
			c.ClusterMetrics.Aggregates = report.CalculateMetricsAggregates(c.ClusterMetrics.Metrics)
		}
	}
	return c
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
		// OSK sources have no region; keep an OSK cluster only when no region filter is
		// active and its id matches the (empty-matches-all) cluster-id filter.
		if src.OSKData != nil {
			if region != "" {
				continue
			}
			newSrc := src
			od := *src.OSKData
			od.Clusters = nil
			for _, oc := range src.OSKData.Clusters {
				if clusterID != "" && oc.ID != clusterID {
					continue
				}
				od.Clusters = append(od.Clusters, oc)
				matched++
			}
			if len(od.Clusters) > 0 {
				newSrc.OSKData = &od
				out.Sources = append(out.Sources, newSrc)
			}
			continue
		}
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

// headerSource labels the plan header by the source platform(s) the scan carries.
// MSK-only stays exactly "Amazon MSK" (and so does the scanless synthetic MSK
// source), keeping existing output unchanged; an OSK scan reads "Apache Kafka", and
// a mixed scan names both. This is the scanned source kind — a Confluent Platform
// source scans as OSK, and the per-cluster source_platform answer, not this fleet
// label, is what distinguishes CP from Apache Kafka in each cluster's plan.
func headerSource(state report.ProcessedState) string {
	hasMSK, hasOSK := false, false
	for _, src := range state.Sources {
		if src.MSKData != nil {
			hasMSK = true
		}
		if src.OSKData != nil && len(src.OSKData.Clusters) > 0 {
			hasOSK = true
		}
	}
	switch {
	case hasMSK && hasOSK:
		return "Amazon MSK, Apache Kafka"
	case hasOSK:
		return "Apache Kafka"
	default:
		return "Amazon MSK"
	}
}

// countRegions counts the distinct regions present in the state.
func countRegions(state report.ProcessedState) int {
	regions := map[string]struct{}{}
	for _, src := range state.Sources {
		if src.MSKData != nil {
			for _, r := range src.MSKData.Regions {
				// The scanless placeholder carries an unnamed region; a real MSK
				// region always has a name, so unnamed regions don't count.
				if r.Name == "" {
					continue
				}
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
