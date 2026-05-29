package plan

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

// mskUsageTypePattern parses the AWS Cost Explorer usage strings the
// MSK service emits. Examples:
//
//	USE1-Kafka.m5.large
//	APN1-Express.m7g.large
//	EU-Kafka.kraft.t3.small
//
// Capture groups: [1] family (Kafka | Express), [2] instance type
// (m5.large, m7g.large, kraft.t3.small). The leading region prefix
// (USE1 / APN1 / EU / …) is intentionally not captured — the cost
// record's own `Region` field is the source of truth for region.
var mskUsageTypePattern = regexp.MustCompile(`^[A-Z0-9]+-((?:Kafka|Express))\.([a-z0-9.]+)$`)

// DetectCostReconciliation produces the per-region diff between MSK
// instance types in the AWS cost report and instance types that
// `kcp discover` actually found in the inventory. Sorted by spend
// desc; no materiality threshold (the customer decides what's
// "real"). Returns nil when there are no candidates or no MSK source
// data — the renderer omits the section in that case.
func DetectCostReconciliation(state types.ProcessedState) *types.CostReconciliationSection {
	var candidates []types.HiddenClusterCandidate
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		for _, region := range src.MSKData.Regions {
			inventory := inventoryInstanceTypes(region)
			byType := aggregateCostByInstanceType(region.Costs)
			for instType, agg := range byType {
				if _, present := inventory[instType]; present {
					continue
				}
				candidates = append(candidates, types.HiddenClusterCandidate{
					Region:         region.Name,
					InstanceType:   instType,
					TotalSpend:     agg.totalSpend,
					MonthsObserved: agg.monthsObserved,
					DaysObserved:   agg.daysObserved,
				})
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Sort by spend desc; tie-break on (Region, InstanceType) so the
	// output is deterministic across runs even when two candidates
	// share the same spend.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].TotalSpend != candidates[j].TotalSpend {
			return candidates[i].TotalSpend > candidates[j].TotalSpend
		}
		if candidates[i].Region != candidates[j].Region {
			return candidates[i].Region < candidates[j].Region
		}
		return candidates[i].InstanceType < candidates[j].InstanceType
	})
	return &types.CostReconciliationSection{Candidates: candidates}
}

// inventoryInstanceTypes returns the set of instance types
// `kcp discover` found in the given region. Stored as a map for
// O(1) lookup during the diff.
func inventoryInstanceTypes(region types.ProcessedRegion) map[string]struct{} {
	out := map[string]struct{}{}
	for _, c := range region.Clusters {
		if t := brokerInstanceType(c); t != "" {
			out[t] = struct{}{}
		}
	}
	return out
}

// costAgg accumulates per-instance-type cost across observation
// windows. monthsObserved counts distinct month prefixes (`YYYY-MM`)
// in the `Start` timestamps; daysObserved counts distinct
// `YYYY-MM-DD` values. Both are surfaced in the rendered section so
// the customer can judge whether a candidate looks like a
// short-lived cluster vs. a long-running one.
type costAgg struct {
	totalSpend     float64
	months         map[string]struct{}
	days           map[string]struct{}
	monthsObserved int
	daysObserved   int
}

func aggregateCostByInstanceType(costs types.ProcessedRegionCosts) map[string]*costAgg {
	out := map[string]*costAgg{}
	for _, r := range costs.Results {
		instType := parseMSKInstanceType(r.UsageType)
		if instType == "" {
			continue
		}
		agg, ok := out[instType]
		if !ok {
			agg = &costAgg{
				months: map[string]struct{}{},
				days:   map[string]struct{}{},
			}
			out[instType] = agg
		}
		agg.totalSpend += r.Values.UnblendedCost
		// Cost Explorer Start is `YYYY-MM-DD`; trim to substrings.
		if len(r.Start) >= 7 {
			agg.months[r.Start[:7]] = struct{}{}
		}
		if len(r.Start) >= 10 {
			agg.days[r.Start[:10]] = struct{}{}
		}
		// Tolerate RFC3339 timestamps too.
		if t, err := time.Parse(time.RFC3339, r.Start); err == nil {
			agg.months[t.Format("2006-01")] = struct{}{}
			agg.days[t.Format("2006-01-02")] = struct{}{}
		}
	}
	for _, agg := range out {
		agg.monthsObserved = len(agg.months)
		agg.daysObserved = len(agg.days)
	}
	return out
}

// parseMSKInstanceType extracts the MSK instance type from an AWS
// Cost Explorer usage string. Returns empty when the string isn't an
// MSK usage type (Cost Explorer mixes data-transfer / EBS / etc.
// rows into the MSK service's results — we only care about broker
// instance rows for the diff).
func parseMSKInstanceType(usageType string) string {
	m := mskUsageTypePattern.FindStringSubmatch(usageType)
	if m == nil {
		return ""
	}
	// m[1] = Kafka | Express, m[2] = instance type (e.g. m5.large).
	// The rendered shape ("kafka.m5.large" / "express.m7g.large")
	// mirrors what BrokerNodeGroupInfo.InstanceType actually carries.
	return strings.ToLower(m[1]) + "." + m[2]
}

// detectCostReconciliationOpenQuestions emits an OQ when the cost
// data is empty across all regions — the diff isn't actionable
// without cost data. Returns nil when at least one region has cost
// data (even if the diff was clean).
func detectCostReconciliationOpenQuestions(state types.ProcessedState) []types.OpenQuestion {
	hasCost := false
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		for _, region := range src.MSKData.Regions {
			if len(region.Costs.Results) > 0 {
				hasCost = true
				break
			}
		}
		if hasCost {
			break
		}
	}
	if hasCost {
		return nil
	}
	// If there are no MSK regions at all, this signal isn't
	// applicable — the renderer's broader empty-fleet handling
	// covers that case.
	if !hasAnyMSKRegion(state) {
		return nil
	}
	return []types.OpenQuestion{{
		ID:         "cost_data_not_collected",
		Title:      "Cost-vs-inventory reconciliation skipped — run `kcp report costs`",
		Body:       "No AWS Cost Explorer data is present in the state file. The cost-vs-inventory diff (which surfaces MSK instance types billed by AWS but NOT discovered by `kcp discover`) needs cost data to run.",
		HowToClose: "Run `kcp report costs --state-file <path> --region <region> --start <YYYY-MM-DD> --end <YYYY-MM-DD>` for each source region, then re-run `kcp report plan`.",
	}}
}

func hasAnyMSKRegion(state types.ProcessedState) bool {
	for _, src := range state.Sources {
		if src.MSKData != nil && len(src.MSKData.Regions) > 0 {
			return true
		}
	}
	return false
}
