// Package option5 implements a clean, state-machine-driven UI for MSK cluster selection.
//
// ARCHITECTURE OVERVIEW:
// This package uses a simple but powerful state machine pattern to manage UI flow.
// Instead of complex nested forms or callback chains, we use a single global state
// variable (uiState) that controls which UI screen is displayed.
//
// DESIGN PATTERNS:
// 1. State Machine: uiState controls the entire application flow
// 2. Separation of Concerns: Each function handles one UI screen
// 3. Persistent State: Cluster selections are preserved across navigation
// 4. Natural Navigation: Uses huh's built-in Shift+Tab within forms, explicit back buttons between forms
//
// UI FLOW:
// Main Menu → Select Clusters → Region Selection → Cluster Selection → Review → Exit
//
//	↑            ↑                ↑                    ↑             ↑        ↑
//	└────────────┴────────────────┴────────────────────┴─────────────┴────────┘
//	                      (All paths controlled by uiState)
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
	// credentialsYaml holds the path to the YAML file containing MSK cluster credentials
	credentialsYaml string

	// globalTheme provides consistent styling across all huh forms
	globalTheme = huh.ThemeCatppuccin()

	// uiState is the heart of our state machine - controls which UI screen is displayed
	// Possible values: "main_menu", "select_clusters", "review", "exit"
	// This single variable orchestrates the entire application flow
	uiState = "main_menu"
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

// parseScanOption5Opts orchestrates the entire cluster selection workflow using a state machine.
//
// STATE MACHINE DESIGN:
// This function implements the core state machine that drives the UI flow. Instead of
// complex nested callbacks or form hierarchies, we use a simple loop with a switch
// statement that responds to the current uiState.
//
// KEY DESIGN DECISIONS:
// 1. Persistent State: regionSelections and clusterMap persist across all UI transitions
// 2. Single Responsibility: Each case handles one UI state cleanly
// 3. Error Boundaries: Each state function can return errors without breaking the flow
// 4. Clean Exit: Both user cancellation and successful completion exit through the same mechanism
//
// DATA FLOW:
// - regionSelections: Maps region names to selected cluster keys (persists user choices)
// - clusterMap: Maps cluster keys to cluster info (enables lookups during review)
// - credsYaml: Immutable configuration loaded once at startup
func parseScanOption5Opts() (*cluster.ClusterScannerOpts, error) {
	slog.Info("🔍 parsing scan option 5 opts using state machine approach", "credentialsYaml", credentialsYaml)

	// Load and validate the credentials file once at startup
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

	// Initialize persistent state that survives UI navigation
	// clusterMap: Maps "region|clusterArn" keys to cluster metadata for lookups
	clusterMap := make(map[string]clusterInfo)

	// regionSelections: Maps region names to arrays of selected cluster keys
	// This is the core state that preserves user selections across navigation
	regionSelections := make(map[string][]string)

	// Initialize empty selection arrays for each region
	for regionName := range credsYaml.Regions {
		regionSelections[regionName] = []string{}
	}

	// STATE MACHINE LOOP:
	// This infinite loop drives the entire application. Each iteration handles
	// one UI state, and state transitions are controlled by updating uiState.
	for {
		switch uiState {
		case "main_menu":
			// Display main menu and wait for user choice
			// Updates uiState based on user selection
			if err := showMainMenu(); err != nil {
				return nil, fmt.Errorf("main menu error: %w", err)
			}

		case "select_clusters":
			// Handle cluster selection workflow (region → clusters)
			// May update uiState to "main_menu" if user goes back
			if err := showClusterSelection(regionSelections, clusterMap, credsYaml); err != nil {
				return nil, fmt.Errorf("cluster selection error: %w", err)
			}

		case "review":
			// Show selected clusters and get final confirmation
			// Updates uiState to either "exit" (approved) or "main_menu" (declined)
			allSelectedClusters := collectAllSelectedClusters(regionSelections)
			err := showReviewForm(regionSelections, clusterMap, allSelectedClusters)
			if err != nil {
				return nil, fmt.Errorf("review form error: %w", err)
			}

		case "exit":
			// Clean exit point for both cancellation and successful completion
			slog.Info("User chose to exit")
			return nil, fmt.Errorf("user cancelled operation")

		default:
			// Safety net: if uiState gets corrupted, reset to main menu
			uiState = "main_menu"
		}
	}
}

// validateCredentials ensures the credentials YAML file is properly configured.
//
// VALIDATION RULES:
// Each MSK cluster must have exactly one authentication method enabled.
// This prevents configuration errors that would cause connection failures later.
//
// SUPPORTED AUTH METHODS:
// - Unauthenticated: No authentication required
// - IAM: AWS IAM-based authentication
// - TLS: Certificate-based authentication
// - SASL/SCRAM: Username/password authentication
func validateCredentials(credsYaml types.CredsYaml) error {
	for regionName, region := range credsYaml.Regions {
		for clusterArn, cluster := range region.Clusters {
			// Count how many auth methods are enabled for this cluster
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

			// Exactly one auth method must be enabled per cluster
			if enabled != 1 {
				return fmt.Errorf("exactly one auth method must be enabled for cluster %s in region %s, found %d", clusterArn, regionName, enabled)
			}
		}
	}
	return nil
}

// collectAllSelectedClusters flattens the regionSelections map into a single array.
//
// PURPOSE:
// The regionSelections map organizes selections by region for easy management,
// but we often need a flat list of all selected clusters (e.g., for counting,
// logging, or passing to other functions).
//
// RETURNS:
// Array of cluster keys in "region|clusterArn" format
func collectAllSelectedClusters(regionSelections map[string][]string) []string {
	var allSelectedClusters []string
	for _, clusters := range regionSelections {
		allSelectedClusters = append(allSelectedClusters, clusters...)
	}
	return allSelectedClusters
}

// showMainMenu displays the top-level navigation menu.
//
// STATE MACHINE ROLE:
// This is the entry point and central hub of the application. Users can:
// 1. Navigate to cluster selection
// 2. Review their current selections
// 3. Exit the application
//
// DESIGN NOTES:
// - Simple single-select form with clear options
// - Updates uiState directly based on user choice
// - No complex logic - just navigation routing
// - Always returns to this menu after other operations complete
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

	// Update global state to trigger the next UI screen
	uiState = selectedAction
	return nil
}

// showClusterSelection handles the two-level cluster selection workflow.
//
// WORKFLOW DESIGN:
// This function implements a two-level selection process:
// 1. Region Selection: User picks which AWS region to explore
// 2. Cluster Selection: User multi-selects clusters within that region
//
// NAVIGATION PATTERNS:
// - Explicit Back Button: "← Back to Main Menu" for cross-form navigation
// - Shift+Tab Navigation: Works within the form (region ↔ clusters)
// - State Persistence: Selections are saved immediately and restored when returning
//
// KEY TECHNICAL DETAILS:
// - OptionsFunc: Dynamically loads cluster options based on selected region
// - WithHideFunc: Controls when cluster selection is visible + saves state
// - Composite Keys: Uses "region|clusterArn" format for unique cluster identification
//
// STATE MANAGEMENT:
// - regionSelections: Preserves user choices across navigation
// - clusterMap: Populated during initialization for later lookups
// - currentRegionClusters: Local variable that syncs with persistent state
func showClusterSelection(regionSelections map[string][]string, clusterMap map[string]clusterInfo, credsYaml types.CredsYaml) error {
	// Build region options with explicit back navigation
	var regionOptions []huh.Option[string]
	regionOptions = append(regionOptions, huh.NewOption("← Back to Main Menu", "back_to_menu"))
	for regionName := range credsYaml.Regions {
		regionOptions = append(regionOptions, huh.NewOption(regionName, regionName))
	}

	// Local form state variables
	var selectedRegion string
	var currentRegionClusters []string

	// Pre-build cluster options for each region and populate clusterMap
	// This happens once at form initialization for performance
	regionClusterOptions := make(map[string][]huh.Option[string])
	for regionName, region := range credsYaml.Regions {
		var clusterOptions []huh.Option[string]
		for clusterArn, cluster := range region.Clusters {
			// Create composite key for unique cluster identification across regions
			key := fmt.Sprintf("%s|%s", regionName, clusterArn)
			clusterOptions = append(clusterOptions, huh.NewOption(cluster.ClusterName, key))
			// Populate clusterMap for later lookups during review
			clusterMap[key] = clusterInfo{region: regionName, clusterArn: clusterArn}
		}
		regionClusterOptions[regionName] = clusterOptions
	}

	// Two-level form: Region selection → Cluster multi-selection
	form := huh.NewForm(
		// Level 1: Region Selection
		// Always visible, allows user to pick region or go back to main menu
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a region").
				Options(regionOptions...).
				Value(&selectedRegion),
		),

		// Level 2: Cluster Selection
		// Only visible when a region is selected, supports multi-selection
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select clusters (Shift+Tab back to regions)").
				Description("Select clusters to scan from the chosen region").
				// OptionsFunc: Dynamically loads clusters based on selected region
				OptionsFunc(func() []huh.Option[string] {
					// Restore previous selections for this region from persistent state
					currentRegionClusters = regionSelections[selectedRegion]
					return regionClusterOptions[selectedRegion]
				}, &selectedRegion).
				Value(&currentRegionClusters),
		).WithHideFunc(func() bool {
			// CRITICAL: Save state every time this function is called
			// This ensures selections are preserved during navigation
			if selectedRegion != "" && selectedRegion != "back_to_menu" {
				regionSelections[selectedRegion] = currentRegionClusters
			}
			// Hide cluster selection if no region selected or user chose to go back
			return selectedRegion == "" || selectedRegion == "back_to_menu"
		}),
	).WithTheme(globalTheme)

	if err := form.Run(); err != nil {
		return err
	}

	// Handle explicit back navigation
	if selectedRegion == "back_to_menu" {
		uiState = "main_menu"
		return nil
	}

	// Final state save before transitioning
	if selectedRegion != "" {
		regionSelections[selectedRegion] = currentRegionClusters
	}

	// Always return to main menu after cluster selection completes
	uiState = "main_menu"
	return nil
}

// showReviewForm displays selected clusters and handles final user confirmation.
//
// DUAL PURPOSE DESIGN:
// This function handles two distinct scenarios with different UI flows:
// 1. No Clusters Selected: Inform user and offer to return to main menu or exit
// 2. Clusters Selected: Show summary and get final approval to proceed
//
// STATE MACHINE INTEGRATION:
// Instead of returning boolean values, this function updates the global uiState
// to control the next step in the application flow. This keeps state management
// centralized and consistent with the overall architecture.
//
// DECISION OUTCOMES:
// - No clusters + "go back": uiState = "main_menu"
// - No clusters + "quit": uiState = "exit"
// - Has clusters + "proceed": uiState = "exit" (after logging scan details)
// - Has clusters + "go back": uiState = "main_menu"
//
// DESIGN PATTERN:
// Uses the same simplified form pattern as option4 - conditional content
// setup followed by a single form creation and result handling.
func showReviewForm(regionSelections map[string][]string, clusterMap map[string]clusterInfo, allSelectedClusters []string) error {
	// SCENARIO 1: No clusters selected
	// Show informational message and offer navigation options
	if len(allSelectedClusters) == 0 {
		var shouldContinue bool
		emptyForm := huh.NewForm(
			huh.NewGroup(
				huh.NewNote().
					Title("No Clusters Selected").
					Description("⚠️  You haven't selected any clusters yet.\n\nPlease go back to the main menu and select some clusters first."),
				huh.NewConfirm().
					Title("Return to main menu?").
					Affirmative("Yes, go back").
					Negative("No, quit").
					Value(&shouldContinue),
			),
		).WithTheme(globalTheme)

		if err := emptyForm.Run(); err != nil {
			return fmt.Errorf("empty form error: %w", err)
		}

		// Update global state based on user choice
		if shouldContinue {
			uiState = "main_menu"
		} else {
			uiState = "exit"
		}

		return nil
	}

	// SCENARIO 2: Clusters are selected
	// Build summary of selections organized by region
	var summaryText string = "📋 Clusters selected for scanning\n\n"

	for regionName, clusters := range regionSelections {
		if len(clusters) > 0 {
			summaryText += fmt.Sprintf("🌍 Region: %s\n", regionName)
			for _, clusterKey := range clusters {
				// Look up cluster details using the composite key
				cluster := clusterMap[clusterKey]
				summaryText += fmt.Sprintf("  ✓ %s\n", cluster.clusterArn)
			}
			summaryText += "\n"
		}
	}

	// Present summary and get final confirmation
	var shouldProceed bool
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
				Value(&shouldProceed),
		),
	).WithTheme(globalTheme)

	if err := reviewForm.Run(); err != nil {
		return fmt.Errorf("review form error: %w", err)
	}

	// Handle final decision and update global state
	if shouldProceed {
		// Log scanning details before proceeding
		slog.Info("Selected clusters approved for scanning", "count", len(allSelectedClusters))
		for _, key := range allSelectedClusters {
			cluster := clusterMap[key]
			slog.Info("Will scan cluster", "cluster", cluster.clusterArn, "region", cluster.region)
		}
		// Set state to exit - scanning will begin
		uiState = "exit"
	} else {
		// User declined - return to main menu for more selections
		uiState = "main_menu"
	}

	return nil
}

// clusterInfo holds metadata for a specific MSK cluster.
//
// PURPOSE:
// This struct enables efficient lookups during the review phase. Instead of
// parsing composite keys like "us-east-1|arn:aws:kafka:...", we can quickly
// access the region and ARN components for display and logging.
//
// USAGE:
// Populated during cluster selection initialization and used during review
// to display cluster details in a user-friendly format.
type clusterInfo struct {
	region     string // AWS region name (e.g., "us-east-1")
	clusterArn string // Full MSK cluster ARN
}
