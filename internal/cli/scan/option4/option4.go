package option4

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

func NewScanOption4Cmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "option4",
		Short:         "Option 4 for scanning - clusters",
		Long:          "Option 4 for scanning - clusters for information that will help with migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption4,
		RunE:          runScanOption4,
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
func preRunScanOption4(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanOption4(cmd *cobra.Command, args []string) error {
	opts, err := parseScanOption4Opts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	slog.Info("🔍 scanning for MSK clusters", "opts", opts)

	return nil
}

func parseScanOption4Opts() (*cluster.ClusterScannerOpts, error) {
	slog.Info("🔍 parsing scan option 4 opts NEW APPROACH", "credentialsYaml", credentialsYaml)
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

	var selectedRegion string
	clusterMap := make(map[string]clusterInfo)

	// Track selections per region to maintain state across region switches
	regionSelections := make(map[string]*[]string)

	// Initialize empty selections for each region
	for regionName := range credsYaml.Regions {
		emptySlice := make([]string, 0)
		regionSelections[regionName] = &emptySlice
	}

	// Build cluster options for each region
	regionClusterOptions := make(map[string][]huh.Option[string])
	for regionName, region := range credsYaml.Regions {
		var clusterOptions []huh.Option[string]
		for clusterArn, cluster := range region.Clusters {
			key := fmt.Sprintf("%s|%s", regionName, clusterArn)
			clusterOptions = append(clusterOptions, huh.NewOption(cluster.ClusterName, key))
			clusterMap[key] = clusterInfo{region: regionName, clusterArn: clusterArn}
		}
		regionClusterOptions[regionName] = clusterOptions
	}

	// Current region's cluster selections (dynamically updated)
	// This slice is what the multi-select form field binds to
	var currentRegionClusters []string

	// ========================================
	// THREE-LEVEL NAVIGATION LOOP
	// ========================================
	for {
		var mainAction string

		form := huh.NewForm(
			// Level 1: Main Menu (Shift+Tab from regions comes here)
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Main Menu (Tab to regions)").
					Options(
						huh.NewOption("🌍 Select Clusters", "select_clusters"),
						huh.NewOption("📋 Review Selections", "review"),
						huh.NewOption("✅ Submit & Continue", "submit"),
						huh.NewOption("❌ Cancel", "cancel"),
					).
					Value(&mainAction),
			),

			// Level 2: Region Selection (Shift+Tab to main menu, Tab to clusters)
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select a region (Shift+Tab to menu, Tab to clusters)").
					Options(regionOptions...).
					Value(&selectedRegion),
			).WithHideFunc(func() bool {
				// Only show if user selected "select_clusters"
				return mainAction != "select_clusters"
			}),

			// Level 3: Cluster Selection (Shift+Tab to regions)
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Select clusters (Shift+Tab back to regions)").
					Description("Select clusters to scan from the chosen region").
					OptionsFunc(func() []huh.Option[string] {
						if selectedRegion == "" || mainAction != "select_clusters" {
							return []huh.Option[string]{}
						}
						// Restore previous selections for this region
						if regionSelections[selectedRegion] != nil {
							currentRegionClusters = *regionSelections[selectedRegion]
						}
						return regionClusterOptions[selectedRegion]
					}, &selectedRegion).
					Value(&currentRegionClusters),
			).WithHideFunc(func() bool {
				// Save state and control visibility
				if selectedRegion != "" && regionSelections[selectedRegion] != nil {
					*regionSelections[selectedRegion] = currentRegionClusters
				}
				// Only show if user selected "select_clusters" and has a region
				return mainAction != "select_clusters" || selectedRegion == ""
			}),
		)

		if err := form.Run(); err != nil {
			return nil, fmt.Errorf("form error: %w", err)
		}

		// Handle the final action
		switch mainAction {
		case "review":
			// Show current selections
			var summaryText string = "📋 CURRENT SELECTIONS\n\n"
			hasSelections := false

			for regionName, clusters := range regionSelections {
				if len(*clusters) > 0 {
					hasSelections = true
					summaryText += fmt.Sprintf("🌍 Region: %s\n", regionName)
					for _, clusterKey := range *clusters {
						cluster := clusterMap[clusterKey]
						summaryText += fmt.Sprintf("  ✓ %s\n", cluster.clusterArn)
					}
					summaryText += "\n"
				}
			}

			if !hasSelections {
				summaryText += "No clusters selected yet.\n"
			}

			fmt.Println(summaryText)
			// Continue the loop to show menu again

		case "submit":
			// Collect all selections and proceed
			var allSelectedClusters []string
			for _, clusters := range regionSelections {
				allSelectedClusters = append(allSelectedClusters, *clusters...)
			}

			if len(allSelectedClusters) == 0 {
				slog.Warn("No clusters selected - please select some clusters first")
				continue // Continue the loop
			}

			slog.Info("Selected clusters for scanning", "count", len(allSelectedClusters))
			for _, key := range allSelectedClusters {
				cluster := clusterMap[key]
				slog.Info("Will scan cluster", "cluster", cluster.clusterArn, "region", cluster.region)
			}

			// Exit the loop and continue with scanning
			goto scanApproved

		case "cancel":
			return nil, fmt.Errorf("scan cancelled by user")

		case "select_clusters":
			// Save final selections and continue loop to show menu again
			if selectedRegion != "" && regionSelections[selectedRegion] != nil {
				*regionSelections[selectedRegion] = currentRegionClusters
				slog.Info("Updated selections", "region", selectedRegion, "clusters", len(currentRegionClusters))
			}
			// Continue the loop to show menu again
		}
	}

scanApproved:

	// opts := cluster.ClusterScannerOpts{}
	// return &opts, nil
	return nil, nil
}

type clusterInfo struct {
	region     string
	clusterArn string
}
