package types

import (
	"testing"
)

func TestNewState(t *testing.T) {
	tests := []struct {
		name        string
		fromState   *State
		wantNil     bool
		wantEmpty   bool
		wantRegions []string
	}{
		{
			name:        "nil fromState creates empty state",
			fromState:   nil,
			wantNil:     false,
			wantEmpty:   true,
			wantRegions: []string{},
		},
		{
			name: "non-nil fromState copies regions",
			fromState: &State{
				Regions: []DiscoveredRegion{
					{Name: "us-east-1"},
					{Name: "eu-west-1"},
				},
			},
			wantNil:     false,
			wantEmpty:   false,
			wantRegions: []string{"us-east-1", "eu-west-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewState(tt.fromState)

			// Check if result is nil when we expect it to be
			if (result == nil) != tt.wantNil {
				t.Errorf("NewState() returned nil = %v, want nil = %v", result == nil, tt.wantNil)
			}

			if result != nil {
				// Check if regions slice is empty when expected
				isEmpty := len(result.Regions) == 0
				if isEmpty != tt.wantEmpty {
					t.Errorf("NewState() regions empty = %v, want empty = %v", isEmpty, tt.wantEmpty)
				}

				// Check that regions match expected
				if len(result.Regions) != len(tt.wantRegions) {
					t.Errorf("NewState() got %d regions, want %d", len(result.Regions), len(tt.wantRegions))
				}

				for i, expectedName := range tt.wantRegions {
					if i >= len(result.Regions) {
						t.Errorf("NewState() missing region at index %d", i)
						continue
					}
					if result.Regions[i].Name != expectedName {
						t.Errorf("NewState() region[%d] = %q, want %q", i, result.Regions[i].Name, expectedName)
					}
				}
			}
		})
	}
}

func TestUpsertRegion(t *testing.T) {
	tests := []struct {
		name         string
		initialState *State
		upsertRegion DiscoveredRegion
		wantRegions  []DiscoveredRegion
	}{
		{
			name: "add new region to empty state",
			initialState: &State{
				Regions: []DiscoveredRegion{},
			},
			upsertRegion: DiscoveredRegion{Name: "us-west-2"},
			wantRegions: []DiscoveredRegion{
				{Name: "us-west-2"},
			},
		},
		{
			name: "add new region to existing regions",
			initialState: &State{
				Regions: []DiscoveredRegion{
					{Name: "us-east-1"},
					{Name: "eu-west-1"},
				},
			},
			upsertRegion: DiscoveredRegion{Name: "ap-south-1"},
			wantRegions: []DiscoveredRegion{
				{Name: "us-east-1"},
				{Name: "eu-west-1"},
				{Name: "ap-south-1"},
			},
		},
		{
			name: "replace existing region with new content",
			initialState: &State{
				Regions: []DiscoveredRegion{
					{Name: "us-east-1"},
					{Name: "eu-west-1", ClusterArns: []string{"old-cluster-1", "old-cluster-2"}},
					{Name: "ap-south-1"},
				},
			},
			upsertRegion: DiscoveredRegion{Name: "eu-west-1", ClusterArns: []string{"new-cluster-1", "new-cluster-2", "new-cluster-3"}},
			wantRegions: []DiscoveredRegion{
				{Name: "us-east-1"},
				{Name: "eu-west-1", ClusterArns: []string{"new-cluster-1", "new-cluster-2", "new-cluster-3"}},
				{Name: "ap-south-1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.initialState.UpsertRegion(tt.upsertRegion)

			// Check that final state matches expected exactly
			if len(tt.initialState.Regions) != len(tt.wantRegions) {
				t.Errorf("UpsertRegion() got %d regions, want %d", len(tt.initialState.Regions), len(tt.wantRegions))
			}

			for i, wantRegion := range tt.wantRegions {
				if i >= len(tt.initialState.Regions) {
					t.Errorf("UpsertRegion() missing region at index %d", i)
					continue
				}

				actualRegion := tt.initialState.Regions[i]

				// Check name
				if actualRegion.Name != wantRegion.Name {
					t.Errorf("UpsertRegion() region[%d].Name = %q, want %q", i, actualRegion.Name, wantRegion.Name)
				}

				// Check ClusterArns
				if len(actualRegion.ClusterArns) != len(wantRegion.ClusterArns) {
					t.Errorf("UpsertRegion() region[%d] got %d cluster ARNs, want %d", i, len(actualRegion.ClusterArns), len(wantRegion.ClusterArns))
				}

				for j, wantArn := range wantRegion.ClusterArns {
					if j >= len(actualRegion.ClusterArns) {
						t.Errorf("UpsertRegion() region[%d] missing cluster ARN at index %d", i, j)
						continue
					}
					if actualRegion.ClusterArns[j] != wantArn {
						t.Errorf("UpsertRegion() region[%d].ClusterArns[%d] = %q, want %q", i, j, actualRegion.ClusterArns[j], wantArn)
					}
				}
			}
		})
	}
}
