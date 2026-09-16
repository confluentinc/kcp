package engine

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// Tests for sizingBand — the capacity-band model. Each case pins a published
// Confluent capacity ceiling to the band it should produce, so a table edge that
// drifts turns these red.

func bandOf(t *testing.T, p Profile) int {
	t.Helper()
	return sizingBand(p).Band
}

func TestSizing_PartitionBandLabels(t *testing.T) {
	cases := map[string]int{
		"Under 2,500":   1, // a shared tier holds it
		"2,500–30,000":  2, // past Standard's ceiling, within the 10 eCKU PrivateLink cap
		"30,000–96,000": 3, // within the 32 eCKU PNI cap
	}
	for label, want := range cases {
		if got := bandOf(t, Profile{PartitionBand: label}); got != want {
			t.Errorf("partition_band %q: band = %d, want %d", label, got, want)
		}
	}
}

func TestSizing_BandXLDerivation(t *testing.T) {
	if got := bandOf(t, Profile{PartitionBand: "Over 30,000"}); got != bandXL {
		t.Errorf("Over 30,000: band = %d, want bandXL(%d)", got, bandXL)
	}
	if bandXL != 3 {
		t.Errorf("bandXL = %d, want 3 (derived from edge count, not hard-coded)", bandXL)
	}
	if sharedTierMaxBand != 1 {
		t.Errorf("sharedTierMaxBand = %d, want 1", sharedTierMaxBand)
	}
	if privateLinkCapBand != bandXL {
		t.Errorf("privateLinkCapBand = %d, want bandXL(%d)", privateLinkCapBand, bandXL)
	}
}

func TestSizing_SupersededLabelsPreserveDecision(t *testing.T) {
	cases := []struct {
		field, label string
		want         int
	}{
		{"partition", "Under 30,000", 2}, // 3-band set
		{"partition", "Under 3,000", 2},  // 4-band, guessed edges
		{"partition", "3,000–30,000", 2},
		{"partition", "Under 2,500", 1}, // capacity-edge set
		{"partition", "600–30,000", 2},  // pre-split set
		{"ingress", "Under 600 MB/s", 2},
	}
	for _, c := range cases {
		var p Profile
		switch c.field {
		case "partition":
			p.PartitionBand = c.label
		case "ingress":
			p.IngressBand = c.label
		}
		if got := bandOf(t, p); got != c.want {
			t.Errorf("%s %q: band = %d, want %d", c.field, c.label, got, c.want)
		}
	}
}

// A legacy answer is a RANGE; we cannot know where in it the customer sits, so it
// must resolve to the band containing its MAXIMUM. The expected band is derived
// by parsing the label (not by restating the map), so a future edit to
// legacyLabelBands cannot quietly re-introduce an under-resolving entry.
func TestSizing_LegacyLabelsNeverUnderResolve(t *testing.T) {
	fieldFor := func(d dim, label string) Profile {
		switch d {
		case dimPartitions:
			return Profile{PartitionBand: label}
		case dimIngress:
			return Profile{IngressBand: label}
		case dimEgress:
			return Profile{EgressBand: label}
		default:
			return Profile{RequestRateBand: label}
		}
	}
	// num strips thousands commas, then reads the leading numeric prefix and
	// ignores any trailing unit ("600 MB/s" -> 600) — strconv.ParseFloat rejects
	// the suffix outright.
	num := func(s string) float64 {
		s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
		i := 0
		for i < len(s) && (s[i] == '-' || s[i] == '+' || s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		v, _ := strconv.ParseFloat(s[:i], 64)
		return v
	}
	bandForMax := func(max float64, edges []int) int {
		for i, e := range edges {
			if max <= float64(e) {
				return i + 1
			}
		}
		return len(edges) + 1
	}
	checked := 0
	for d, labels := range legacyLabelBands {
		edges := bandEdges[d]
		for label := range labels {
			var max float64
			switch {
			case strings.HasPrefix(label, "Over "):
				max = math.Inf(1)
			case strings.HasPrefix(label, "Under "):
				max = num(strings.TrimPrefix(label, "Under "))
			default:
				parts := strings.FieldsFunc(label, func(r rune) bool { return r == '–' || r == '-' })
				if len(parts) < 2 {
					continue
				}
				max = num(parts[1])
			}
			var want int
			if math.IsInf(max, 1) {
				want = len(edges) + 1
			} else {
				want = bandForMax(max, edges)
			}
			if got := bandOf(t, fieldFor(d, label)); got != want {
				t.Errorf("legacy %s %q: band = %d, want %d (band of range max)", d, label, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no legacy labels checked — the derivation loop is inert")
	}
}

func TestSizing_SecondDimensionRaisesNeverLowers(t *testing.T) {
	// request rate can raise the band on its own, and reports as the driver.
	s := sizingBand(Profile{PartitionBand: "Under 2,500", RequestRateBand: "75,000–240,000/s"})
	if s.Band != 3 || s.Driver != string(dimRequestRate) {
		t.Errorf("request-rate raise: band=%d driver=%q, want 3 / %q", s.Band, s.Driver, dimRequestRate)
	}
	// ingress alone can raise the band past low partitions.
	if got := bandOf(t, Profile{PartitionBand: "Under 2,500", IngressBand: "Over 1,920 MB/s"}); got != bandXL {
		t.Errorf("ingress raise: band=%d, want bandXL(%d)", got, bandXL)
	}
	// a lower second dimension can never pull the band down.
	if got := bandOf(t, Profile{PartitionBand: "Over 96,000", EgressBand: "Under 750 MB/s"}); got != bandXL {
		t.Errorf("egress must not lower: band=%d, want bandXL(%d)", got, bandXL)
	}
}

func TestSizing_RawNumbers(t *testing.T) {
	// ingress 650 MBps (Band 3) beats partitions 25,000 (Band 2).
	s := sizingBand(Profile{PartitionsExact: f(25000), PeakIngressMbps: f(650)})
	if s.Band != 3 || s.Driver != string(dimIngress) {
		t.Errorf("raw ingress-beats-partitions: band=%d driver=%q, want 3 / %q", s.Band, s.Driver, dimIngress)
	}
	// banding works on an exact figure; no approaching-ceiling field is set.
	s = sizingBand(Profile{PartitionsExact: f(22000)})
	if s.Band != 2 {
		t.Errorf("exact 22000: band=%d, want 2", s.Band)
	}
	// given.partitions on an exact 5000 → band 2.
	if g := sizingBand(Profile{PartitionsExact: f(5000)}).Given[dimPartitions]; g != 2 {
		t.Errorf("exact 5000: given.partitions=%d, want 2", g)
	}
}

func TestSizing_NegativeZeroNonFiniteRejected(t *testing.T) {
	for _, v := range []float64{-1e9, 0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN()} {
		g := sizingBand(Profile{PartitionsExact: f(v)}).Given
		if _, ok := g[dimPartitions]; ok {
			t.Errorf("%v must not be accepted as a partition count (given.partitions present)", v)
		}
	}
	if g := sizingBand(Profile{PartitionsExact: f(5000)}).Given[dimPartitions]; g != 2 {
		t.Errorf("sanity: exact 5000 given.partitions=%d, want 2", g)
	}
}

func TestSizing_MissingPartitions(t *testing.T) {
	// partitions alone is a complete answer — throughput is assumed, not missing.
	if s := sizingBand(Profile{PartitionBand: "Under 2,500"}); s.MissingPartitions || s.Band != 1 {
		t.Errorf("partition-only: missing=%v band=%d, want false / 1", s.MissingPartitions, s.Band)
	}
	// no sizing answer at all is a non-committable lower bound.
	if !sizingBand(Profile{}).MissingPartitions {
		t.Error("empty profile: missingPartitions=false, want true")
	}
}

func TestSizing_Serverless(t *testing.T) {
	p := Profile{MSKClusterType: MSKServerless}
	s := sizingBand(p)
	if s.Band != 1 || !s.ServerlessCapped {
		t.Errorf("serverless: band=%d serverlessCapped=%v, want 1 / true", s.Band, s.ServerlessCapped)
	}
	if s.Driver != "source platform" {
		t.Errorf("serverless driver=%q, want %q", s.Driver, "source platform")
	}
	// a stale/hand-edited answer cannot push a Serverless source out of Band 1.
	stale := Profile{MSKClusterType: MSKServerless, PartitionBand: "Over 96,000"}
	if got := bandOf(t, stale); got != 1 {
		t.Errorf("serverless with stale Over 96,000: band=%d, want 1", got)
	}
	// a missing partition count on Serverless is by design, not a gap.
	if sizingBand(Profile{MSKClusterType: MSKServerless, PeakIngressMbps: f(50)}).MissingPartitions {
		t.Error("serverless missingPartitions=true, want false")
	}
	// every published Serverless quota (as raw numbers) still lands in Band 1.
	q := sizingBand(Profile{PartitionsExact: f(2400), PeakIngressMbps: f(200), PeakEgressMbps: f(400), PeakRequestsPerSec: f(15000)})
	if q.Band != 1 {
		t.Errorf("serverless quotas as raw numbers: band=%d, want 1", q.Band)
	}
}

func TestSizing_PartitionsDimDisplay(t *testing.T) {
	if got := sizingBand(Profile{PartitionsExact: f(30000)}).PartitionsDim.Display; got != "30,000" {
		t.Errorf("display exact 30000 = %q, want %q", got, "30,000")
	}
	if got := sizingBand(Profile{PartitionBand: "2,500–30,000"}).PartitionsDim.Display; got != "2,500–30,000" {
		t.Errorf("display label = %q, want the label", got)
	}
	if got := sizingBand(Profile{}).PartitionsDim.Display; got != "Not provided" {
		t.Errorf("display none = %q, want %q", got, "Not provided")
	}
}
