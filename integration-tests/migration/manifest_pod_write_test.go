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

// writeManifestToPod renders a scenario's GatewayMigration manifest and the
// three credentials files it references, and writes all four into the runner pod
// at mode 0600. Credentials are files now, not inline blocks, so the secret
// material lives in the credentials files while the manifest carries only paths.
//
// Every file travels over STDIN. Passing content as an argument would put a
// destination password into the container's process table and into the host
// kubectl command line; stdin puts it in neither. The mode and path-handling
// properties live in podWriteCommand, which is unit-tested without a cluster.
//
// Safe to call more than once for the same paths: re-rendering is how a scenario
// varies execute-time policy between init and execute, which is legal because execute
// reads policy fresh and the drift check compares topology only.
func writeManifestToPod(t *testing.T, cfg envConfig, podPath string, opts manifestOpts) {
	t.Helper()

	creds, err := renderCredentialFiles(opts)
	require.NoError(t, err, "rendering the credentials files for %s", podPath)
	// Redacted: test output is CI output, and these files are secret-bearing.
	writeFileToPod(t, cfg, opts.SourceCredPath, creds.Source)
	writeFileToPod(t, cfg, opts.DestKafkaCredPath, creds.DestKafka)
	writeFileToPod(t, cfg, opts.LinkCredPath, creds.Link)

	rendered, err := renderGatewayMigration(opts)
	require.NoError(t, err, "rendering the GatewayMigration manifest for %s", podPath)
	t.Logf("manifest written to %s:\n%s", podPath, manifestForLog(rendered))
	writeFileToPod(t, cfg, podPath, rendered)
}

// writeFileToPod writes content into the runner pod at podPath, mode 0600, over
// stdin. The content is redacted for the test log because a credentials file (or
// a manifest) may be secret-bearing.
func writeFileToPod(t *testing.T, cfg envConfig, podPath, content string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	argv := podWriteCommand(cfg.KubeContext, cfg.Namespace, cfg.KCPPod, podPath)
	cmd := exec.CommandContext(ctx, "kubectl", argv...)
	cmd.Stdin = strings.NewReader(content)

	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "writing %s into the pod: %s", podPath, stderr.String())
}

// e2eSwitchoverDomainName is the target every converted e2e scenario switches
// to — the destination streaming domain the initial CR fixture
// (testdata/manifests/templates/gateway-initial.yaml) already declares. The
// bootstrap server id kcp binds to is derived from that CR at init, not set
// per route, so it is no longer named here.
const e2eSwitchoverDomainName = "destination-kafka-cluster"

// manifestOptsFor builds the manifest options every scenario shares, from the
// topology setup.sh published into .env. Callers set metadata.name, the manifest
// path, pause intent and policy on top.
func manifestOptsFor(cfg envConfig) manifestOpts {
	fenceRoutes := make([]fenceRouteOpts, len(cfg.FenceRoutes))
	for i, name := range cfg.FenceRoutes {
		fenceRoutes[i] = fenceRouteOpts{
			Name:                 name,
			SwitchoverDomainName: e2eSwitchoverDomainName,
		}
	}
	return manifestOpts{
		MetadataName:      "e2e-" + cfg.Scenario,
		SourceBootstrap:   cfg.SourceBootstrap,
		DestBootstrap:     cfg.DestBootstrap,
		DestClusterID:     cfg.DestClusterID,
		RestEndpoint:      cfg.RestProxyEndpoint,
		ClusterLinkName:   cfg.ClusterLinkName,
		APIKey:            cfg.ClusterAPIKey,
		APISecret:         cfg.ClusterAPISecret,
		Namespace:         cfg.Namespace,
		GatewayName:       cfg.GatewayName,
		FenceRoutes:       fenceRoutes,
		KubePath:          cfg.KubePath,
		SourceCredPath:    "/workspace/source-creds-" + cfg.Scenario + ".yaml",
		DestKafkaCredPath: "/workspace/dest-kafka-creds-" + cfg.Scenario + ".yaml",
		LinkCredPath:      "/workspace/link-creds-" + cfg.Scenario + ".yaml",
	}
}

// manifestPathFor is the in-pod path for a scenario's manifest. Absolute because
// kcp runs under `kubectl exec` with no cwd control, and per-scenario so the
// suite's scenarios stay independent.
func manifestPathFor(cfg envConfig) string {
	return "/workspace/gateway-migration-" + cfg.Scenario + ".yaml"
}
