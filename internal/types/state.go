package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/state/migrate"
)

// State represents the unified state file (kcp-state.json)
type State struct {
	SchemaVersion    int                    `json:"schema_version"`
	MSKSources       *MSKSourcesState       `json:"msk_sources,omitempty"`
	OSKSources       *OSKSourcesState       `json:"osk_sources,omitempty"`
	SchemaRegistries *SchemaRegistriesState `json:"schema_registries,omitempty"`
	KcpBuildInfo     KcpBuildInfo           `json:"kcp_build_info"`
	Timestamp        time.Time              `json:"timestamp"`
	UpdatedAt        time.Time              `json:"updated_at,omitempty"`
	UpgradedFrom     string                 `json:"upgraded_from,omitempty"`
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

		// Carry forward data that isn't source-scoped so a RUW write (discover/scan)
		// doesn't silently drop it: the upgraded_from breadcrumb (durable provenance
		// of the file's origin shape) and any previously discovered schema registries
		// (discover does not repopulate these — dropping them violates append-only).
		workingState.UpgradedFrom = fromState.UpgradedFrom
		workingState.SchemaRegistries = fromState.SchemaRegistries

		// Timestamp is the created-at; only updated_at moves per write. Preserve the
		// original so re-running discover/scan doesn't reset creation time to now.
		// (Fall back to the fresh time.Now() above if the source has none.)
		if !fromState.Timestamp.IsZero() {
			workingState.Timestamp = fromState.Timestamp
		}
	}

	return workingState
}

func NewStateFromFile(stateFile string) (*State, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file %s: %v", stateFile, err)
	}
	slog.Debug("🔍 loading state file", "path", stateFile, "bytes", len(data))
	return NewStateFromBytes(data)
}

func NewStateFromBytes(data []byte) (*State, error) {
	// File-driven dev classification: inspect the FILE's own stamp, never the
	// reader binary's build_info.Version (spec §6.2/§6.9).
	fileIsDevStamped := fileBuildIsDev(data)

	migrated, fromLabel, err := migrate.Upgrade(data)
	if err != nil {
		switch {
		case errors.Is(err, migrate.ErrNewerSchemaDev):
			return nil, fmt.Errorf("%w. This state file was produced by a local/unreleased KCP build whose schema is ahead of this release; there may be no released version that reads it. Open it with the dev build that wrote it, or regenerate it", err)
		case errors.Is(err, migrate.ErrNewerSchema):
			return nil, fmt.Errorf("%w. This state file was written by a newer version of KCP than you are running. Run `kcp update` to upgrade, then retry", err)
		case errors.Is(err, migrate.ErrUnsupportedLegacy):
			return nil, fmt.Errorf("%w. This kcp build cannot read this format; recreate the file with `kcp discover` (MSK) or `kcp scan clusters` (Apache Kafka), or use a kcp release that supports it", err)
		default:
			return nil, err
		}
	}

	var state State
	decoder := json.NewDecoder(bytes.NewReader(migrated))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		if fileIsDevStamped {
			return nil, fmt.Errorf("state file was produced by a local/unreleased KCP build (%s) and its schema does not match any released format, so it cannot be migrated automatically: %w. Open it with the dev build that wrote it, or regenerate it", fromLabel, err)
		}
		return nil, fmt.Errorf("state file could not be loaded: its schema does not match this build of kcp (migrated from %s): %w. This kcp build does not support this file's format; recreate it with `kcp discover` (MSK) or `kcp scan clusters` (Apache Kafka), or use a kcp release that supports it", fromLabel, err)
	}

	// A dev-stamped file that decodes cleanly is a clean success — no warning
	// (spec §6.9: the dev stamp is provenance, not a defect). fileIsDevStamped only
	// shapes the FAILURE messages above.
	if state.UpgradedFrom == "" && fromLabel != fmt.Sprintf("schema_version=%d", migrate.CurrentSchemaVersion) {
		state.UpgradedFrom = fromLabel
	}
	if state.KcpBuildInfo.Version == "" {
		slog.Warn("⚠️ state file has no kcp_build_info.version, this may not be a valid KCP state file")
	}
	slog.Debug("loaded state file",
		"schema_version", state.SchemaVersion,
		"kcp_build_version", state.KcpBuildInfo.Version,
		"upgraded_from", state.UpgradedFrom,
	)
	return &state, nil
}

// fileBuildIsDev reports whether the state file's OWN kcp_build_info.version is a
// development sentinel. It reads only the file bytes (never the running binary).
// A MISSING version is treated as unknown provenance, NOT dev: only a file carrying
// an actual dev stamp (dev / 0.0.0-localdev) gets the dev-aware error wording, so
// region-scan files and unrelated JSON (which have no version) fall to the generic
// "recreate it with kcp discover / kcp scan clusters" message (spec N5).
func fileBuildIsDev(data []byte) bool {
	var probe struct {
		KcpBuildInfo struct {
			Version string `json:"version"`
		} `json:"kcp_build_info"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.KcpBuildInfo.Version != "" && build_info.IsDevVersion(probe.KcpBuildInfo.Version)
}

func (s *State) WriteToFile(filePath string) error {
	if err := backupIfMigrating(filePath); err != nil {
		return err
	}
	s.SchemaVersion = migrate.CurrentSchemaVersion
	s.UpdatedAt = time.Now()

	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to a uniquely-named temp file in the same directory, then atomically
	// rename it onto the target. os.CreateTemp creates the file with mode 0600,
	// and we pin it explicitly so the state file (which holds sensitive
	// infrastructure metadata) is never group/world readable, even briefly and
	// even under an unusual umask. The real file is only ever replaced by the
	// rename and is never deleted directly, so a crash before the rename leaves
	// the previous state file intact.
	tmpFile, err := os.CreateTemp(filepath.Dir(filePath), "."+filepath.Base(filePath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename (on most filesystems)
	if err := os.Rename(tmpName, filePath); err != nil {
		_ = os.Remove(tmpName) // Clean up temp file
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	slog.Debug("wrote state file", "path", filePath, "schema_version", s.SchemaVersion, "bytes", len(data))
	return nil
}

// backupIfMigrating copies an existing target to <path>.<UTC-timestamp>.bak when its
// on-disk schema_version differs from the current one (design D7). New files and same-version
// rewrites are not backed up. The timestamped name lets multiple upgrades coexist in a
// folder; a counter suffix guards the rare same-second collision.
func backupIfMigrating(filePath string) error {
	existing, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no existing file → nothing to back up
		}
		// The file exists but couldn't be read — do NOT silently overwrite it without a
		// backup; abort the migrating write so the original is preserved (design D7).
		return fmt.Errorf("failed to read existing state file before migrating write: %w", err)
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	_ = json.Unmarshal(existing, &probe) // absent/invalid → schema_version 0 (legacy) → back up
	if probe.SchemaVersion == migrate.CurrentSchemaVersion {
		return nil // same version → not a migrating write
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	bak := fmt.Sprintf("%s.%s.bak", filePath, ts)
	for i := 1; ; i++ {
		// Break on any non-nil Stat error: not-exist means the name is free to use, and any
		// other error means we can't stat it — let os.WriteFile below surface the real error
		// rather than spinning forever (a non-NotExist error would never satisfy IsNotExist).
		if _, err := os.Stat(bak); err != nil {
			break
		}
		bak = fmt.Sprintf("%s.%s-%d.bak", filePath, ts, i)
	}
	if err := os.WriteFile(bak, existing, 0600); err != nil {
		return fmt.Errorf("failed to back up state file before migrating write: %w", err)
	}
	slog.Debug("backed up state file before migrating write",
		"backup", bak,
		"from_schema_version", probe.SchemaVersion,
		"to_schema_version", migrate.CurrentSchemaVersion,
	)
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

// UpsertTargetedClusters refreshes region-level data (costs, configurations) and creates
// or replaces only the clusters present in newRegion.Clusters, preserving every other
// existing cluster in the region. Used by targeted (--cluster-arn) discovery. If the
// region does not yet exist it is added as-is (fresh-state / new-region case).
func (s *State) UpsertTargetedClusters(newRegion DiscoveredRegion) {
	if s.MSKSources == nil {
		s.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
	}
	for i := range s.MSKSources.Regions {
		if s.MSKSources.Regions[i].Name == newRegion.Name {
			// refresh region-level data discovered this run
			s.MSKSources.Regions[i].Configurations = newRegion.Configurations
			s.MSKSources.Regions[i].Costs = newRegion.Costs
			// create-or-replace only the targeted clusters
			for _, targeted := range newRegion.Clusters {
				s.MSKSources.Regions[i].UpsertCluster(targeted)
			}
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
