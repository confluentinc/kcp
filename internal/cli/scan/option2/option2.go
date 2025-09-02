package option2

import (
	"fmt"
	"log/slog"
	"os"

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

func NewScanOption2Cmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "option2",
		Short:         "Option 2 for scanning - clusters",
		Long:          "Option 2 for scanning - clusters for information that will help with migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption2,
		RunE:          runScanOption2,
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
func preRunScanOption2(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanOption2(cmd *cobra.Command, args []string) error {
	opts, err := parseScanOption2Opts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	slog.Info("🔍 scanning for MSK clusters", "opts", opts)

	return nil
}

func parseScanOption2Opts() (*cluster.ClusterScannerOpts, error) {
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

	var allSelectedClusters []string
	clusterMap := make(map[string]clusterInfo)
	regionClusterSelections := make(map[string][]string)

	// Build cluster map and initialize region selections
	for regionName, region := range credsYaml.Regions {
		regionClusterSelections[regionName] = []string{}
		for clusterArn, _ := range region.Clusters {
			key := fmt.Sprintf("%s|%s", regionName, clusterArn)
			clusterMap[key] = clusterInfo{region: regionName, clusterArn: clusterArn}
		}
	}

	// Interactive loop for region and cluster selection
	for {
		// Build region options with status indicators
		var regionOptions []huh.Option[string]
		for regionName := range credsYaml.Regions {
			selectedCount := len(regionClusterSelections[regionName])
			totalCount := len(credsYaml.Regions[regionName].Clusters)
			displayName := fmt.Sprintf("%s (%d/%d selected)", regionName, selectedCount, totalCount)
			regionOptions = append(regionOptions, huh.NewOption(displayName, regionName))
		}
		regionOptions = append(regionOptions, huh.NewOption("✓ Finish selection", "done"))

		var selectedRegion string
		regionForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select a region to configure clusters").
					Options(regionOptions...).
					Value(&selectedRegion),
			),
		)

		if err := regionForm.Run(); err != nil {
			return nil, fmt.Errorf("region selection error: %w", err)
		}

		if selectedRegion == "done" {
			break
		}

		// Build cluster options for selected region
		region := credsYaml.Regions[selectedRegion]
		var clusterOptions []huh.Option[string]
		currentSelections := regionClusterSelections[selectedRegion]

		for clusterArn, cluster := range region.Clusters {
			key := fmt.Sprintf("%s|%s", selectedRegion, clusterArn)
			clusterOptions = append(clusterOptions, huh.NewOption(cluster.ClusterName, key))
		}

		clusterForm := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title(fmt.Sprintf("Select clusters from %s", selectedRegion)).
					Description("Use space to select/deselect, enter to continue").
					Options(clusterOptions...).
					Value(&currentSelections),
			),
		)

		if err := clusterForm.Run(); err != nil {
			return nil, fmt.Errorf("cluster selection error: %w", err)
		}

		// Update selections for this region
		regionClusterSelections[selectedRegion] = currentSelections
	}

	// Collect all selected clusters from all regions
	for _, selections := range regionClusterSelections {
		allSelectedClusters = append(allSelectedClusters, selections...)
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
