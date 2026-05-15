package plan

import (
	"fmt"
	"math"

	"github.com/confluentinc/kcp/internal/types"
)

// bytesPerMBps turns CloudWatch byte-rates into MBps (1024 * 1024).
const bytesPerMBps = 1_048_576.0

// ComputeClusterSizing implements the deterministic sizing formula:
//
//	max_ratio = max(P95In/per_eCKU_ingress_mbps,
//	                P95Out/per_eCKU_egress_mbps,
//	                user_partitions/per_eCKU_partition_rate)
//	sized_eCKU = CEIL(max_ratio * (1 + headroom))
//	final_eCKU = max(sized_eCKU, SLA_floor)
//
// Peak burst eCKU is derived from instantaneous max metric values and used
// downstream to decide PrivateLink vs PNI.
//
// When throughput metrics are missing (e.g. `kcp discover` ran without
// `kcp scan metrics`), the function returns a degraded ClusterSizing with
// FinalECKU = SLA floor and Degraded = true rather than failing the whole
// plan build. The renderer surfaces the gap so the customer knows the
// sizing column is a placeholder.
func ComputeClusterSizing(c types.ProcessedCluster, cfg *PlanConfig, inputs types.PlanInputsResolved) types.ClusterSizing {
	caps := cfg.EnterpriseCaps
	aggs := c.ClusterMetrics.Aggregates

	pct := normalizePercentile(inputs.SizingPercentile)
	p95InBytes, haveIn := pickPercentile(aggs, "BytesInPerSec", pct)
	p95OutBytes, haveOut := pickPercentile(aggs, "BytesOutPerSec", pct)
	if !haveIn || !haveOut {
		slaFloor := slaFloorECKU(inputs.SLATarget, cfg.PlanInputDefaults.SLAFloorECKU)
		return types.ClusterSizing{
			ClusterID:      c.Name,
			UserPartitions: userPartitionsOf(c),
			SLAFloorECKU:   slaFloor,
			FinalECKU:      slaFloor,
			Degraded:       true,
			DegradedReason: missingMetricsReason(haveIn, haveOut, pct),
			Citations: []types.FieldCitation{
				{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesInPerSec.%s", c.Name, citationKey(pct)), Value: nil},
				{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesOutPerSec.%s", c.Name, citationKey(pct)), Value: nil},
			},
		}
	}

	peakInBytes, _ := pickPercentile(aggs, "BytesInPerSec", "max")
	peakOutBytes, _ := pickPercentile(aggs, "BytesOutPerSec", "max")

	p95InMBps := p95InBytes / bytesPerMBps
	p95OutMBps := p95OutBytes / bytesPerMBps
	peakInMBps := peakInBytes / bytesPerMBps
	peakOutMBps := peakOutBytes / bytesPerMBps

	userPartitions := userPartitionsOf(c)
	internalPartitions := 0
	if topics := c.KafkaAdminClientInformation.Topics; topics != nil {
		internalPartitions = topics.Summary.TotalInternalPartitions
	}

	ingressRatio := p95InMBps / float64(caps.PerECKUIngressMBps)
	egressRatio := p95OutMBps / float64(caps.PerECKUEgressMBps)
	partitionRatio := float64(userPartitions) / float64(caps.PerECKUPartitionRate)
	maxRatio, maxDriver := pickMaxDriver(ingressRatio, egressRatio, partitionRatio)

	sized := int(math.Ceil(maxRatio * (1.0 + inputs.HeadroomFraction)))
	if sized < 1 {
		sized = 1
	}
	slaFloor := slaFloorECKU(inputs.SLATarget, cfg.PlanInputDefaults.SLAFloorECKU)
	final := sized
	if slaFloor > final {
		final = slaFloor
	}

	peakBurstInRatio := peakInMBps / float64(caps.PerECKUIngressMBps)
	peakBurstOutRatio := peakOutMBps / float64(caps.PerECKUEgressMBps)
	peakBurstECKU := int(math.Ceil(maxOf(peakBurstInRatio, peakBurstOutRatio)))
	if peakBurstECKU < final {
		peakBurstECKU = final
	}
	peakBurstPctOfPLCap := 100.0 * float64(peakBurstECKU) / float64(caps.PrivateLinkMaxECKU)

	spikyRatio := inputs.SpikyWorkloadRatio

	return types.ClusterSizing{
		ClusterID:           c.Name,
		P95InMBps:           p95InMBps,
		P95OutMBps:          p95OutMBps,
		PeakInMBps:          peakInMBps,
		PeakOutMBps:         peakOutMBps,
		UserPartitions:      userPartitions,
		InternalPartitions:  internalPartitions,
		IngressRatio:        ingressRatio,
		EgressRatio:         egressRatio,
		PartitionRatio:      partitionRatio,
		MaxRatio:            maxRatio,
		MaxRatioDriver:      maxDriver,
		SizedECKU:           sized,
		SLAFloorECKU:        slaFloor,
		FinalECKU:           final,
		PeakBurstInRatio:    peakBurstInRatio,
		PeakBurstOutRatio:   peakBurstOutRatio,
		PeakBurstECKU:       peakBurstECKU,
		PeakBurstPctOfPLCap: peakBurstPctOfPLCap,
		SpikyIngress:        peakInMBps > spikyRatio*p95InMBps,
		SpikyEgress:         peakOutMBps > spikyRatio*p95OutMBps,
		Citations: []types.FieldCitation{
			{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesInPerSec.%s", c.Name, citationKey(pct)), Value: p95InBytes},
			{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesOutPerSec.%s", c.Name, citationKey(pct)), Value: p95OutBytes},
			{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesInPerSec.max", c.Name), Value: peakInBytes},
			{Path: fmt.Sprintf("cluster[%s].metrics.aggregates.BytesOutPerSec.max", c.Name), Value: peakOutBytes},
			{Path: fmt.Sprintf("cluster[%s].kafka_admin_client_information.topics.summary.total_partitions", c.Name), Value: userPartitions},
		},
	}
}

// normalizePercentile maps the customer's sizing_percentile input
// (p95 | p99 | max) to the canonical lowercase form. Accepts legacy
// uppercase variants (`P95`, `P99`) for back-compat with pre-spec
// inputs, but the canonical surface is lowercase to match Confluent
// dashboards. Unknown values fall back to "p95".
func normalizePercentile(s string) string {
	switch s {
	case "p95", "P95":
		return "p95"
	case "p99", "P99":
		return "p99"
	case "max":
		return "max"
	default:
		return "p95"
	}
}

// citationKey is the lowercase token the citation path uses — same as
// the percentile itself now that the input is lowercase.
func citationKey(pct string) string {
	return pct
}

// pickMaxDriver returns the largest of the three ratios and the label
// of the dimension that produced it ("ingress" | "egress" | "partitions").
// Ties resolve to the first ratio that touches the max, in the order
// ingress, egress, partitions — stable and not float-equality dependent.
func pickMaxDriver(ingress, egress, partitions float64) (float64, string) {
	maxR, driver := ingress, "ingress"
	if egress > maxR {
		maxR, driver = egress, "egress"
	}
	if partitions > maxR {
		maxR, driver = partitions, "partitions"
	}
	return maxR, driver
}

func userPartitionsOf(c types.ProcessedCluster) int {
	if c.KafkaAdminClientInformation.Topics == nil {
		return 0
	}
	return c.KafkaAdminClientInformation.Topics.Summary.TotalPartitions
}

func missingMetricsReason(haveIn, haveOut bool, pct string) string {
	switch {
	case !haveIn && !haveOut:
		return fmt.Sprintf("no BytesInPerSec or BytesOutPerSec %s; re-run kcp scan metrics", pct)
	case !haveIn:
		return fmt.Sprintf("no BytesInPerSec %s; re-run kcp scan metrics", pct)
	default:
		return fmt.Sprintf("no BytesOutPerSec %s; re-run kcp scan metrics", pct)
	}
}

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

func maxOf(vals ...float64) float64 {
	m := math.Inf(-1)
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}

func slaFloorECKU(target string, floors map[string]int) int {
	if target == "" {
		target = "99.9"
	}
	if v, ok := floors[target]; ok {
		return v
	}
	return 1
}
