package option5

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
	globalTheme     = huh.ThemeCatppuccin()
	currentState    = "main_menu" // Global state: "main_menu", "select_clusters", "review", "exit"
)

func NewScanOption5Cmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:           "option5",
		Short:         "Option 5 for scanning - clusters",
		Long:          "Option 5 for scanning - clusters for information that will help with migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption5,
		RunE:          runScanOption5,
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
func preRunScanOption5(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanOption5(cmd *cobra.Command, args []string) error {
	opts, err := parseScanOption5Opts()
	if err != nil {
		return fmt.Errorf("failed to parse scan cluster opts: %v", err)
	}

	slog.Info("🔍 scanning for MSK clusters", "opts", opts)

	return nil
}

func parseScanOption5Opts() (*cluster.ClusterScannerOpts, error) {
	slog.Info("🔍 parsing scan option 4 opts NEW APPROACH", "credentialsYaml", credentialsYaml)
	data, err := os.ReadFile(credentialsYaml)
	if err != nil {
		return nil, fmt.Errorf("failed to read creds.yaml file: %w", err)
	}

	var credsYaml types.CredsYaml
	if err := yaml.Unmarshal(data, &credsYaml); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if err := validateCredentials(credsYaml); err != nil {
		return nil, err
	}

	clusterMap := make(map[string]clusterInfo)
	regionSelections := make(map[string][]string)

	for regionName := range credsYaml.Regions {
		regionSelections[regionName] = []string{}
	}

	for {
		switch currentState {
		case "main_menu":
			if err := showMainMenu(); err != nil {
				return nil, fmt.Errorf("main menu error: %w", err)
			}

		case "select_clusters":
			if err := showClusterSelection(regionSelections, clusterMap, credsYaml); err != nil {
				return nil, fmt.Errorf("cluster selection error: %w", err)
			}

		case "review":
			allSelectedClusters := collectAllSelectedClusters(regionSelections)
			approved, err := showReviewForm(regionSelections, clusterMap, allSelectedClusters)
			if err != nil {
				return nil, fmt.Errorf("review form error: %w", err)
			}
			if approved {
				slog.Info("Selected clusters approved for scanning", "count", len(allSelectedClusters))
				for _, key := range allSelectedClusters {
					cluster := clusterMap[key]
					slog.Info("Will scan cluster", "cluster", cluster.clusterArn, "region", cluster.region)
				}
				return nil, nil
			}
			// If not approved, go back to main menu
			currentState = "main_menu"

		case "exit":
			slog.Info("User chose to exit")
			return nil, fmt.Errorf("user cancelled operation")

		default:
			currentState = "main_menu"
		}
	}
}

// validateCredentials ensures exactly one auth method is enabled per cluster
func validateCredentials(credsYaml types.CredsYaml) error {
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
				return fmt.Errorf("exactly one auth method must be enabled for cluster %s in region %s, found %d", clusterArn, regionName, enabled)
			}
		}
	}
	return nil
}

// collectAllSelectedClusters gathers all selected cluster keys from all regions
func collectAllSelectedClusters(regionSelections map[string][]string) []string {
	var allSelectedClusters []string
	for _, clusters := range regionSelections {
		allSelectedClusters = append(allSelectedClusters, clusters...)
	}
	return allSelectedClusters
}

// showMainMenu displays the main menu and updates currentState
func showMainMenu() error {
	var selectedAction string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("🏠 Main Menu").
				Description("Choose what you'd like to do").
				Options(
					huh.NewOption("🌍 Select Clusters", "select_clusters"),
					huh.NewOption("📋 Review Selections", "review"),
					huh.NewOption("🚪 Exit", "exit"),
				).
				Value(&selectedAction),
		),
	).WithTheme(globalTheme)

	if err := form.Run(); err != nil {
		return err
	}

	currentState = selectedAction
	return nil
}

// showClusterSelection handles the cluster selection flow with backward navigation
func showClusterSelection(regionSelections map[string][]string, clusterMap map[string]clusterInfo, credsYaml types.CredsYaml) error {
	var regionOptions []huh.Option[string]
	regionOptions = append(regionOptions, huh.NewOption("🏠 Back to Main Menu", "back_to_menu"))
	for regionName := range credsYaml.Regions {
		regionOptions = append(regionOptions, huh.NewOption(regionName, regionName))
	}

	var selectedRegion string
	var currentRegionClusters []string

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

	form := huh.NewForm(
		// Level 1: Region Selection
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a region").
				Options(regionOptions...).
				Value(&selectedRegion),
		),

		// Level 2: Cluster Selection
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select clusters (Shift+Tab back to regions)").
				Description("Select clusters to scan from the chosen region").
				OptionsFunc(func() []huh.Option[string] {
					// Restore previous selections for this region
					currentRegionClusters = regionSelections[selectedRegion]
					return regionClusterOptions[selectedRegion]
				}, &selectedRegion).
				Value(&currentRegionClusters),
		).WithHideFunc(func() bool {
			// Save state and control visibility
			if selectedRegion != "" && selectedRegion != "back_to_menu" {
				regionSelections[selectedRegion] = currentRegionClusters
			}
			return selectedRegion == "" || selectedRegion == "back_to_menu"
		}),
	).WithTheme(globalTheme)

	if err := form.Run(); err != nil {
		return err
	}

	// Check if user wants to go back to main menu
	if selectedRegion == "back_to_menu" {
		currentState = "main_menu"
		return nil
	}

	// Save final state
	if selectedRegion != "" {
		regionSelections[selectedRegion] = currentRegionClusters
	}

	// Go back to main menu when done
	currentState = "main_menu"
	return nil
}

// showReviewForm displays a dedicated form showing all selected clusters by region
// and asks for user confirmation to proceed
func showReviewForm(regionSelections map[string][]string, clusterMap map[string]clusterInfo, allSelectedClusters []string) (bool, error) {
	// Check if any clusters are selected
	if len(allSelectedClusters) == 0 {
		// Show a form that just informs the user and returns them to main menu
		var shouldQuit bool
		emptyForm := huh.NewForm(
			huh.NewGroup(
				huh.NewNote().
					Title("No Clusters Selected").
					Description("⚠️  You haven't selected any clusters yet.\n\nPlease go back to the main menu and select some clusters first."),
				huh.NewConfirm().
					Title("Return to main menu?").
					Affirmative("Yes, go back").
					Negative("No, quit").
					Value(&shouldQuit),
			),
		).WithTheme(globalTheme)

		if err := emptyForm.Run(); err != nil {
			return false, fmt.Errorf("empty form error: %w", err)
		}

		if !shouldQuit {
			// User chose "No, exit" - exit the application
			return false, fmt.Errorf("user chose to exit")
		}

		return false, nil // User chose "Yes, go back" - return to main menu
	}

	// Build the review summary
	var summaryText string = "📋 Clusters selected for scanning\n\n"

	for regionName, clusters := range regionSelections {
		if len(clusters) > 0 {
			summaryText += fmt.Sprintf("🌍 Region: %s\n", regionName)
			for _, clusterKey := range clusters {
				cluster := clusterMap[clusterKey]
				summaryText += fmt.Sprintf("  ✓ %s\n", cluster.clusterArn)
			}
			summaryText += "\n"
		}
	}

	var approved bool
	reviewForm := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Review Your Selections").
				Description(summaryText),
			huh.NewConfirm().
				Title("Are you happy with these selections?").
				Description("Select 'Yes' to proceed with scanning, or 'No' to return to the main menu").
				Affirmative("Yes, proceed with scanning").
				Negative("No, go back to main menu").
				Value(&approved),
		),
	).WithTheme(globalTheme)

	if err := reviewForm.Run(); err != nil {
		return false, fmt.Errorf("review form error: %w", err)
	}

	return approved, nil
}

type clusterInfo struct {
	region     string
	clusterArn string
}
