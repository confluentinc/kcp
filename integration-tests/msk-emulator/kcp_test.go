//go:build integration

package mskemulator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runKCP execs the repo-root kcp binary in dir and fails on a non-zero exit or on
// any ERROR line in kcp.log: kcp logs most per-cluster and per-region failures
// and still exits 0, so the exit code alone would pass on lost data.
func runKCP(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(), "kcp"), args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "kcp %s:\n%s", strings.Join(args, " "), out)
	assertNoErrorLines(t, dir)
}

func assertNoErrorLines(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kcp.log"))
	require.NoError(t, err)
	var errs []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, " ERROR ") {
			errs = append(errs, line)
		}
	}
	assert.Empty(t, errs, "kcp.log has ERROR lines")
}

func loadState(t *testing.T, dir string) types.State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kcp-state.json"))
	require.NoError(t, err)
	var st types.State
	require.NoError(t, json.Unmarshal(data, &st))
	return st
}

// loadCreds decodes msk-credentials.yaml directly: types.NewCredentialsFromFile
// validates, and rejects the SCRAM entries discover writes with empty credentials.
func loadCreds(t *testing.T, dir string) types.Credentials {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "msk-credentials.yaml"))
	require.NoError(t, err)
	var c types.Credentials
	require.NoError(t, yaml.Unmarshal(data, &c))
	return c
}

func findRegion(t *testing.T, st types.State, region string) types.DiscoveredRegion {
	t.Helper()
	require.NotNil(t, st.MSKSources, "state has no msk_sources")
	var names []string
	for _, r := range st.MSKSources.Regions {
		if r.Name == region {
			return r
		}
		names = append(names, r.Name)
	}
	require.FailNowf(t, "region not in state", "%s not found; state has %v", region, names)
	return types.DiscoveredRegion{}
}

func findCluster(t *testing.T, st types.State, region, name string) types.DiscoveredCluster {
	t.Helper()
	r := findRegion(t, st, region)
	for _, c := range r.Clusters {
		if c.Name == name {
			return c
		}
	}
	require.FailNowf(t, "cluster not in state", "%s not found in %s; region has %v", name, region, clusterNames(r))
	return types.DiscoveredCluster{}
}

func findCredCluster(t *testing.T, c types.Credentials, region, name string) types.ClusterAuth {
	t.Helper()
	for _, r := range c.Regions {
		if r.Name != region {
			continue
		}
		for _, cl := range r.Clusters {
			if cl.Name == name {
				return cl
			}
		}
	}
	require.FailNowf(t, "cluster not in credentials", "%s not found in %s of msk-credentials.yaml", name, region)
	return types.ClusterAuth{}
}

func clusterNames(r types.DiscoveredRegion) []string {
	names := make([]string, 0, len(r.Clusters))
	for _, c := range r.Clusters {
		names = append(names, c.Name)
	}
	return names
}
