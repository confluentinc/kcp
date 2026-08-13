//go:build e2e

package migration_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeManifestToPod renders the GatewayMigration manifest for a scenario and
// writes it into the runner pod at podPath, mode 0600.
//
// The YAML travels over STDIN. Passing it as an argument would put the
// destination password into the container's process table and into the host
// kubectl command line; stdin puts it in neither. The mode and path-handling
// properties live in podWriteCommand, which is unit-tested without a cluster.
//
// Safe to call more than once for the same path: re-rendering is how a scenario
// varies execute-time policy between init and apply, which is legal because apply
// reads policy fresh and the drift check compares topology only.
func writeManifestToPod(t *testing.T, cfg envConfig, podPath string, opts manifestOpts) {
	t.Helper()

	rendered, err := renderGatewayMigration(opts)
	require.NoError(t, err, "rendering the GatewayMigration manifest for %s", podPath)

	// Redacted: test output is CI output.
	t.Logf("manifest written to %s:\n%s", podPath, manifestForLog(rendered))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	argv := podWriteCommand(cfg.KubeContext, cfg.Namespace, cfg.KCPPod, podPath)
	cmd := exec.CommandContext(ctx, "kubectl", argv...)
	cmd.Stdin = strings.NewReader(rendered)

	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "writing %s into the pod: %s", podPath, stderr.String())
}

// manifestOptsFor builds the manifest options every scenario shares, from the
// topology setup.sh published into .env. Callers set metadata.name, the manifest
// path, pause intent and policy on top.
func manifestOptsFor(cfg envConfig) manifestOpts {
	return manifestOpts{
		MetadataName:    "e2e-" + cfg.Scenario,
		SourceBootstrap: cfg.SourceBootstrap,
		DestBootstrap:   cfg.DestBootstrap,
		DestClusterID:   cfg.DestClusterID,
		RestEndpoint:    cfg.RestProxyEndpoint,
		ClusterLinkName: cfg.ClusterLinkName,
		APIKey:          cfg.ClusterAPIKey,
		APISecret:       cfg.ClusterAPISecret,
		Namespace:       cfg.Namespace,
		GatewayName:     cfg.GatewayName,
		FencedCR:        cfg.FencedCR,
		SwitchoverCR:    cfg.SwitchoverCR,
		KubePath:        cfg.KubePath,
	}
}

// manifestPathFor is the in-pod path for a scenario's manifest. Absolute because
// kcp runs under `kubectl exec` with no cwd control, and per-scenario so the
// suite's scenarios stay independent.
func manifestPathFor(cfg envConfig) string {
	return "/workspace/gateway-migration-" + cfg.Scenario + ".yaml"
}
