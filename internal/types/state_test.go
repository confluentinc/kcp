package types

import (
	"os"
	"strings"
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
			result := NewStateFrom(tt.fromState)

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
				Regions: []DiscoveredRegion{},
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
				Regions: []DiscoveredRegion{
					{Name: "us-east-1", Clusters: []DiscoveredCluster{}},
					{Name: "eu-west-1", Clusters: []DiscoveredCluster{}},
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
				"kcp report metrics --state-file /path/to/state.json --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/cluster-1/abc123 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report metrics --state-file /path/to/state.json --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/cluster-2/def456 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
				"kcp report metrics --state-file /path/to/state.json --cluster-arn arn:aws:kafka:eu-west-1:123456789012:cluster/cluster-3/ghi789 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
			},
			wantError: false,
		},
		{
			name: "state with single region and single cluster",
			state: &State{
				Regions: []DiscoveredRegion{
					{
						Name: "ap-south-1",
						Clusters: []DiscoveredCluster{
							{Name: "my-cluster", Arn: "arn:aws:kafka:ap-south-1:123456789012:cluster/my-cluster/xyz789"},
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
				"kcp report metrics --state-file ./kcp-state.json --cluster-arn arn:aws:kafka:ap-south-1:123456789012:cluster/my-cluster/xyz789 --start <YYYY-MM-DD> --end <YYYY-MM-DD>",
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
			tmpFile.Close()
			defer os.Remove(tmpFilePath)

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
		Regions: []DiscoveredRegion{
			{Name: "us-east-1", Clusters: []DiscoveredCluster{}},
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
