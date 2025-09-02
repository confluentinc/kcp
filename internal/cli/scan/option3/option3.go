package option3

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

func NewScanOption3Cmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "option3",
		Short:         "Option 3 for scanning - clusters",
		Long:          "Option 3 for scanning - clusters for information that will help with migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption3,
		RunE:          runScanOption3,
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
func preRunScanOption3(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanOption3(cmd *cobra.Command, args []string) error {
	opts, err := parseScanOption3Opts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	slog.Info("🔍 scanning for MSK clusters", "opts", opts)

	return nil
}

func parseScanOption3Opts() (*cluster.ClusterScannerOpts, error) {
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
	// HUH FORM CREATION - THE MAIN MAGIC
	// ========================================
	form := huh.NewForm(
		// Create a single group containing both our fields
		// Groups allow related fields to be navigated together with Tab/Shift+Tab
		huh.NewGroup(

			// ========================================
			// REGION SELECTOR (Single Select Dropdown)
			// ========================================
			huh.NewSelect[string](). // Create single-select dropdown for string values
							Title("Select a region").    // Display title above the dropdown
							Options(regionOptions...).   // Static list of region options (eu-west-1, us-east-1, etc.)
							Value(&selectedRegion).      // Bind to selectedRegion variable - when user picks, this updates
							WithTheme(huh.ThemeCharm()), // Apply Charm's default styling

			// ========================================
			// CLUSTER SELECTOR (Multi Select with Dynamic Options)
			// ========================================
			huh.NewMultiSelect[string](). // Create multi-select checkbox list for string values
							Title("Select clusters").                                      // Display title above the checkboxes
							Description("Select clusters to scan from the chosen region"). // Helper text

				// *** THE DYNAMIC MAGIC - OptionsFunc ***
				// This function runs every time selectedRegion changes
				OptionsFunc(func() []huh.Option[string] {
					// If no region selected yet, show no clusters
					if selectedRegion == "" {
						return []huh.Option[string]{}
					}

					// *** STATE RESTORATION ***
					// When user switches regions, restore their previous selections for this region
					// Example: User had selected cluster1,cluster2 in us-east-1, then switched to eu-west-1
					// When they switch back to us-east-1, this restores cluster1,cluster2 selections
					if regionSelections[selectedRegion] != nil {
						currentRegionClusters = *regionSelections[selectedRegion]
					}

					// Return the cluster options for the currently selected region
					// Example: If selectedRegion = "us-east-1", return clusters for us-east-1
					return regionClusterOptions[selectedRegion]
				}, &selectedRegion). // Watch selectedRegion - rerun function when it changes

				Value(&currentRegionClusters). // Bind to currentRegionClusters - user selections go here
				WithTheme(huh.ThemeCharm()),   // Apply Charm's default styling

		// ========================================
		// STATE PERSISTENCE HACK - WithHideFunc
		// ========================================
		).WithHideFunc(func() bool {
			// This function is called on every form update/render
			// We don't actually want to hide the group, but we use this as a hook to save state

			// *** STATE SAVING ***
			// Save current cluster selections back to the region's permanent storage
			// Example: User selected cluster3,cluster4 in eu-west-1, save these to regionSelections["eu-west-1"]
			if selectedRegion != "" && regionSelections[selectedRegion] != nil {
				*regionSelections[selectedRegion] = currentRegionClusters
			}

			return false // Never actually hide the group - we just want the state saving side effect
		}),
	)

	// ========================================
	// RUN THE FORM - USER INTERACTION HAPPENS HERE
	// ========================================
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("form error: %w", err)
	}
	// At this point, user has finished interacting with the form

	// ========================================
	// FINAL STATE CLEANUP
	// ========================================
	// Save final selections for the last selected region
	// The WithHideFunc saves state during navigation, but we need one final save
	// for whatever region the user was on when they pressed Enter to finish
	if selectedRegion != "" && regionSelections[selectedRegion] != nil {
		*regionSelections[selectedRegion] = currentRegionClusters
	}

	// ========================================
	// COLLECT ALL SELECTIONS FROM ALL REGIONS
	// ========================================
	// Now we have:
	// regionSelections["us-east-1"] = &["us-east-1|cluster1", "us-east-1|cluster2"]
	// regionSelections["eu-west-1"] = &["eu-west-1|cluster3", "eu-west-1|cluster4", "eu-west-1|cluster5"]
	//
	// Combine them all into one final list
	var allSelectedClusters []string
	for regionName, clusters := range regionSelections {
		// Dereference the pointer and append all clusters from this region
		// Example: append ["us-east-1|cluster1", "us-east-1|cluster2"] to allSelectedClusters
		allSelectedClusters = append(allSelectedClusters, *clusters...)

		// Debug info: could log here to see what we got from each region
		fmt.Printf("Region %s contributed %d clusters\n", regionName, len(*clusters))
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
