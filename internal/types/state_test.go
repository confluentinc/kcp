package types

import (
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/build_info"
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

func TestProcessedSource_TypeDiscrimination(t *testing.T) {
	// Test MSK source
	mskSource := ProcessedSource{
		Type: SourceTypeMSK,
		MSKData: &ProcessedMSKSource{
			Regions: []ProcessedRegion{},
		},
	}
	if mskSource.Type != SourceTypeMSK {
		t.Errorf("Expected MSK type, got %s", mskSource.Type)
	}
	if mskSource.MSKData == nil {
		t.Error("MSKData should not be nil for MSK source")
	}

	// Test OSK source
	oskSource := ProcessedSource{
		Type: SourceTypeOSK,
		OSKData: &ProcessedOSKSource{
			Clusters: []ProcessedOSKCluster{},
		},
	}
	if oskSource.Type != SourceTypeOSK {
		t.Errorf("Expected OSK type, got %s", oskSource.Type)
	}
	if oskSource.OSKData == nil {
		t.Error("OSKData should not be nil for OSK source")
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
	defer os.Remove(tmpFile.Name())

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

func TestNewStateFromFile_VersionMismatch(t *testing.T) {
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
			defer os.Remove(tmpFile.Name())

			state := &State{KcpBuildInfo: KcpBuildInfo{Version: tt.version}}
			if err := state.WriteToFile(tmpFile.Name()); err != nil {
				t.Fatalf("failed to write state file: %v", err)
			}

			_, err = NewStateFromFile(tmpFile.Name())
			if err == nil {
				t.Fatal("expected version mismatch error, got nil")
			}
			if !strings.Contains(err.Error(), "state file version mismatch") {
				t.Errorf("expected version mismatch error, got: %v", err)
			}
			if !strings.Contains(err.Error(), tt.version) {
				t.Errorf("expected error to contain file version %q, got: %v", tt.version, err)
			}
			if !strings.Contains(err.Error(), build_info.Version) {
				t.Errorf("expected error to contain running version %q, got: %v", build_info.Version, err)
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

func TestNewStateFromFile_InvalidJSON(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString("not valid json {{{"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	_, err = NewStateFromFile(tmpFile.Name())
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal state") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}
