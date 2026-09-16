package plan

import (
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
)

// Observation is a scan-derived signal worth surfacing alongside the plan — a
// thing to watch ("warn") or a scoping note ("info"). These are additive to the
// engine's verdicts; they never change a recommendation.
type Observation struct {
	Severity string `json:"severity"` // "warn" | "info"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// clusterObservations computes the per-cluster signals from the scanned cluster
// and its built profile.
func clusterObservations(c report.ProcessedCluster, p engine.Profile) []Observation {
	var obs []Observation

	// Source client auth (IAM, unauthenticated) is not surfaced here: the
	// Authentication recommendation already says every client gets new Confluent
	// Cloud credentials, and the Migration infrastructure section already carries
	// the source-side handling (IAM bridging, RBAC, moving unauthenticated clients).
	// A separate note here only repeated that and confused the target method
	// (Confluent Cloud has no SASL/SCRAM for clients).

	// Tiered-storage backfill only matters when you are actually moving existing
	// data; on a start-fresh migration there is no backfill, so this would
	// contradict the historical-data verdict.
	if deref(p.StorageMode) == "Yes" && p.NeedsDataMigration != "No" {
		obs = append(obs, Observation{"info", "Tiered storage in use",
			"Historical data sits in S3. Backfilling it during cutover takes time proportional to the tiered volume."})
	}
	if len(c.ClusterMetrics.Aggregates) == 0 {
		obs = append(obs, Observation{"warn", "No throughput metrics scanned",
			"Sizing is based on the partition count alone and is a lower bound. Run `kcp scan metrics` to size from real ingress/egress before you commit: a higher measured throughput can raise the recommended cluster type, and a move to Dedicated changes the private-networking design (PNI is not available on Dedicated)."})
	}

	// Topic-name signals.
	var linkTopics, checkpointTopics, streamsTopics int
	if t := c.KafkaAdminClientInformation.Topics; t != nil {
		for _, td := range t.Details {
			n := strings.ToLower(td.Name)
			if strings.Contains(n, "confluent-link") {
				linkTopics++
			}
			if strings.Contains(n, ".checkpoints.internal") {
				checkpointTopics++
			}
			if strings.HasSuffix(n, "-changelog") || strings.HasSuffix(n, "-repartition") {
				streamsTopics++
			}
		}
	}
	if linkTopics > 0 {
		obs = append(obs, Observation{"warn", "Migration may already be in progress",
			fmt.Sprintf("Found %d Cluster-Linking topic(s) on the source. Confirm whether a migration to Confluent Cloud is already underway before planning another.", linkTopics)})
	}
	if checkpointTopics > 0 {
		obs = append(obs, Observation{"info", "MirrorMaker 2 checkpoints detected",
			fmt.Sprintf("Found %d MM2 checkpoint topic(s). If MM2 is running today, factor its teardown into the cutover.", checkpointTopics)})
	}
	if streamsTopics > 0 {
		obs = append(obs, Observation{"info", "Kafka Streams in use",
			fmt.Sprintf("Found %d Kafka Streams internal topic(s) (changelog/repartition). Streams apps carry local state. Confirm the exactly-once / Streams answer for the affected applications.", streamsTopics)})
	}
	return obs
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
