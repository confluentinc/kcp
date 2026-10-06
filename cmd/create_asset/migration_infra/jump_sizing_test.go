package migration_infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scan with no broker storage (or instance type) info must produce a clear error for
// the jump-cluster types rather than a nil dereference.
func TestParseMSKMigrationInfraOpts_ProvisionedScanWithoutBrokerInfo(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *types.DiscoveredCluster)
		set    func()
		want   string
	}{
		{
			name: "no storage info",
			mutate: func(c *types.DiscoveredCluster) {
				c.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo.StorageInfo = nil
			},
			set:  func() { jumpClusterInstanceType = "m5.large" },
			want: "--jump-cluster-broker-storage",
		},
		{
			name: "no broker node group",
			mutate: func(c *types.DiscoveredCluster) {
				c.AWSClientInformation.MskClusterConfig.Provisioned.BrokerNodeGroupInfo = nil
			},
			set:  func() { jumpClusterBrokerStorage = 100 },
			want: "--jump-cluster-instance-type",
		},
	}
	for _, typ := range []string{"5"} {
		for _, tc := range cases {
			t.Run("type "+typ+" "+tc.name, func(t *testing.T) {
				cluster := buildProvisionedTestCluster()
				tc.mutate(&cluster)
				state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{{Name: "us-east-1", Clusters: []types.DiscoveredCluster{cluster}}}}}
				data, err := json.Marshal(state)
				require.NoError(t, err)
				path := filepath.Join(t.TempDir(), "kcp-state.json")
				require.NoError(t, os.WriteFile(path, data, 0644))

				resetMigrationInfraFlags(t)
				stateFile = path
				clusterId = provisionedClusterArn
				migrationInfraType = typ
				jumpClusterBrokerSubnetCidr = mustParseCIDRs(t, "10.0.54.0/24", "10.0.55.0/24", "10.0.56.0/24")
				tc.set()

				_, err = parseMSKMigrationInfraOpts()

				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want)
			})
		}
	}
}
