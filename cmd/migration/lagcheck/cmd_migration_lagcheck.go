package lagcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// clusterLinkVerifyTimeout bounds the pre-TUI GetClusterLink probe so the
// command fails fast with a clear error instead of hanging on a bad endpoint.
const clusterLinkVerifyTimeout = 15 * time.Second

var (
	restEndpoint string
	clusterID    string
	linkName     string
	apiKey       string
	apiSecret    string
	pollInterval int
)

func NewMigrationLagCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lag-check",
		Short: "Show mirror topic lag for the cluster link",
		Long:  "Interactive TUI that displays mirror topic lag for the cluster link. Run in a terminal with cluster link credentials. Press q to quit, p to toggle partition details, r to refresh, +/- to adjust interval, arrow keys to scroll.",
		Example: `  kcp migration lag-check --rest-endpoint https://... --cluster-id lkc-xxx --cluster-link-name my-link --cluster-api-key xxx --cluster-api-secret xxx
  All flags can be provided via environment variables (uppercase, with underscores).`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationLagCheck,
		RunE:          runMigrationLagCheck,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&restEndpoint, "rest-endpoint", "", "Cluster link REST endpoint")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "Cluster link cluster ID")
	requiredFlags.StringVar(&linkName, "cluster-link-name", "", "Cluster link name")
	requiredFlags.StringVar(&apiKey, "cluster-api-key", "", "Cluster link API key")
	requiredFlags.StringVar(&apiSecret, "cluster-api-secret", "", "Cluster link API secret")
	cmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.IntVar(&pollInterval, "poll-interval", 1, "Poll interval in seconds (1-60)")
	cmd.Flags().AddFlagSet(optionalFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		fmt.Printf("Required:\n%s\n", requiredFlags.FlagUsages())
		fmt.Printf("Optional:\n%s\n", optionalFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = cmd.MarkFlagRequired("rest-endpoint")
	_ = cmd.MarkFlagRequired("cluster-id")
	_ = cmd.MarkFlagRequired("cluster-link-name")
	_ = cmd.MarkFlagRequired("cluster-api-key")
	_ = cmd.MarkFlagRequired("cluster-api-secret")

	return cmd
}

func preRunMigrationLagCheck(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationLagCheck(cmd *cobra.Command, args []string) error {
	interval := max(pollInterval, 1)
	interval = min(interval, 60)

	config := clusterlink.Config{
		RestEndpoint: restEndpoint,
		ClusterID:    clusterID,
		LinkName:     linkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       []string{}, // return all mirror topics
	}

	svc := clusterlink.NewConfluentCloudService(nil)

	ctx, cancel := context.WithTimeout(cmd.Context(), clusterLinkVerifyTimeout)
	defer cancel()
	if _, err := svc.GetClusterLink(ctx, config); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out verifying cluster link after %s — check network connectivity to %s", clusterLinkVerifyTimeout, restEndpoint)
		}
		return fmt.Errorf("failed to verify cluster link: %w", err)
	}

	model := newModel(svc, config, interval)
	p := newProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}
