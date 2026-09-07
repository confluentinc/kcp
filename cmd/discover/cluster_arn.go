package discover

import (
	"github.com/confluentinc/kcp/internal/utils"
)

// regionsFromClusterArns returns the distinct AWS regions parsed from the given MSK
// cluster ARNs, preserving first-seen order. Returns an error if any ARN is malformed.
// Thin wrapper over utils.RegionsFromClusterArns, kept so callers in this package read
// naturally; the shared implementation lives in internal/utils.
func regionsFromClusterArns(clusterArns []string) ([]string, error) {
	return utils.RegionsFromClusterArns(clusterArns)
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
