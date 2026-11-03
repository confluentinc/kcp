package engine

import (
	"math"
	"strconv"
)

// PartitionsDim is the partition readout for the UI: the raw exact value if
// given, else the banded label. Band is nil when no partition answer exists.
type PartitionsDim struct {
	Display string `json:"display"`
	Band    *int   `json:"band"`
}

// SizingResult is the verdict of sizingBand: the most-binding band, which
// dimension drove it, the per-dimension bands (a dim absent from Given was not
// answered), and the flags the plan reads downstream.
type SizingResult struct {
	Band              int           // 1..bandXL, most-binding dimension wins
	Driver            string        // dim name, or "source platform" on Serverless; "" when nothing answered
	Given             map[dim]int   // dimension -> band; key absent where assumed/unanswered
	MissingPartitions bool          // no sizing signal at all — a non-committable lower bound
	PartitionsDim     PartitionsDim // partition readout for the UI
	ServerlessCapped  bool          // Serverless: band settled by the platform, not an answer
}

// dimResult is one dimension resolved to a band, with the ceiling of that band
// (for the "approaching ceiling" nudge; Inf above the top edge).
type dimResult struct {
	name    dim
	band    int
	value   *float64
	ceiling float64
}

// sizingBand picks the most-binding size band from whichever dimensions the
// caller provided — most-binding dimension wins. Accepts either raw numeric
// fields (kcp's scanned metrics / partition counts) or banded categorical
// labels. A band can be raised by any dimension and lowered by none.
func sizingBand(p Profile) SizingResult {
	// MSK Serverless cannot exceed Band 1, so the sizing question is not asked
	// and the answer is known without it (every published Serverless quota falls
	// inside Band 1). The band is settled by the platform.
	if p.isServerless() {
		one := 1
		return SizingResult{
			Band:              1,
			Driver:            "source platform",
			Given:             map[dim]int{dimPartitions: 1, dimIngress: 1, dimEgress: 1, dimRequestRate: 1},
			MissingPartitions: false,
			PartitionsDim:     PartitionsDim{Display: "Up to 2,400 (MSK Serverless ceiling)", Band: &one},
			ServerlessCapped:  true,
		}
	}

	partitionDim := fromRaw(p.PartitionsExact, bandEdges[dimPartitions], dimPartitions)
	if partitionDim == nil {
		partitionDim = fromLabel(p.PartitionBand, dimPartitions)
	}
	ingressDim := fromRaw(p.PeakIngressMbps, bandEdges[dimIngress], dimIngress)
	if ingressDim == nil {
		ingressDim = fromLabel(p.IngressBand, dimIngress)
	}
	egressDim := fromRaw(p.PeakEgressMbps, bandEdges[dimEgress], dimEgress)
	if egressDim == nil {
		egressDim = fromLabel(p.EgressBand, dimEgress)
	}
	requestDim := fromRaw(p.PeakRequestsPerSec, bandEdges[dimRequestRate], dimRequestRate)
	if requestDim == nil {
		requestDim = fromLabel(p.RequestRateBand, dimRequestRate)
	}

	given := map[dim]int{}
	if partitionDim != nil {
		given[dimPartitions] = partitionDim.band
	}
	if ingressDim != nil {
		given[dimIngress] = ingressDim.band
	}
	if egressDim != nil {
		given[dimEgress] = egressDim.band
	}
	if requestDim != nil {
		given[dimRequestRate] = requestDim.band
	}

	// A sizing gap means no sizing signal at all — a non-committable lower bound.
	missingPartitions := partitionDim == nil && ingressDim == nil

	// drivers in the fixed order partitions, ingress, egress, request rate.
	drivers := []*dimResult{}
	for _, d := range []*dimResult{partitionDim, ingressDim, egressDim, requestDim} {
		if d != nil {
			drivers = append(drivers, d)
		}
	}
	// Find the top band first, THEN the FIRST dimension that reaches it — so a
	// small workload reports partitions (asked first) as its driver, not
	// whichever dimension happened to be listed last.
	driverBand := 1
	for _, d := range drivers {
		if d.band > driverBand {
			driverBand = d.band
		}
	}
	driver := ""
	for _, d := range drivers {
		if driver == "" && d.band == driverBand {
			driver = string(d.name)
		}
	}

	partitionsDim := PartitionsDim{Display: dimDisplay(p.PartitionsExact, p.PartitionBand)}
	if partitionDim != nil {
		b := partitionDim.band
		partitionsDim.Band = &b
	}

	return SizingResult{
		Band:              driverBand,
		Driver:            driver,
		Given:             given,
		MissingPartitions: missingPartitions,
		PartitionsDim:     partitionsDim,
	}
}

// sizingAnchor is the one dimension we ask for; the rest are assumed at the same
// band and shown for review.
const sizingAnchor = dimPartitions

// sizingFieldDim maps a reviewed field name to its dimension.
var sizingFieldDim = map[string]dim{
	"partition_band": dimPartitions, "partitions_exact": dimPartitions,
	"ingress_band": dimIngress, "peak_ingress_mbps": dimIngress,
	"egress_band": dimEgress, "peak_egress_mbps": dimEgress,
	"request_rate_band": dimRequestRate, "peak_requests_per_sec": dimRequestRate,
}

// Mismatch records a reviewed dimension that came back ABOVE the anchor.
type Mismatch struct {
	Name       string
	Band       int
	AnchorBand int
}

// SizingShape reports which dimensions the customer actually stood behind (vs
// assumed), and whether any reviewed one disagrees UPWARD with the anchor.
type SizingShape struct {
	Anchor     string // "partitions" or "" when the anchor was not answered
	AnchorBand int
	Assumed    []string
	Mismatch   *Mismatch
}

// sizingShape distinguishes stated from assumed. Only fields the caller marks in
// SizingReviewed count as stated; egress can raise the band but is never a
// mismatch (its edge is a lower fan-out than a consumer-heavy workload runs).
func sizingShape(p Profile) SizingShape {
	given := sizingBand(p).Given
	anchor, anchorPresent := given[sizingAnchor]

	reviewed := map[dim]bool{}
	for _, fld := range p.SizingReviewed {
		if d, ok := sizingFieldDim[fld]; ok {
			reviewed[d] = true
		}
		// An unknown field name maps to no dimension and is harmlessly ignored.
	}
	anchorReviewed := anchorPresent && reviewed[sizingAnchor]

	var assumed []string
	var mismatch *Mismatch
	for _, d := range []dim{dimPartitions, dimIngress, dimEgress, dimRequestRate} {
		if d == sizingAnchor {
			continue
		}
		band, present := given[d]
		if d == dimEgress {
			if !present || !reviewed[d] {
				assumed = append(assumed, string(d))
			}
			continue
		}
		if !present || !reviewed[d] {
			assumed = append(assumed, string(d))
			continue
		}
		if anchorReviewed && band > anchor && (mismatch == nil || band > mismatch.Band) {
			mismatch = &Mismatch{Name: string(d), Band: band, AnchorBand: anchor}
		}
	}

	anchorName := ""
	if anchorPresent {
		anchorName = string(sizingAnchor)
	}
	return SizingShape{Anchor: anchorName, AnchorBand: anchor, Assumed: assumed, Mismatch: mismatch}
}

// fromRaw resolves a raw numeric answer to a band. Non-finite or non-positive is
// not a small cluster, it is a bad answer — return nil rather than sizing it to
// Band 1. edges = [band1Max, band2Max]; above band2Max is bandXL.
func fromRaw(value *float64, edges []int, name dim) *dimResult {
	if value == nil {
		return nil
	}
	v := *value
	if math.IsInf(v, 0) || math.IsNaN(v) || v <= 0 {
		return nil
	}
	band := 1
	for band <= len(edges) && v > float64(edges[band-1]) {
		band++
	}
	ceiling := math.Inf(1)
	if band <= len(edges) {
		ceiling = float64(edges[band-1])
	}
	return &dimResult{name: name, band: band, value: &v, ceiling: ceiling}
}

// fromLabel resolves a banded categorical answer to a band. Current labels map
// positionally; superseded labels resolve via legacyLabelBands, preserving their
// original decision.
func fromLabel(label string, d dim) *dimResult {
	if label == "" {
		return nil
	}
	band := 0
	for i, l := range bandLabels[d] {
		if l == label {
			band = i + 1
			break
		}
	}
	if band == 0 {
		band = legacyLabelBands[d][label] // 0 when absent
	}
	if band == 0 {
		return nil
	}
	return &dimResult{name: name(d), band: band}
}

// name is an identity helper so fromLabel reads symmetrically with fromRaw.
func name(d dim) dim { return d }

// dimDisplay is the partitions readout: the raw exact value (comma-grouped) if
// given, else the banded label, else "Not provided".
func dimDisplay(exact *float64, label string) string {
	if exact != nil && !math.IsNaN(*exact) {
		return formatCommas(*exact)
	}
	if label != "" {
		return label
	}
	return "Not provided"
}

// formatCommas renders a number for display: integers get en-US thousands
// separators, a fractional value keeps its decimals ungrouped (kcp partition
// counts are always integral).
func formatCommas(v float64) string {
	if v == math.Trunc(v) && !math.IsInf(v, 0) {
		return groupThousands(strconv.FormatInt(int64(v), 10))
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func groupThousands(s string) string {
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg, s = true, s[1:]
	}
	n := len(s)
	if n <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	lead := n % 3
	out := make([]byte, 0, n+n/3)
	if lead > 0 {
		out = append(out, s[:lead]...)
	}
	for i := lead; i < n; i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
