package option1

import (
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/charmbracelet/huh"
	"github.com/confluentinc/kcp/internal/generators/scan/cluster"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/goccy/go-yaml"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	credentialsYaml string
)

func NewScanOption1Cmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "option1",
		Short:         "Option 1 for scanning - clusters",
		Long:          "Option 1 for scanning - clusters for information that will help with migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption1,
		RunE:          runScanOption1,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file used for authenticating to the MSK cluster(s).")

	clusterCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	clusterCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	clusterCmd.MarkFlagsOneRequired("credentials-yaml")

	return clusterCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunScanOption1(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanOption1(cmd *cobra.Command, args []string) error {
	opts, err := parseScanOption1Opts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	slog.Info("🔍 scanning for MSK clusters", "opts", opts)

	return nil
}

func parseScanOption1Opts() (*cluster.ClusterScannerOpts, error) {
	data, err := os.ReadFile(credentialsYaml)
	if err != nil {
		return nil, fmt.Errorf("failed to read creds.yaml file: %w", err)
	}

	var credsYaml types.CredsYaml
	if err := yaml.Unmarshal(data, &credsYaml); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	//do some validation on the credsFile
	for regionName, region := range credsYaml.Regions {
		for clusterArn, cluster := range region.Clusters {
			enabled := 0
			if cluster.AuthMethod.Unauthenticated != nil && cluster.AuthMethod.Unauthenticated.Use {
				enabled++
			}
			if cluster.AuthMethod.IAM != nil && cluster.AuthMethod.IAM.Use {
				enabled++
			}
			if cluster.AuthMethod.TLS != nil && cluster.AuthMethod.TLS.Use {
				enabled++
			}
			if cluster.AuthMethod.SASLScram != nil && cluster.AuthMethod.SASLScram.Use {
				enabled++
			}

			if enabled != 1 {
				return nil, fmt.Errorf("exactly one auth method must be enabled for cluster %s in region %s, found %d", clusterArn, regionName, enabled)
			}
		}
	}

	// Build region options
	var regionOptions []huh.Option[string]
	for regionName := range credsYaml.Regions {
		regionOptions = append(regionOptions, huh.NewOption(regionName, regionName))
	}

	var selectedRegions []string
	clusterMap := make(map[string]clusterInfo)
	regionSelections := make(map[string]*[]string)

	// Region selection group
	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select regions").
				Options(regionOptions...).
				Value(&selectedRegions),
		),
	}

	// Cluster selection groups (one per region)
	for regionName, region := range credsYaml.Regions {
		var clusterOptions []huh.Option[string]
		regionClusters := make([]string, 0)
		regionSelections[regionName] = &regionClusters

		for clusterArn, cluster := range region.Clusters {
			key := fmt.Sprintf("%s|%s", regionName, clusterArn)
			clusterOptions = append(clusterOptions, huh.NewOption(cluster.ClusterName, key))
			clusterMap[key] = clusterInfo{region: regionName, clusterArn: clusterArn}
		}

		group := huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(fmt.Sprintf("Select clusters from %s", regionName)).
				Options(clusterOptions...).
				Value(regionSelections[regionName]),
		).WithHideFunc(func() bool {
			return !slices.Contains(selectedRegions, regionName) // Hide this group
		})

		groups = append(groups, group)
	}

	form := huh.NewForm(groups...)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("form error: %w", err)
	}

	// Collect all selected clusters from all regions
	var allSelectedClusters []string
	for _, clusters := range regionSelections {
		allSelectedClusters = append(allSelectedClusters, *clusters...)
	}

	if len(allSelectedClusters) == 0 {
		return nil, fmt.Errorf("no clusters selected")
	}

	slog.Info("Selected clusters for scanning", "count", len(allSelectedClusters))
	for _, key := range allSelectedClusters {
		cluster := clusterMap[key]
		slog.Info("Will scan cluster", "cluster", cluster.clusterArn, "region", cluster.region)
	}

	// opts := cluster.ClusterScannerOpts{}
	// return &opts, nil
	return nil, nil
}

type clusterInfo struct {
	region     string
	clusterArn string
}
