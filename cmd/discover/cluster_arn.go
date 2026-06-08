package discover

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
)

// regionsFromClusterArns returns the distinct AWS regions parsed from the given MSK
// cluster ARNs, preserving first-seen order. Returns an error if any ARN is malformed.
func regionsFromClusterArns(clusterArns []string) ([]string, error) {
	seen := map[string]bool{}
	regions := []string{}
	for _, arn := range clusterArns {
		region, err := utils.ExtractRegionFromArn(arn)
		if err != nil {
			return nil, fmt.Errorf("invalid cluster ARN %q: %w", arn, err)
		}
		if !seen[region] {
			seen[region] = true
			regions = append(regions, region)
		}
	}
	return regions, nil
}

// filterArnsToDiscover returns the subset of regionArns that appear in targetArns.
// When targetArns is empty, all regionArns are returned (full-region discovery).
func filterArnsToDiscover(regionArns, targetArns []string) []string {
	if len(targetArns) == 0 {
		return regionArns
	}
	target := map[string]bool{}
	for _, a := range targetArns {
		target[a] = true
	}
	filtered := []string{}
	for _, a := range regionArns {
		if target[a] {
			filtered = append(filtered, a)
		}
	}
	return filtered
}
