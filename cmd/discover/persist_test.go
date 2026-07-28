package discover

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestPersistDiscoveredRegion(t *testing.T) {
	// A fresh state/creds containing two clusters (A, B) in one region.
	mk := func() (*types.State, *types.Credentials) {
		st := &types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{
			Name: "us-east-1",
			Clusters: []types.DiscoveredCluster{
				{Arn: "arn:aws:kafka:us-east-1:111:cluster/a/uuid"},
				{Arn: "arn:aws:kafka:us-east-1:111:cluster/b/uuid"},
			},
		}}}}
		cr := &types.Credentials{Regions: []types.RegionAuth{{
			Name: "us-east-1",
			Clusters: []types.ClusterAuth{
				{Arn: "arn:aws:kafka:us-east-1:111:cluster/a/uuid"},
				{Arn: "arn:aws:kafka:us-east-1:111:cluster/b/uuid"},
			},
		}}}
		return st, cr
	}

	// A discovery run that produced only cluster A (e.g. `--cluster-arn <A>`).
	region := types.DiscoveredRegion{
		Name:     "us-east-1",
		Clusters: []types.DiscoveredCluster{{Arn: "arn:aws:kafka:us-east-1:111:cluster/a/uuid"}},
	}
	regionAuth := types.RegionAuth{
		Name:     "us-east-1",
		Clusters: []types.ClusterAuth{{Arn: "arn:aws:kafka:us-east-1:111:cluster/a/uuid"}},
	}

	t.Run("full-region replaces the whole cluster list (prunes B)", func(t *testing.T) {
		st, cr := mk()
		persistDiscoveredRegion(st, cr, region, regionAuth, false)
		if got := len(st.MSKSources.Regions[0].Clusters); got != 1 {
			t.Errorf("state: got %d clusters, want 1 (full-region replace prunes B)", got)
		}
		if got := len(cr.Regions[0].Clusters); got != 1 {
			t.Errorf("creds: got %d clusters, want 1 (full-region replace prunes B)", got)
		}
	})

	t.Run("targeted preserves sibling B", func(t *testing.T) {
		st, cr := mk()
		persistDiscoveredRegion(st, cr, region, regionAuth, true)
		if got := len(st.MSKSources.Regions[0].Clusters); got != 2 {
			t.Errorf("state: got %d clusters, want 2 (targeted preserves B)", got)
		}
		if got := len(cr.Regions[0].Clusters); got != 2 {
			t.Errorf("creds: got %d clusters, want 2 (targeted preserves B)", got)
		}
	})
}
