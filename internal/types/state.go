package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
)

// State represents the unified state file (kcp-state.json)
type State struct {
	MSKSources       *MSKSourcesState       `json:"msk_sources,omitempty"`
	OSKSources       *OSKSourcesState       `json:"osk_sources,omitempty"`
	SchemaRegistries *SchemaRegistriesState `json:"schema_registries,omitempty"`
	KcpBuildInfo     KcpBuildInfo           `json:"kcp_build_info"`
	Timestamp        time.Time              `json:"timestamp"`
}

func NewStateFrom(fromState *State) *State {
	// Always create with fresh metadata for the current discovery run
	workingState := &State{
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}

	if fromState == nil {
		// Initialize both sources with empty arrays
		workingState.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
		workingState.OSKSources = &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{},
		}
	} else {
		// Copy existing MSK data or initialize empty
		if fromState.MSKSources != nil {
			mskSources := &MSKSourcesState{
				Regions: make([]DiscoveredRegion, len(fromState.MSKSources.Regions)),
			}
			copy(mskSources.Regions, fromState.MSKSources.Regions)
			workingState.MSKSources = mskSources
		} else {
			workingState.MSKSources = &MSKSourcesState{
				Regions: []DiscoveredRegion{},
			}
		}

		// Copy existing OSK data or initialize empty
		if fromState.OSKSources != nil {
			oskSources := &OSKSourcesState{
				Clusters: make([]OSKDiscoveredCluster, len(fromState.OSKSources.Clusters)),
			}
			copy(oskSources.Clusters, fromState.OSKSources.Clusters)
			workingState.OSKSources = oskSources
		} else {
			workingState.OSKSources = &OSKSourcesState{
				Clusters: []OSKDiscoveredCluster{},
			}
		}
	}

	return workingState
}

func NewStateFromFile(stateFile string) (*State, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file %s: %v", stateFile, err)
	}
	return NewStateFromBytes(data)
}

func NewStateFromBytes(data []byte) (*State, error) {
	var state State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		// Decode failed — the schema may have changed between versions,
		// or the file contains unknown fields from a different KCP version.
		// Try to extract just the version from the raw bytes to give a more
		// actionable error than a raw JSON type error.
		var raw struct {
			KcpBuildInfo struct {
				Version string `json:"version"`
			} `json:"kcp_build_info"`
		}
		if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil {
			if raw.KcpBuildInfo.Version != "" && raw.KcpBuildInfo.Version != build_info.Version {
				return nil, fmt.Errorf("%v (file was created with KCP version %q, you are running %q). Please recreate the state file with kcp discover (MSK) or kcp scan clusters (Apache Kafka) using the latest KCP release, or use KCP version %s to load this file", err, raw.KcpBuildInfo.Version, build_info.Version, raw.KcpBuildInfo.Version)
			}
			return nil, fmt.Errorf("%v. Please recreate the state file with kcp discover (MSK) or kcp scan clusters (Apache Kafka) using the latest KCP release", err)
		}
		return nil, fmt.Errorf("%v. Please recreate the state file with kcp discover (MSK) or kcp scan clusters (Apache Kafka) using the latest KCP release", err)
	}

	if state.KcpBuildInfo.Version == "" {
		slog.Warn("state file has no kcp_build_info.version, this may not be a valid KCP state file")
	}

	return &state, nil
}

func (s *State) WriteToFile(filePath string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}

	// Write to temporary file first for atomic operation
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename (on most filesystems)
	if err := os.Rename(tmpFile, filePath); err != nil {
		os.Remove(tmpFile) // Clean up temp file
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (s *State) WriteReportCommands(filePath string, stateFilePath string) error {
	regionCommands := []string{"# Report region costs commands"}
	clusterCommands := []string{"# Report cluster metrics commands"}

	// Loop through regions
	if s.MSKSources != nil {
		for _, region := range s.MSKSources.Regions {
			// Output command for report costs for this region
			regionCommand := []string{fmt.Sprintf("# region: %s", region.Name)}
			regionCommand = append(regionCommand, fmt.Sprintf("kcp report costs --state-file %s --region %s --start <YYYY-MM-DD> --end <YYYY-MM-DD>\n", stateFilePath, region.Name))
			regionCommands = append(regionCommands, strings.Join(regionCommand, "\n"))

			// Loop through clusters in this region
			for _, cluster := range region.Clusters {
				clusterCommand := []string{fmt.Sprintf("# cluster: %s", cluster.Name)}
				clusterCommand = append(clusterCommand, fmt.Sprintf("kcp report metrics --state-file %s --cluster-id %s --start <YYYY-MM-DD> --end <YYYY-MM-DD>\n", stateFilePath, cluster.Arn))
				clusterCommands = append(clusterCommands, strings.Join(clusterCommand, "\n"))
			}
		}
	}

	// Combine all commands and write to file
	regionLines := strings.Join(regionCommands, "\n") + "\n"
	clusterLines := strings.Join(clusterCommands, "\n")
	allLines := regionLines + "\n" + clusterLines + "\n"

	err := os.WriteFile(filePath, []byte(allLines), 0644)
	if err != nil {
		return fmt.Errorf("failed to write commands to file: %v", err)
	}
	return nil
}

func (s *State) PersistStateFile(stateFile string) error {
	if s == nil {
		return fmt.Errorf("discovery state is nil")
	}

	return s.WriteToFile(stateFile)
}

func (s *State) UpsertRegion(newRegion DiscoveredRegion) {
	if s.MSKSources == nil {
		s.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
	}
	for i, existingRegion := range s.MSKSources.Regions {
		if existingRegion.Name == newRegion.Name {
			discoveredClusters := newRegion.Clusters
			newRegion.Clusters = existingRegion.Clusters
			// set discovered clusters and refresh into state (preserves KafkaAdminClientInformation)
			newRegion.RefreshClusters(discoveredClusters)
			s.MSKSources.Regions[i] = newRegion
			return
		}
	}
	s.MSKSources.Regions = append(s.MSKSources.Regions, newRegion)
}

func (s *State) UpsertDiscoveredClients(regionName string, clusterName string, discoveredClients []DiscoveredClient) error {
	slog.Info("🔍 looking for region and cluster in state file", "region", regionName, "cluster_name", clusterName)
	if s.MSKSources == nil {
		return fmt.Errorf("no MSK sources found in state file")
	}
	for i := range s.MSKSources.Regions {
		region := &s.MSKSources.Regions[i]
		if region.Name == regionName {
			for j := range region.Clusters {
				cluster := &region.Clusters[j]
				if cluster.Name == clusterName {
					// Merge existing clients from state with newly discovered clients
					cluster.DiscoveredClients = dedupDiscoveredClients(append(cluster.DiscoveredClients, discoveredClients...))
					return nil
				}
			}
		}
	}
	return fmt.Errorf("cluster '%s' not found in region '%s'", clusterName, regionName)
}

func dedupDiscoveredClients(discoveredClients []DiscoveredClient) []DiscoveredClient {
	// Deduplicate by composite key, keeping the client with the most recent timestamp
	clientsByCompositeKey := make(map[string]DiscoveredClient)

	for _, currentClient := range discoveredClients {
		existingClient, exists := clientsByCompositeKey[currentClient.CompositeKey]

		if !exists || currentClient.Timestamp.After(existingClient.Timestamp) {
			clientsByCompositeKey[currentClient.CompositeKey] = currentClient
		}
	}

	dedupedClients := make([]DiscoveredClient, 0, len(clientsByCompositeKey))
	for _, client := range clientsByCompositeKey {
		dedupedClients = append(dedupedClients, client)
	}

	return dedupedClients
}

func (s *State) GetClusterByArn(clusterArn string) (*DiscoveredCluster, error) {
	if s.MSKSources != nil {
		for i := range s.MSKSources.Regions {
			for j := range s.MSKSources.Regions[i].Clusters {
				if s.MSKSources.Regions[i].Clusters[j].Arn == clusterArn {
					return &s.MSKSources.Regions[i].Clusters[j], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cluster with ARN %s not found in state file", clusterArn)
}

// GetOSKClusterByID looks up an OSK cluster by the user-provided ID from credentials
func (s *State) GetOSKClusterByID(id string) (*OSKDiscoveredCluster, error) {
	if s.OSKSources == nil {
		return nil, fmt.Errorf("no Apache Kafka sources in state file")
	}

	for i := range s.OSKSources.Clusters {
		if s.OSKSources.Clusters[i].ID == id {
			return &s.OSKSources.Clusters[i], nil
		}
	}

	return nil, fmt.Errorf("no Apache Kafka cluster with ID '%s' found in state file", id)
}
