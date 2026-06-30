package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/state/migrate"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
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
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{Name: "us-east-1"},
						{Name: "eu-west-1"},
					},
				},
			},
			wantNil:     false,
			wantEmpty:   false,
			wantRegions: []string{"us-east-1", "eu-west-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewStateFrom(tt.fromState)

			// Check if result is nil when we expect it to be
			if (result == nil) != tt.wantNil {
				t.Errorf("NewState() returned nil = %v, want nil = %v", result == nil, tt.wantNil)
			}

			if result != nil {
				// Check if MSKSources exists and regions slice is empty when expected
				var regions []DiscoveredRegion
				if result.MSKSources != nil {
					regions = result.MSKSources.Regions
				}
				isEmpty := len(regions) == 0
				if isEmpty != tt.wantEmpty {
					t.Errorf("NewState() regions empty = %v, want empty = %v", isEmpty, tt.wantEmpty)
				}

				// Check that regions match expected
				if len(regions) != len(tt.wantRegions) {
					t.Errorf("NewState() got %d regions, want %d", len(regions), len(tt.wantRegions))
				}

				for i, expectedName := range tt.wantRegions {
					if i >= len(regions) {
						t.Errorf("NewState() missing region at index %d", i)
						continue
					}
					if regions[i].Name != expectedName {
						t.Errorf("NewState() region[%d] = %q, want %q", i, regions[i].Name, expectedName)
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
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{},
				},
			},
			upsertRegion: DiscoveredRegion{Name: "us-west-2"},
			wantRegions: []DiscoveredRegion{
				{Name: "us-west-2"},
			},
		},
		{
			name: "add new region to existing regions",
			initialState: &State{
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{Name: "us-east-1"},
						{Name: "eu-west-1"},
					},
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
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{Name: "us-east-1"},
						{Name: "eu-west-1", ClusterArns: []string{"old-cluster-1", "old-cluster-2"}},
						{Name: "ap-south-1"},
					},
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
			if tt.initialState.MSKSources == nil {
				t.Fatal("UpsertRegion() MSKSources is nil")
			}
			if len(tt.initialState.MSKSources.Regions) != len(tt.wantRegions) {
				t.Errorf("UpsertRegion() got %d regions, want %d", len(tt.initialState.MSKSources.Regions), len(tt.wantRegions))
			}

			for i, wantRegion := range tt.wantRegions {
				if i >= len(tt.initialState.MSKSources.Regions) {
					t.Errorf("UpsertRegion() missing region at index %d", i)
					continue
				}

				actualRegion := tt.initialState.MSKSources.Regions[i]

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

func TestRefreshClusters(t *testing.T) {
	tests := []struct {
		name            string
		initialClusters []DiscoveredCluster
		newClusters     []DiscoveredCluster
		wantClusters    []DiscoveredCluster
	}{
		{
			name:            "add clusters to empty region",
			initialClusters: []DiscoveredCluster{},
			newClusters: []DiscoveredCluster{
				{Name: "cluster-1", Arn: "arn:cluster-1"},
				{Name: "cluster-2", Arn: "arn:cluster-2"},
			},
			wantClusters: []DiscoveredCluster{
				{Name: "cluster-1", Arn: "arn:cluster-1"},
				{Name: "cluster-2", Arn: "arn:cluster-2"},
			},
		},
		{
			name: "preserve admin info for existing clusters",
			initialClusters: []DiscoveredCluster{
				{
					Name:                        "cluster-1",
					Arn:                         "arn:cluster-1",
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: "old-cluster-1-id"},
				},
				{
					Name:                        "cluster-2",
					Arn:                         "arn:cluster-2",
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: "old-cluster-2-id"},
				},
			},
			newClusters: []DiscoveredCluster{
				{Name: "cluster-1", Arn: "arn:cluster-1"}, // fresh discovery data
				{Name: "cluster-3", Arn: "arn:cluster-3"}, // new cluster
			},
			wantClusters: []DiscoveredCluster{
				{
					Name:                        "cluster-1",
					Arn:                         "arn:cluster-1",
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: "old-cluster-1-id"}, // preserved
				},
				{Name: "cluster-3", Arn: "arn:cluster-3"}, // no admin info to preserve
			},
		},
		{
			name: "new discovery takes precedence over old empty values",
			initialClusters: []DiscoveredCluster{
				{
					Name: "cluster-1",
					Arn:  "arn:cluster-1",
					// Old state had no ClusterID (failed first run)
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: ""},
				},
			},
			newClusters: []DiscoveredCluster{
				{
					Name: "cluster-1",
					Arn:  "arn:cluster-1",
					// New discovery has ClusterID (successful second run)
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: "new-cluster-id"},
				},
			},
			wantClusters: []DiscoveredCluster{
				{
					Name:                        "cluster-1",
					Arn:                         "arn:cluster-1",
					KafkaAdminClientInformation: KafkaAdminClientInformation{ClusterID: "new-cluster-id"}, // new discovery wins
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region := &DiscoveredRegion{
				Name:     "test-region",
				Clusters: tt.initialClusters,
			}

			region.RefreshClusters(tt.newClusters)

			// Check that final clusters match expected
			if len(region.Clusters) != len(tt.wantClusters) {
				t.Errorf("RefreshClusters() got %d clusters, want %d", len(region.Clusters), len(tt.wantClusters))
			}

			for i, wantCluster := range tt.wantClusters {
				if i >= len(region.Clusters) {
					t.Errorf("RefreshClusters() missing cluster at index %d", i)
					continue
				}

				actualCluster := region.Clusters[i]
				if actualCluster.Name != wantCluster.Name {
					t.Errorf("RefreshClusters() cluster[%d].Name = %q, want %q", i, actualCluster.Name, wantCluster.Name)
				}
				if actualCluster.Arn != wantCluster.Arn {
					t.Errorf("RefreshClusters() cluster[%d].Arn = %q, want %q", i, actualCluster.Arn, wantCluster.Arn)
				}
				if actualCluster.KafkaAdminClientInformation.ClusterID != wantCluster.KafkaAdminClientInformation.ClusterID {
					t.Errorf("RefreshClusters() cluster[%d].KafkaAdminClientInformation.ClusterID = %q, want %q",
						i, actualCluster.KafkaAdminClientInformation.ClusterID, wantCluster.KafkaAdminClientInformation.ClusterID)
				}
			}
		})
	}
}

func TestWriteReportCommands(t *testing.T) {
	tests := []struct {
		name           string
		state          *State
		stateFilePath  string
		wantContains   []string
		wantNotContain []string
		wantError      bool
	}{
		{
			name: "empty state writes headers only",
			state: &State{
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{},
				},
			},
			stateFilePath: "/path/to/state.json",
			wantContains: []string{
				"# Report region costs commands",
				"# Report cluster metrics commands",
			},
			wantNotContain: []string{
				"kcp report costs",
				"kcp report metrics",
			},
			wantError: false,
		},
		{
			name: "state with regions but no clusters",
			state: &State{
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{Name: "us-east-1", Clusters: []DiscoveredCluster{}},
						{Name: "eu-west-1", Clusters: []DiscoveredCluster{}},
					},
				},
			},
			stateFilePath: "/path/to/state.json",
			wantContains: []string{
				"# Report region costs commands",
				"# Report cluster metrics commands",
				"# region: us-east-1",
				"# region: eu-west-1",
				"kcp report costs --state-file /path/to/state.json --region us-east-1 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report costs --state-file /path/to/state.json --region eu-west-1 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
			},
			wantNotContain: []string{
				"kcp report metrics",
			},
			wantError: false,
		},
		{
			name: "state with regions and clusters",
			state: &State{
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{
							Name: "us-east-1",
							Clusters: []DiscoveredCluster{
								{Name: "cluster-1", Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/cluster-1/abc123"},
								{Name: "cluster-2", Arn: "arn:aws:kafka:us-east-1:123456789012:cluster/cluster-2/def456"},
							},
						},
						{
							Name: "eu-west-1",
							Clusters: []DiscoveredCluster{
								{Name: "cluster-3", Arn: "arn:aws:kafka:eu-west-1:123456789012:cluster/cluster-3/ghi789"},
							},
						},
					},
				},
			},
			stateFilePath: "/path/to/state.json",
			wantContains: []string{
				"# Report region costs commands",
				"# Report cluster metrics commands",
				"# region: us-east-1",
				"# region: eu-west-1",
				"kcp report costs --state-file /path/to/state.json --region us-east-1 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report costs --state-file /path/to/state.json --region eu-west-1 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"# cluster: cluster-1",
				"# cluster: cluster-2",
				"# cluster: cluster-3",
				"kcp report metrics --state-file /path/to/state.json --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/cluster-1/abc123 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report metrics --state-file /path/to/state.json --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/cluster-2/def456 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report metrics --state-file /path/to/state.json --cluster-id arn:aws:kafka:eu-west-1:123456789012:cluster/cluster-3/ghi789 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
			},
			wantError: false,
		},
		{
			name: "state with single region and single cluster",
			state: &State{
				MSKSources: &MSKSourcesState{
					Regions: []DiscoveredRegion{
						{
							Name: "ap-south-1",
							Clusters: []DiscoveredCluster{
								{Name: "my-cluster", Arn: "arn:aws:kafka:ap-south-1:123456789012:cluster/my-cluster/xyz789"},
							},
						},
					},
				},
			},
			stateFilePath: "./kcp-state.json",
			wantContains: []string{
				"# Report region costs commands",
				"# Report cluster metrics commands",
				"# region: ap-south-1",
				"# cluster: my-cluster",
				"kcp report costs --state-file ./kcp-state.json --region ap-south-1 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report metrics --state-file ./kcp-state.json --cluster-id arn:aws:kafka:ap-south-1:123456789012:cluster/my-cluster/xyz789 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file for testing
			tmpFile, err := os.CreateTemp("", "test-report-commands-*.txt")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			tmpFilePath := tmpFile.Name()
			_ = tmpFile.Close()
			defer func() { _ = os.Remove(tmpFilePath) }()

			// Call the method
			err = tt.state.WriteReportCommands(tmpFilePath, tt.stateFilePath)

			// Check for expected error
			if (err != nil) != tt.wantError {
				t.Errorf("WriteReportCommands() error = %v, wantError %v", err, tt.wantError)
				return
			}

			if !tt.wantError {
				// Read the file content
				content, err := os.ReadFile(tmpFilePath)
				if err != nil {
					t.Fatalf("failed to read output file: %v", err)
				}
				contentStr := string(content)

				// Check that all expected strings are present
				for _, wantStr := range tt.wantContains {
					if !strings.Contains(contentStr, wantStr) {
						t.Errorf("WriteReportCommands() output does not contain expected string: %q\nGot content:\n%s", wantStr, contentStr)
					}
				}

				// Check that unwanted strings are not present
				for _, notWantStr := range tt.wantNotContain {
					if strings.Contains(contentStr, notWantStr) {
						t.Errorf("WriteReportCommands() output contains unexpected string: %q\nGot content:\n%s", notWantStr, contentStr)
					}
				}

				// Verify file structure: should have region commands section, blank line, then cluster commands section
				lines := strings.Split(contentStr, "\n")
				hasRegionHeader := false
				hasClusterHeader := false

				for _, line := range lines {
					if strings.Contains(line, "# Report region costs commands") {
						hasRegionHeader = true
					}
					if strings.Contains(line, "# Report cluster metrics commands") {
						hasClusterHeader = true
					}
				}

				if !hasRegionHeader {
					t.Error("WriteReportCommands() output missing region costs commands header")
				}
				if !hasClusterHeader {
					t.Error("WriteReportCommands() output missing cluster metrics commands header")
				}
			}
		})
	}
}

func TestWriteReportCommands_FileError(t *testing.T) {
	// Test error handling for invalid file path
	state := &State{
		MSKSources: &MSKSourcesState{
			Regions: []DiscoveredRegion{
				{Name: "us-east-1", Clusters: []DiscoveredCluster{}},
			},
		},
	}

	// Try to write to an invalid path (directory that doesn't exist)
	invalidPath := "/nonexistent/directory/file.txt"
	err := state.WriteReportCommands(invalidPath, "/path/to/state.json")

	if err == nil {
		t.Error("WriteReportCommands() expected error for invalid file path, got nil")
	}

	if !strings.Contains(err.Error(), "failed to write commands to file") {
		t.Errorf("WriteReportCommands() error message should mention file write failure, got: %v", err)
	}
}

func TestKafkaAdminClientInformation_MergeFrom(t *testing.T) {
	tests := []struct {
		name     string
		current  KafkaAdminClientInformation
		other    KafkaAdminClientInformation
		expected KafkaAdminClientInformation
	}{
		{
			name: "new discovery with topics takes precedence over old empty",
			current: KafkaAdminClientInformation{
				ClusterID: "new-cluster-id",
				Topics: &Topics{
					Details: []TopicDetails{{Name: "new-topic", Partitions: 3}},
				},
			},
			other: KafkaAdminClientInformation{
				ClusterID: "old-cluster-id",
				Topics:    nil, // old run had no topics (permission error)
			},
			expected: KafkaAdminClientInformation{
				ClusterID: "new-cluster-id", // new takes precedence
				Topics: &Topics{
					Details: []TopicDetails{{Name: "new-topic", Partitions: 3}}, // new takes precedence
				},
			},
		},
		{
			name: "old values used when new is empty",
			current: KafkaAdminClientInformation{
				ClusterID: "",
				Topics:    nil,
				Acls:      nil,
			},
			other: KafkaAdminClientInformation{
				ClusterID: "old-cluster-id",
				Topics:    &Topics{Details: []TopicDetails{{Name: "old-topic"}}},
				Acls:      []Acls{{ResourceName: "old-acl"}},
			},
			expected: KafkaAdminClientInformation{
				ClusterID: "old-cluster-id",
				Topics:    &Topics{Details: []TopicDetails{{Name: "old-topic"}}},
				Acls:      []Acls{{ResourceName: "old-acl"}},
			},
		},
		{
			name: "old topics preserved when new has empty details (failed third run)",
			current: KafkaAdminClientInformation{
				// Third run failed - Topics struct exists but Details is empty
				Topics: &Topics{
					Details: []TopicDetails{}, // empty slice, NOT nil
					Summary: TopicSummary{},
				},
			},
			other: KafkaAdminClientInformation{
				// Second run succeeded - has actual topics
				Topics: &Topics{
					Details: []TopicDetails{{Name: "topic-from-successful-run", Partitions: 5}},
					Summary: TopicSummary{Topics: 1, TotalPartitions: 5},
				},
			},
			expected: KafkaAdminClientInformation{
				// Should preserve old topics from successful run
				Topics: &Topics{
					Details: []TopicDetails{{Name: "topic-from-successful-run", Partitions: 5}},
				},
			},
		},
		{
			name: "old connectors preserved when new has empty connectors",
			current: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{}, // empty slice
				},
			},
			other: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{{Name: "old-connector"}},
				},
			},
			expected: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{{Name: "old-connector"}}, // old preserved
				},
			},
		},
		{
			// Regression test for the post-refactor shape. A fresh
			// `KafkaAdminClientInformation` returned from ScanKafkaResources has
			// SelfManagedConnectors == nil (not an empty slice). The merge must
			// preserve connectors that already exist in state. Locks in R6.
			name: "old connectors preserved when new is nil (post-refactor scan-clusters shape)",
			current: KafkaAdminClientInformation{
				SelfManagedConnectors: nil,
			},
			other: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{{Name: "rest-discovered-connector", State: "RUNNING"}},
				},
			},
			expected: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{{Name: "rest-discovered-connector", State: "RUNNING"}},
				},
			},
		},
		{
			name: "topics are merged - new added, old preserved, duplicates use new",
			current: KafkaAdminClientInformation{
				Topics: &Topics{
					Details: []TopicDetails{
						{Name: "topic-a", Partitions: 5}, // updated partition count
						{Name: "topic-c", Partitions: 2}, // new topic
					},
				},
			},
			other: KafkaAdminClientInformation{
				Topics: &Topics{
					Details: []TopicDetails{
						{Name: "topic-a", Partitions: 3}, // old partition count
						{Name: "topic-b", Partitions: 4}, // will be preserved
					},
				},
			},
			expected: KafkaAdminClientInformation{
				Topics: &Topics{
					Details: []TopicDetails{
						{Name: "topic-a", Partitions: 5}, // new takes precedence
						{Name: "topic-b", Partitions: 4}, // preserved from old
						{Name: "topic-c", Partitions: 2}, // added from new
					},
				},
			},
		},
		{
			name: "acls are merged - no duplicates",
			current: KafkaAdminClientInformation{
				Acls: []Acls{
					{ResourceName: "new-acl", ResourceType: "Topic", Operation: "Read"},
					{ResourceName: "shared-acl", ResourceType: "Topic", Operation: "Write"}, // duplicate
				},
			},
			other: KafkaAdminClientInformation{
				Acls: []Acls{
					{ResourceName: "old-acl", ResourceType: "Topic", Operation: "Read"},
					{ResourceName: "shared-acl", ResourceType: "Topic", Operation: "Write"}, // duplicate
				},
			},
			expected: KafkaAdminClientInformation{
				Acls: []Acls{
					{ResourceName: "new-acl", ResourceType: "Topic", Operation: "Read"},
					{ResourceName: "old-acl", ResourceType: "Topic", Operation: "Read"},
					{ResourceName: "shared-acl", ResourceType: "Topic", Operation: "Write"}, // deduplicated
				},
			},
		},
		{
			name: "connectors are merged by name",
			current: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{
						{Name: "connector-a", State: "RUNNING"},
						{Name: "connector-c", State: "PAUSED"},
					},
				},
			},
			other: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{
						{Name: "connector-a", State: "PAUSED"},  // old state
						{Name: "connector-b", State: "RUNNING"}, // preserved
					},
				},
			},
			expected: KafkaAdminClientInformation{
				SelfManagedConnectors: &SelfManagedConnectors{
					Connectors: []SelfManagedConnector{
						{Name: "connector-a", State: "RUNNING"}, // new takes precedence
						{Name: "connector-b", State: "RUNNING"}, // preserved from old
						{Name: "connector-c", State: "PAUSED"},  // added from new
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := tt.current
			current.MergeFrom(tt.other)

			if current.ClusterID != tt.expected.ClusterID {
				t.Errorf("MergeFrom() ClusterID = %q, want %q", current.ClusterID, tt.expected.ClusterID)
			}

			// Check Topics (order-independent)
			if (current.Topics == nil) != (tt.expected.Topics == nil) {
				t.Errorf("MergeFrom() Topics nil mismatch: got nil=%v, want nil=%v", current.Topics == nil, tt.expected.Topics == nil)
			} else if current.Topics != nil && tt.expected.Topics != nil {
				if len(current.Topics.Details) != len(tt.expected.Topics.Details) {
					t.Errorf("MergeFrom() Topics.Details length = %d, want %d", len(current.Topics.Details), len(tt.expected.Topics.Details))
				} else {
					// Check all expected topics are present with correct values
					topicsByName := make(map[string]TopicDetails)
					for _, topic := range current.Topics.Details {
						topicsByName[topic.Name] = topic
					}
					for _, expectedTopic := range tt.expected.Topics.Details {
						if actualTopic, exists := topicsByName[expectedTopic.Name]; !exists {
							t.Errorf("MergeFrom() missing expected topic %q", expectedTopic.Name)
						} else if actualTopic.Partitions != expectedTopic.Partitions {
							t.Errorf("MergeFrom() topic %q partitions = %d, want %d", expectedTopic.Name, actualTopic.Partitions, expectedTopic.Partitions)
						}
					}
				}
			}

			// Check Acls (order-independent)
			if len(current.Acls) != len(tt.expected.Acls) {
				t.Errorf("MergeFrom() Acls length = %d, want %d", len(current.Acls), len(tt.expected.Acls))
			} else if len(tt.expected.Acls) > 0 {
				// Check all expected ACLs are present
				aclsByName := make(map[string]bool)
				for _, acl := range current.Acls {
					aclsByName[acl.ResourceName] = true
				}
				for _, expectedAcl := range tt.expected.Acls {
					if !aclsByName[expectedAcl.ResourceName] {
						t.Errorf("MergeFrom() missing expected ACL with ResourceName %q", expectedAcl.ResourceName)
					}
				}
			}

			// Check SelfManagedConnectors (order-independent)
			if (current.SelfManagedConnectors == nil) != (tt.expected.SelfManagedConnectors == nil) {
				t.Errorf("MergeFrom() SelfManagedConnectors nil mismatch")
			} else if current.SelfManagedConnectors != nil && tt.expected.SelfManagedConnectors != nil {
				if len(current.SelfManagedConnectors.Connectors) != len(tt.expected.SelfManagedConnectors.Connectors) {
					t.Errorf("MergeFrom() SelfManagedConnectors.Connectors length = %d, want %d",
						len(current.SelfManagedConnectors.Connectors), len(tt.expected.SelfManagedConnectors.Connectors))
				} else {
					// Check all expected connectors are present with correct state
					connectorsByName := make(map[string]SelfManagedConnector)
					for _, c := range current.SelfManagedConnectors.Connectors {
						connectorsByName[c.Name] = c
					}
					for _, expectedConn := range tt.expected.SelfManagedConnectors.Connectors {
						if actualConn, exists := connectorsByName[expectedConn.Name]; !exists {
							t.Errorf("MergeFrom() missing expected connector %q", expectedConn.Name)
						} else if actualConn.State != expectedConn.State {
							t.Errorf("MergeFrom() connector %q state = %q, want %q", expectedConn.Name, actualConn.State, expectedConn.State)
						}
					}
				}
			}
		})
	}
}

func TestOSKDiscoveredCluster_Structure(t *testing.T) {
	cluster := OSKDiscoveredCluster{
		ID:               "prod-kafka-01",
		BootstrapServers: []string{"broker1:9092"},
		Metadata: OSKClusterMetadata{
			Environment:  "production",
			Location:     "us-datacenter-1",
			KafkaVersion: "3.6.0",
		},
	}

	if cluster.ID != "prod-kafka-01" {
		t.Errorf("expected ID 'prod-kafka-01', got '%s'", cluster.ID)
	}
	if cluster.Metadata.Environment != "production" {
		t.Errorf("expected environment 'production', got '%s'", cluster.Metadata.Environment)
	}
}

func TestOSKSourcesState_Structure(t *testing.T) {
	state := OSKSourcesState{
		Clusters: []OSKDiscoveredCluster{
			{ID: "cluster-1"},
			{ID: "cluster-2"},
		},
	}

	if len(state.Clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(state.Clusters))
	}
}

func TestNewStateFrom_AlwaysInitializesBothSources(t *testing.T) {
	// Test nil input
	state := NewStateFrom(nil)
	if state.MSKSources == nil {
		t.Error("MSKSources should be initialized, got nil")
	}
	if state.OSKSources == nil {
		t.Error("OSKSources should be initialized, got nil")
	}
	if len(state.MSKSources.Regions) != 0 {
		t.Errorf("MSKSources.Regions should be empty, got %d items", len(state.MSKSources.Regions))
	}
	if len(state.OSKSources.Clusters) != 0 {
		t.Errorf("OSKSources.Clusters should be empty, got %d items", len(state.OSKSources.Clusters))
	}
}

func TestNewStateFrom_PreservesExistingOSKData(t *testing.T) {
	// Create state with OSK data
	existingState := &State{
		OSKSources: &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{
				{ID: "test-cluster"},
			},
		},
	}

	newState := NewStateFrom(existingState)
	if newState.OSKSources == nil {
		t.Fatal("OSKSources should be preserved")
	}
	if len(newState.OSKSources.Clusters) != 1 {
		t.Errorf("Expected 1 OSK cluster, got %d", len(newState.OSKSources.Clusters))
	}
	if newState.MSKSources == nil {
		t.Error("MSKSources should be initialized even when copying OSK data")
	}
}

func TestGetOSKClusterByID_Found(t *testing.T) {
	state := &State{
		OSKSources: &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{
				{
					ID:               "my-kafka",
					BootstrapServers: []string{"broker1:9092", "broker2:9092"},
					KafkaAdminClientInformation: KafkaAdminClientInformation{
						ClusterID: "abc-123",
					},
				},
			},
		},
	}

	cluster, err := state.GetOSKClusterByID("my-kafka")
	if err != nil {
		t.Fatalf("GetOSKClusterByID() error = %v, want nil", err)
	}
	if cluster.ID != "my-kafka" {
		t.Errorf("GetOSKClusterByID() ID = %q, want %q", cluster.ID, "my-kafka")
	}
	if cluster.KafkaAdminClientInformation.ClusterID != "abc-123" {
		t.Errorf("GetOSKClusterByID() ClusterID = %q, want %q", cluster.KafkaAdminClientInformation.ClusterID, "abc-123")
	}
	if len(cluster.BootstrapServers) != 2 {
		t.Errorf("GetOSKClusterByID() BootstrapServers length = %d, want 2", len(cluster.BootstrapServers))
	}
	if len(cluster.BootstrapServers) >= 1 && cluster.BootstrapServers[0] != "broker1:9092" {
		t.Errorf("GetOSKClusterByID() BootstrapServers[0] = %q, want %q", cluster.BootstrapServers[0], "broker1:9092")
	}
	if len(cluster.BootstrapServers) >= 2 && cluster.BootstrapServers[1] != "broker2:9092" {
		t.Errorf("GetOSKClusterByID() BootstrapServers[1] = %q, want %q", cluster.BootstrapServers[1], "broker2:9092")
	}
}

func TestGetOSKClusterByID_NotFound(t *testing.T) {
	state := &State{
		OSKSources: &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{
				{ID: "my-kafka"},
			},
		},
	}

	_, err := state.GetOSKClusterByID("nonexistent")
	if err == nil {
		t.Error("GetOSKClusterByID() error = nil, want error")
	}
	if err != nil && !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("GetOSKClusterByID() error should contain 'nonexistent', got: %v", err)
	}
}

func TestGetOSKClusterByID_NilOSKSources(t *testing.T) {
	state := &State{}

	_, err := state.GetOSKClusterByID("my-kafka")
	if err == nil {
		t.Error("GetOSKClusterByID() error = nil, want error")
	}
}

func TestSchemaRegistriesState_UpsertConfluentSchemaRegistry(t *testing.T) {
	s := &SchemaRegistriesState{}

	s.UpsertConfluentSchemaRegistry(SchemaRegistryInformation{URL: "http://sr1:8081", Type: "confluent"})
	if len(s.ConfluentSchemaRegistry) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(s.ConfluentSchemaRegistry))
	}

	s.UpsertConfluentSchemaRegistry(SchemaRegistryInformation{URL: "http://sr2:8081", Type: "confluent"})
	if len(s.ConfluentSchemaRegistry) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(s.ConfluentSchemaRegistry))
	}

	s.UpsertConfluentSchemaRegistry(SchemaRegistryInformation{URL: "http://sr1:8081", Type: "confluent", Contexts: []string{"updated"}})
	if len(s.ConfluentSchemaRegistry) != 2 {
		t.Fatalf("expected 2 entries after upsert, got %d", len(s.ConfluentSchemaRegistry))
	}
	if len(s.ConfluentSchemaRegistry[0].Contexts) != 1 || s.ConfluentSchemaRegistry[0].Contexts[0] != "updated" {
		t.Errorf("expected updated contexts, got %v", s.ConfluentSchemaRegistry[0].Contexts)
	}
}

func TestSchemaRegistriesState_UpsertGlueSchemaRegistry(t *testing.T) {
	s := &SchemaRegistriesState{}

	s.UpsertGlueSchemaRegistry(GlueSchemaRegistryInformation{RegistryName: "reg1", Region: "us-east-1", RegistryArn: "arn1"})
	if len(s.AWSGlue) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(s.AWSGlue))
	}

	s.UpsertGlueSchemaRegistry(GlueSchemaRegistryInformation{RegistryName: "reg1", Region: "eu-west-1", RegistryArn: "arn2"})
	if len(s.AWSGlue) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(s.AWSGlue))
	}

	s.UpsertGlueSchemaRegistry(GlueSchemaRegistryInformation{RegistryName: "reg1", Region: "us-east-1", RegistryArn: "arn1-updated"})
	if len(s.AWSGlue) != 2 {
		t.Fatalf("expected 2 entries after upsert, got %d", len(s.AWSGlue))
	}
	if s.AWSGlue[0].RegistryArn != "arn1-updated" {
		t.Errorf("expected updated ARN, got %q", s.AWSGlue[0].RegistryArn)
	}
}

func TestNewStateFromFile_VersionMatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := &State{KcpBuildInfo: KcpBuildInfo{Version: build_info.Version}}
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	loaded, err := NewStateFromFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if loaded.KcpBuildInfo.Version != build_info.Version {
		t.Errorf("expected version %q, got %q", build_info.Version, loaded.KcpBuildInfo.Version)
	}
}

func TestNewStateFromFile_VersionMismatch_SucceedsWhenDeserialisable(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"older version", "0.5.0"},
		{"newer version", "2.0.0"},
		{"empty version", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			state := &State{KcpBuildInfo: KcpBuildInfo{Version: tt.version}}
			if err := state.WriteToFile(tmpFile.Name()); err != nil {
				t.Fatalf("failed to write state file: %v", err)
			}

			loaded, err := NewStateFromFile(tmpFile.Name())
			if err != nil {
				t.Fatalf("expected success for deseralisable state file with version mismatch, got error: %v", err)
			}
			if loaded.KcpBuildInfo.Version != tt.version {
				t.Errorf("expected version %q, got %q", tt.version, loaded.KcpBuildInfo.Version)
			}
		})
	}
}

func TestNewStateFromFile_FileNotFound(t *testing.T) {
	_, err := NewStateFromFile("/nonexistent/path/state.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read state file") {
		t.Errorf("expected file read error, got: %v", err)
	}
}

func TestNewStateFromFile_SchemaMismatch_SurfacesVersionError(t *testing.T) {
	// Simulate a state file from a different KCP version where a field's type
	// changed (e.g. msk_sources was a plain array, now an object). The full
	// unmarshal will fail but the version should still be surfaced.
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Valid JSON with a readable version but msk_sources as an array (type mismatch
	// against the current struct which expects an object)
	brokenSchema := `{"kcp_build_info":{"version":"0.5.0"},"msk_sources":["unexpected","array"]}`
	if _, err := tmpFile.WriteString(brokenSchema); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpFile.Close()

	_, err = NewStateFromFile(tmpFile.Name())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Errorf("expected actionable recreate guidance, got: %v", err)
	}
	// The source file's version is surfaced via the "migrated from ..." breadcrumb.
	if !strings.Contains(err.Error(), "0.5.0") {
		t.Errorf("expected error to reference the file's version, got: %v", err)
	}
}

func TestNewStateFromFile_InvalidJSON(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString("not valid json {{{"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpFile.Close()

	_, err = NewStateFromFile(tmpFile.Name())
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "state file") {
		t.Errorf("expected a state-file error, got: %v", err)
	}
}

func TestNewStateFromFile_SchemaMismatch_NoVersion_SurfacesFriendlyError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Valid JSON with no version stamp but a type mismatch that will fail deserialisation
	brokenSchema := `{"msk_sources":["unexpected","array"]}`
	if _, err := tmpFile.WriteString(brokenSchema); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpFile.Close()

	_, err = NewStateFromFile(tmpFile.Name())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Errorf("expected actionable recreate guidance for versionless schema mismatch, got: %v", err)
	}
}

func TestNewStateFromFile_EmptyVersion_Succeeds(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := &State{KcpBuildInfo: KcpBuildInfo{Version: ""}}
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	loaded, err := NewStateFromFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("expected success for versionless state file, got error: %v", err)
	}
	if loaded.KcpBuildInfo.Version != "" {
		t.Errorf("expected empty version to be preserved, got: %s", loaded.KcpBuildInfo.Version)
	}
}

func TestNewStateFromBytes_ValidJSON(t *testing.T) {
	data := []byte(`{"kcp_build_info":{"version":"` + build_info.Version + `"}}`)
	state, err := NewStateFromBytes(data)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if state.KcpBuildInfo.Version != build_info.Version {
		t.Errorf("expected version %q, got %q", build_info.Version, state.KcpBuildInfo.Version)
	}
}

func TestNewStateFromBytes_SchemaMismatch_WithVersion(t *testing.T) {
	data := []byte(`{"kcp_build_info":{"version":"0.5.0"},"msk_sources":["unexpected","array"]}`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The file's version is surfaced via the "migrated from ..." breadcrumb. The new
	// contract no longer echoes the running binary's version (superseded #308 behavior);
	// it gives actionable upgrade/recreate guidance instead.
	if !strings.Contains(err.Error(), "0.5.0") {
		t.Errorf("expected error to reference the file's version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Errorf("expected actionable recreate guidance, got: %v", err)
	}
}

func TestNewStateFromBytes_SchemaMismatch_NoVersion(t *testing.T) {
	// An Era C-shaped file (msk_sources present) whose msk_sources is malformed passes
	// through migration unchanged (era C, no schema_version) and fails the strict decode.
	// With no version stamp it is NOT dev-classified, so it gets the generic, actionable
	// recreate/upgrade message rather than a dev-build message (spec N5).
	data := []byte(`{"msk_sources":["unexpected","array"]}`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Errorf("expected actionable recreate guidance, got: %v", err)
	}
}

func TestNewStateFromBytes_InvalidJSON(t *testing.T) {
	data := []byte(`not valid json {{{`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	// Invalid JSON fails during version inspection; the message names the state file.
	if !strings.Contains(err.Error(), "state file") {
		t.Errorf("expected a state-file error, got: %v", err)
	}
}

func TestNewStateFromBytes_EmptyVersion(t *testing.T) {
	data := []byte(`{"kcp_build_info":{"version":""}}`)
	state, err := NewStateFromBytes(data)
	if err != nil {
		t.Fatalf("expected success for empty version, got error: %v", err)
	}
	if state.KcpBuildInfo.Version != "" {
		t.Errorf("expected empty version, got: %s", state.KcpBuildInfo.Version)
	}
}

// A legacy Era B file that the engine can't fully migrate (here, an unknown field after the
// B→C reshape) must fail with actionable, NON-CIRCULAR guidance: recreate with discover/scan.
// It must NOT advise `kcp state upgrade` — that command shares this exact loader, so it would
// fail identically (circular advice). Guards the fix for the schema_registries-style load failure.
func assertNonCircularLoadError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error for unsupported legacy file, got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, "kcp state upgrade") {
		t.Errorf("error must NOT advise `kcp state upgrade` (circular — same loader), got: %v", err)
	}
	if !strings.Contains(msg, "recreate") {
		t.Errorf("expected actionable recreate guidance, got: %v", err)
	}
}

func TestNewStateFromBytes_LegacyEraB_WithVersion_NonCircularAdvice(t *testing.T) {
	data := []byte(`{"kcp_build_info":{"version":"0.7.2"},"regions":[{"region_name":"us-east-1"}]}`)
	_, err := NewStateFromBytes(data)
	assertNonCircularLoadError(t, err)
}

func TestNewStateFromBytes_LegacyEraB_NoVersion_NonCircularAdvice(t *testing.T) {
	data := []byte(`{"regions":[{"region_name":"us-east-1"}]}`)
	_, err := NewStateFromBytes(data)
	assertNonCircularLoadError(t, err)
}

func TestNewStateFromBytes_UnknownFields_AnyExtraField_Rejects(t *testing.T) {
	data := []byte(`{"kcp_build_info":{"version":"` + build_info.Version + `"},"unexpected_field":"value"}`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestUpsertTargetedClusters(t *testing.T) {
	clusterWithAcls := DiscoveredCluster{
		Arn:    "arn:aws:kafka:us-east-1:111:cluster/a/uuid",
		Region: "us-east-1",
		KafkaAdminClientInformation: KafkaAdminClientInformation{
			Acls: []Acls{{ResourceName: "topic-a", Principal: "User:alice"}},
		},
	}
	siblingCluster := DiscoveredCluster{
		Arn:    "arn:aws:kafka:us-east-1:111:cluster/b/uuid",
		Region: "us-east-1",
	}

	t.Run("creates region when absent", func(t *testing.T) {
		s := &State{MSKSources: &MSKSourcesState{Regions: []DiscoveredRegion{}}}
		s.UpsertTargetedClusters(DiscoveredRegion{
			Name:     "us-east-1",
			Clusters: []DiscoveredCluster{clusterWithAcls},
		})
		if len(s.MSKSources.Regions) != 1 {
			t.Fatalf("got %d regions, want 1", len(s.MSKSources.Regions))
		}
		if len(s.MSKSources.Regions[0].Clusters) != 1 {
			t.Fatalf("got %d clusters, want 1", len(s.MSKSources.Regions[0].Clusters))
		}
	})

	t.Run("preserves siblings and refreshes region costs", func(t *testing.T) {
		s := &State{MSKSources: &MSKSourcesState{Regions: []DiscoveredRegion{{
			Name:     "us-east-1",
			Costs:    CostInformation{},
			Clusters: []DiscoveredCluster{clusterWithAcls, siblingCluster},
		}}}}

		// targeted re-discovery of cluster A with fresh (empty-admin-info) data + new region costs
		s.UpsertTargetedClusters(DiscoveredRegion{
			Name:  "us-east-1",
			Costs: CostInformation{CostResults: make([]costexplorertypes.ResultByTime, 1)},
			Clusters: []DiscoveredCluster{{
				Arn:    "arn:aws:kafka:us-east-1:111:cluster/a/uuid",
				Region: "us-east-1",
			}},
		})

		region := s.MSKSources.Regions[0]
		if len(region.Clusters) != 2 {
			t.Fatalf("got %d clusters, want 2 (sibling preserved)", len(region.Clusters))
		}
		if len(region.Costs.CostResults) != 1 {
			t.Errorf("region costs not refreshed: got %d results, want 1", len(region.Costs.CostResults))
		}

		// targeted cluster keeps its scan-acquired ACLs (MergeFrom preserves old when new empty)
		var clusterA *DiscoveredCluster
		for i := range region.Clusters {
			if region.Clusters[i].Arn == "arn:aws:kafka:us-east-1:111:cluster/a/uuid" {
				clusterA = &region.Clusters[i]
			}
		}
		if clusterA == nil {
			t.Fatal("targeted cluster A missing")
		}
		if len(clusterA.KafkaAdminClientInformation.Acls) != 1 {
			t.Errorf("scan ACLs not preserved on targeted cluster: got %d, want 1", len(clusterA.KafkaAdminClientInformation.Acls))
		}
	})

	t.Run("adds a new cluster to an existing region", func(t *testing.T) {
		s := &State{MSKSources: &MSKSourcesState{Regions: []DiscoveredRegion{{
			Name:     "us-east-1",
			Clusters: []DiscoveredCluster{clusterWithAcls, siblingCluster}, // A, B
		}}}}

		newCluster := DiscoveredCluster{
			Arn:    "arn:aws:kafka:us-east-1:111:cluster/c/uuid",
			Region: "us-east-1",
		}
		s.UpsertTargetedClusters(DiscoveredRegion{
			Name:     "us-east-1",
			Clusters: []DiscoveredCluster{newCluster},
		})

		region := s.MSKSources.Regions[0]
		if len(region.Clusters) != 3 {
			t.Fatalf("got %d clusters, want 3 (A, B preserved + new C appended)", len(region.Clusters))
		}
		found := false
		for _, c := range region.Clusters {
			if c.Arn == "arn:aws:kafka:us-east-1:111:cluster/c/uuid" {
				found = true
			}
		}
		if !found {
			t.Error("new cluster C was not appended to the existing region")
		}
	})
}

func TestFormatQueryDuration(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"seconds only", 30 * time.Second, "30s"},
		{"minutes only", 5 * time.Minute, "5m"},
		{"minutes and seconds", 5*time.Minute + 30*time.Second, "5m30s"},
		{"hours only", 2 * time.Hour, "2h"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"exactly 24h", 24 * time.Hour, "1d"},
		{"1 day 2 hours", 26 * time.Hour, "1d2h"},
		{"7 days", 7 * 24 * time.Hour, "7d"},
		{"7 days 2 hours", 7*24*time.Hour + 2*time.Hour, "7d2h"},
		{"30 days", 30 * 24 * time.Hour, "30d"},
		{"170 hours", 170 * time.Hour, "7d2h"},
		{"1 day 30 minutes", 24*time.Hour + 30*time.Minute, "1d30m"},
		{"2 days 3 hours 15 minutes", 2*24*time.Hour + 3*time.Hour + 15*time.Minute, "2d3h15m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatQueryDuration(tt.d)
			if got != tt.expected {
				t.Errorf("FormatQueryDuration(%v) = %q, want %q", tt.d, got, tt.expected)
			}
		})
	}
}

// skipIfWindows skips file-mode assertions on Windows, where POSIX permission
// bits are not meaningfully enforced.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode semantics do not apply on Windows")
	}
}

// TestWriteToFile_NewFileHasOwnerOnlyPerms verifies a freshly written state
// file is created with mode 0600 (owner read/write only), not world/group
// readable. (R1)
func TestWriteToFile_NewFileHasOwnerOnlyPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), "kcp-state.json")
	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perms = %#o, want 0600", got)
	}
}

// TestWriteToFile_StaleLooseTempDoesNotLeak guards against regressing to the old
// fixed-name temp scheme, whose bug was that a leftover <path>.tmp at 0644 from a
// crashed run kept its loose mode and the rename carried it through. We seed that
// exact condition and assert (a) the final state file is still 0600, (b) the
// stale fixed-name temp is left untouched -- proving the writer created its own
// unique temp rather than reusing the leftover one -- and (c) the writer's own
// unique temp is cleaned up on success. (R3 abuse case)
func TestWriteToFile_StaleLooseTempDoesNotLeak(t *testing.T) {
	skipIfWindows(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")
	// Simulate a crash leaving a fixed-name temp at loose perms -- the exact
	// condition the old os.WriteFile(path+".tmp", ...) code mishandled.
	stale := path + ".tmp"
	if err := os.WriteFile(stale, []byte("{}"), 0644); err != nil {
		t.Fatalf("seed stale temp: %v", err)
	}

	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perms = %#o, want 0600 (stale 0644 temp leaked into result)", got)
	}

	// The writer must not reuse the stale fixed-name temp: it should still exist,
	// untouched at 0644, proving a fresh unique temp was used instead.
	staleInfo, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("stale fixed-name temp should be left untouched, but stat failed: %v", err)
	}
	if got := staleInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("stale fixed-name temp perms = %#o, want 0644 untouched (writer must not reuse the fixed name)", got)
	}

	// The writer's own unique temp (.kcp-state.json.tmp-*) must be cleaned up on success.
	matches, err := filepath.Glob(filepath.Join(dir, ".kcp-state.json.tmp-*"))
	if err != nil {
		t.Fatalf("glob unique temp: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("unique temp file(s) left behind after success: %v", matches)
	}
}

// TestWriteToFile_SecondWritePreservesPerms verifies a rewrite of an existing
// state file keeps mode 0600 rather than loosening it back to 0644 -- so the
// hardening holds across every command that persists state, not just the first.
// (R4 regression guard)
func TestWriteToFile_SecondWritePreservesPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), "kcp-state.json")
	for i := 0; i < 2; i++ {
		if err := (&State{}).WriteToFile(path); err != nil {
			t.Fatalf("WriteToFile (write %d): %v", i+1, err)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perms after second write = %#o, want 0600", got)
	}
}

// TestWriteToFile_TightensExistingLooseFile verifies that an existing state
// file already at 0644 (e.g. created by an older kcp build) is tightened to
// 0600 on the next write -- no separate migration step required. (R5 regression
// guard)
func TestWriteToFile_TightensExistingLooseFile(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), "kcp-state.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatalf("seed legacy 0644 state file: %v", err)
	}

	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perms = %#o, want 0600 (existing 0644 file not tightened)", got)
	}
}

// TestWriteToFile_RoundTripsContent verifies the permission change did not break
// the write/load round trip -- content is still valid JSON and reloads via the
// existing loader. (R6 regression guard)
func TestWriteToFile_RoundTripsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kcp-state.json")
	want := "round-trip-test-1.2.3"
	if err := (&State{KcpBuildInfo: KcpBuildInfo{Version: want}}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	got, err := NewStateFromFile(path)
	if err != nil {
		t.Fatalf("NewStateFromFile: %v", err)
	}
	if got.KcpBuildInfo.Version != want {
		t.Fatalf("reloaded version = %q, want %q", got.KcpBuildInfo.Version, want)
	}
}

func TestWriteToFileStampsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")

	s := &State{} // no schema_version set by caller
	if err := s.WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var probe struct {
		SchemaVersion int    `json:"schema_version"`
		UpdatedAt     string `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if probe.SchemaVersion != migrate.CurrentSchemaVersion {
		t.Errorf("schema_version = %d, want %d", probe.SchemaVersion, migrate.CurrentSchemaVersion)
	}
	if probe.UpdatedAt == "" {
		t.Errorf("updated_at not stamped")
	}
}

func TestWriteToFileBacksUpMigratingWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")

	// Existing on-disk file at an OLDER schema_version (0) than current.
	if err := os.WriteFile(path, []byte(`{"schema_version":0,"msk_sources":{"regions":[]},"kcp_build_info":{"version":"0.8.0"},"timestamp":"2026-05-14T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	// Exactly one timestamped .bak should exist, holding the old (schema_version 0) bytes.
	entries, _ := filepath.Glob(filepath.Join(dir, "kcp-state.json.*.bak"))
	if len(entries) != 1 {
		t.Fatalf("want 1 .bak, got %d: %v", len(entries), entries)
	}
	bak, _ := os.ReadFile(entries[0])
	if !strings.Contains(string(bak), `"schema_version":0`) {
		t.Errorf(".bak should contain the pre-migration file, got: %s", bak)
	}

	// A same-version rewrite must NOT create another backup.
	if err := (&State{}).WriteToFile(path); err != nil {
		t.Fatal(err)
	}
	entries, _ = filepath.Glob(filepath.Join(dir, "kcp-state.json.*.bak"))
	if len(entries) != 1 {
		t.Errorf("same-version rewrite must not back up; want 1 .bak, got %d", len(entries))
	}
}

func TestNewStateFromBytesLoadsCurrentEraC(t *testing.T) {
	data := []byte(`{"schema_version":1,"msk_sources":{"regions":[]},"kcp_build_info":{"version":"0.8.5","commit":"x","date":"y"},"timestamp":"2026-01-01T00:00:00Z"}`)
	st, err := NewStateFromBytes(data)
	if err != nil {
		t.Fatalf("NewStateFromBytes: %v", err)
	}
	if st.MSKSources == nil {
		t.Fatalf("msk_sources not decoded")
	}
}

func TestNewStateFromBytesNewerSchemaIsActionable(t *testing.T) {
	data := []byte(`{"schema_version":99,"msk_sources":{}}`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error for newer schema")
	}
	msg := err.Error()
	if !strings.Contains(msg, "newer") || !strings.Contains(msg, "kcp update") {
		t.Errorf("error should tell the user to update, got: %q", msg)
	}
	if strings.Contains(msg, "recreate") || strings.Contains(msg, "downgrade") {
		t.Errorf("error must NOT suggest recreate/downgrade for a newer file, got: %q", msg)
	}
}

func TestNewStateFromBytesDevStampedNewerSchema(t *testing.T) {
	// Official-release reader opening a dev-STAMPED file with a newer schema_version:
	// must explain it's a dev build, NOT advise `kcp update` (spec §6.9).
	data := []byte(`{"schema_version":99,"kcp_build_info":{"version":"0.0.0-localdev"},"msk_sources":{}}`)
	_, err := NewStateFromBytes(data)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "development build") {
		t.Errorf("error should name the development build, got: %q", msg)
	}
	if strings.Contains(msg, "kcp update") {
		t.Errorf("error must NOT advise `kcp update` for a dev-stamped file, got: %q", msg)
	}
}

func TestWriteToFileFailsWhenExistingFileUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — 0000 perms do not block reads")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")
	// Existing file at an older schema_version (so the next write is a *migrating* write),
	// then made unreadable. The write must abort rather than silently overwrite with no backup.
	if err := os.WriteFile(path, []byte(`{"schema_version":0,"kcp_build_info":{"version":"0.8.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) }) // let TempDir cleanup remove it

	if err := (&State{}).WriteToFile(path); err == nil {
		t.Fatal("WriteToFile must fail when the existing file can't be read (no silent overwrite without backup)")
	}
}

func TestNewStateFromBytes_TrailingDataRejected(t *testing.T) {
	data := []byte(`{"schema_version":1,"kcp_build_info":{"version":"0.8.5"},"msk_sources":{}}{"extra":true}`)
	if _, err := NewStateFromBytes(data); err == nil {
		t.Fatal("expected error for trailing data after the JSON object")
	}
}
