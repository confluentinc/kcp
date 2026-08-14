package lagcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

// clusterLinkVerifyTimeout bounds the pre-TUI GetClusterLink probe so the
// command fails fast with a clear error instead of hanging on a bad endpoint.
const clusterLinkVerifyTimeout = 15 * time.Second

const (
	minPollInterval = 1
	maxPollInterval = 60
)

var (
	manifestFile string
	pollInterval int
)

func NewMigrationLagCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lag-check",
		Short: "Show mirror topic lag for the cluster link",
		Long: `Interactive TUI that displays mirror topic lag for the cluster link. Run in a terminal.
Press q to quit, p to toggle partition details, r to refresh, +/- to adjust interval, arrow keys to scroll.

Everything it needs — the destination REST endpoint, cluster id, link name and credentials —
comes from the GatewayMigration manifest, so this command reads no state file and works
before 'kcp migration init' has ever run.

It always shows every mirror topic on the link; spec.topics does not narrow the view.`,
		Example:       `  kcp migration lag-check -f gateway-migration.yaml`,
		SilenceErrors: true,
		// A runtime failure must not bury the error under Cobra's usage block.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		PreRunE:      preRunMigrationLag,
		RunE:         runMigrationLag,
	}

	cmd.Flags().StringVarP(&manifestFile, "file", "f", "", "Path to the GatewayMigration manifest describing this migration.")
	cmd.Flags().IntVar(&pollInterval, "poll-interval", 1, "Poll interval in seconds (1-60)")

	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func preRunMigrationLag(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationLag(cmd *cobra.Command, args []string) error {
	g, err := manifest.LoadGatewayMigrationFile(manifestFile)
	if err != nil {
		return err
	}

	config, httpClient, err := buildLagCheckConfig(g)
	if err != nil {
		return err
	}

	svc := clusterlink.NewConfluentCloudService(httpClient)

	ctx, cancel := context.WithTimeout(cmd.Context(), clusterLinkVerifyTimeout)
	defer cancel()
	if _, err := svc.GetClusterLink(ctx, config); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out verifying cluster link after %s — check network connectivity to %s", clusterLinkVerifyTimeout, config.RestEndpoint)
		}
		return fmt.Errorf("failed to verify cluster link: %w", err)
	}

	model := newModel(svc, config, clampPollInterval(pollInterval))
	p := newProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}

// buildLagCheckConfig maps the manifest onto the five values clusterlink.Config
// needs, plus an HTTP client carrying the REST leg's TLS trust — so a private-CA
// or self-signed destination is reachable rather than the manifest's ca_cert
// being a silent no-op.
//
// Topics is deliberately empty: this command always reports every mirror topic
// on the link, and spec.topics does not narrow it.
func buildLagCheckConfig(g *manifest.GatewayMigration) (clusterlink.Config, clusterlink.HTTPClient, error) {
	restCreds, err := g.RestCredentials()
	if err != nil {
		return clusterlink.Config{}, nil, fmt.Errorf("resolving destination REST credentials: %w", err)
	}

	httpClient, err := migration.NewRESTHTTPClient(restCreds.CACert, restCreds.InsecureSkipVerify)
	if err != nil {
		return clusterlink.Config{}, nil, fmt.Errorf("building destination REST client: %w", err)
	}

	return clusterlink.Config{
		RestEndpoint: g.Spec.Target.Kafka.RestEndpoint,
		ClusterID:    g.Spec.Target.ClusterID,
		LinkName:     g.Spec.ClusterLink.Name,
		APIKey:       restCreds.APIKey,
		APISecret:    restCreds.APISecret,
		Topics:       []string{},
	}, httpClient, nil
}

// clampPollInterval keeps the TUI refresh inside its documented 1-60s range.
func clampPollInterval(seconds int) int {
	return min(max(seconds, minPollInterval), maxPollInterval)
}
